package relay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DatanoiseTV/tinyice/logger"
	"github.com/kazzmir/opus-go/ogg"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/pion/webrtc/v4/pkg/media/oggwriter"
)

type SimplePacer struct {
	startTime time.Time
	sentMS    int64
}

func (p *SimplePacer) Pace(duration time.Duration) {
	if p.startTime.IsZero() {
		p.startTime = time.Now()
	}
	p.sentMS += duration.Milliseconds()
	targetTime := p.startTime.Add(time.Duration(p.sentMS) * time.Millisecond)

	wait := time.Until(targetTime)

	// Catch-up logic: If we are more than 500ms behind, reset the pacer baseline
	if wait < -500*time.Millisecond {
		p.startTime = time.Now()
		p.sentMS = 0
		return
	}

	if wait > 0 {
		time.Sleep(wait)
	}
}

type WebRTCManager struct {
	api *webrtc.API
	// sourceAPI is used for ingest only. Its MediaEngine registers just
	// Opus and H.264, so a publishing browser must send H.264 video or
	// fail negotiation outright. The alternative is accepting VP8 and
	// having nothing downstream able to use it: HLS, the mpegts muxer
	// and WHEP playback are all H.264, and there is no video transcoder
	// in the binary. Playback keeps the default codec set.
	sourceAPI       *webrtc.API
	relay           *Relay
	mu              sync.RWMutex
	sources         map[string]*webrtc.PeerConnection
	runtimeRegistry *RuntimeRegistry
	listenerLimit   func(string) int
	// sourceDone[mount] is closed by the OnTrack pump goroutine when
	// it exits. Lets a successor HandleSourceOffer wait for the
	// previous pump to drain before starting its own, preventing the
	// brief two-pumps-into-one-stream overlap that produces torn Ogg
	// pages. Indexed by mount so each source rotation is isolated.
	sourceDone map[string]chan struct{}
}

func NewWebRTCManager(r *Relay) *WebRTCManager {
	s := webrtc.SettingEngine{}
	// Optimize for many listeners
	s.SetICETimeouts(10*time.Second, 20*time.Second, 2*time.Second)

	api := webrtc.NewAPI(webrtc.WithSettingEngine(s))

	sourceAPI := api
	if me, err := newSourceMediaEngine(); err != nil {
		logger.L.Errorw("WebRTC: source media engine failed; ingest falls back to the default codec set", "error", err)
	} else {
		sourceAPI = webrtc.NewAPI(webrtc.WithSettingEngine(s), webrtc.WithMediaEngine(me))
	}

	return &WebRTCManager{
		api:        api,
		sourceAPI:  sourceAPI,
		relay:      r,
		sources:    make(map[string]*webrtc.PeerConnection),
		sourceDone: make(map[string]chan struct{}),
	}
}

func (wm *WebRTCManager) SetRuntimeRegistry(rr *RuntimeRegistry) {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	wm.runtimeRegistry = rr
}

func (wm *WebRTCManager) SetListenerLimit(resolve func(string) int) { wm.listenerLimit = resolve }
func (wm *WebRTCManager) admitPlayback(mount string, details ...ListenerSnapshot) (*Listener, error) {
	l := &Listener{ID: fmt.Sprintf("webrtc-%d", time.Now().UnixNano()), Protocol: ListenerProtocolWebRTC, RequestedMount: mount, CurrentMount: mount, Connected: time.Now(), DisconnectCh: make(chan struct{})}
	if len(details) > 0 {
		l.RemoteAddr = details[0].RemoteAddr
		l.UserAgent = details[0].UserAgent
	}
	limit := 0
	if wm.listenerLimit != nil {
		limit = wm.listenerLimit(mount)
	}
	if err := wm.relay.Listeners.Admit(l, limit); err != nil {
		return nil, err
	}
	return l, nil
}

func (wm *WebRTCManager) HandleOffer(mount string, offer webrtc.SessionDescription, details ...ListenerSnapshot) (*webrtc.SessionDescription, error) {
	stream, ok := wm.relay.GetStream(mount)
	if !ok {
		return nil, fmt.Errorf("stream not found")
	}

	// WebRTC requires Opus (usually in Ogg container for us)
	ct := strings.ToLower(stream.ContentType)
	if !strings.Contains(ct, "ogg") && !strings.Contains(ct, "opus") {
		return nil, fmt.Errorf("WebRTC requires Opus stream (got %s)", stream.ContentType)
	}

	listener, err := wm.admitPlayback(mount, details...)
	if err != nil {
		return nil, err
	}
	playbackHandedOff := false
	defer func() {
		if !playbackHandedOff {
			wm.relay.Listeners.Unregister(listener.ID)
		}
	}()
	peerConnection, err := wm.api.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	})
	if err != nil {
		return nil, err
	}
	// Every early return below used to leak the peer connection — pion
	// keeps four goroutines and RTCP tickers alive until Close — so a
	// rejected or malformed offer cost the process resources for good.
	// Unauthenticated /webrtc/offer made that a free leak per request.
	handedOff := false
	defer func() {
		if !handedOff {
			_ = peerConnection.Close()
		}
	}()

	// Create a track for Opus audio
	audioTrack, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus}, "audio", "tinyice")
	if err != nil {
		return nil, err
	}

	if _, err = peerConnection.AddTrack(audioTrack); err != nil {
		return nil, err
	}

	// Set the remote SessionDescription
	if err = peerConnection.SetRemoteDescription(offer); err != nil {
		return nil, err
	}

	// Create answer
	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		return nil, err
	}

	// Sets the LocalDescription and starts gathering ICE candidates
	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)
	if err = peerConnection.SetLocalDescription(answer); err != nil {
		return nil, err
	}
	<-gatherComplete

	// Start feeding the track. Register OnConnectionStateChange once
	// here (pion replaces handlers, so registering inside the goroutine
	// caused leaks under WHEP). Cancelling pumpCtx unblocks the pump.
	pumpCtx, pumpCancel := context.WithCancel(context.Background())
	peerConnection.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateClosed ||
			state == webrtc.PeerConnectionStateFailed ||
			state == webrtc.PeerConnectionStateDisconnected {
			pumpCancel()
			wm.relay.Listeners.Unregister(listener.ID)
			// Release UDP sockets / DTLS state. Same FD-creep
			// concern as the WHEP path.
			_ = peerConnection.Close()
		}
	})
	go func() {
		defer wm.relay.Listeners.Unregister(listener.ID)
		wm.streamToTrack(pumpCtx, peerConnection, audioTrack, stream)
	}()
	go func() {
		select {
		case <-pumpCtx.Done():
		case <-listener.DisconnectCh:
			pumpCancel()
			_ = peerConnection.Close()
		}
	}()
	playbackHandedOff = true

	handedOff = true
	return peerConnection.LocalDescription(), nil
}

type relayWriter struct {
	token  int64
	relay  *Relay
	stream *Stream
}

func (rw *relayWriter) Write(p []byte) (n int, err error) {
	logger.L.Debugw("relayWriter: Broadcasting", "bytes", len(p), "mount", rw.stream.MountName)
	if !rw.relay.PublishSource(rw.stream, rw.token, p) {
		return 0, ErrSourceOwnershipLost
	}
	return len(p), nil
}

func (wm *WebRTCManager) HandleSourceOffer(mount string, offer webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	peerConnection, err := wm.sourceAPI.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	})
	if err != nil {
		return nil, err
	}
	// Every early return below used to leak the peer connection — pion
	// keeps four goroutines and RTCP tickers alive until Close — so a
	// rejected or malformed offer cost the process resources for good.
	// Unauthenticated /webrtc/offer made that a free leak per request.
	handedOff := false
	defer func() {
		if !handedOff {
			_ = peerConnection.Close()
		}
	}()

	// Per-source-ingest context, cancelled when the PC tears down.
	// The OnTrack ReadRTP loop selects on it so a closed PC unwinds
	// the pump goroutine immediately rather than waiting for pion's
	// internal track teardown.
	srcCtx, srcCancel := context.WithCancel(context.Background())

	// When a source reconnects (same mount, new peer connection), we
	// must wait for the previous source's pump goroutine to actually
	// EXIT before letting the new one start writing — otherwise both
	// run briefly and interleave bytes into the shared Stream.Buffer,
	// producing torn Ogg pages until pion finishes closing the prior
	// track. existingDone is closed by the prior OnTrack on exit.
	wm.mu.Lock()
	existingPC, _ := wm.sources[mount]
	existingDone := wm.sourceDone[mount]
	doneCh := make(chan struct{})
	if wm.sourceDone == nil {
		wm.sourceDone = make(map[string]chan struct{})
	}
	wm.sources[mount] = peerConnection
	wm.sourceDone[mount] = doneCh
	wm.mu.Unlock()
	if existingPC != nil {
		_ = existingPC.Close()
	}
	if existingDone != nil {
		// Bounded wait: pion's track.ReadRTP returns promptly on
		// PC.Close.
		select {
		case <-existingDone:
		case <-time.After(3 * time.Second):
			srcCancel()
			_ = peerConnection.Close()
			wm.mu.Lock()
			if wm.sources[mount] == peerConnection {
				delete(wm.sources, mount)
				delete(wm.sourceDone, mount)
			}
			wm.mu.Unlock()
			return nil, fmt.Errorf("previous source pumps did not stop")
		}
	}

	peerConnection.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		logger.L.Infow("WebRTC Source: Connection state changed", "mount", mount, "state", state.String())
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed || state == webrtc.PeerConnectionStateDisconnected {
			srcCancel()
			wm.mu.Lock()
			currentSource := wm.sources[mount] == peerConnection
			if currentSource {
				delete(wm.sources, mount)
			}
			wm.mu.Unlock()

			if currentSource {
				wm.relay.Diagnostics.Record(DiagnosticUpdate{
					Mount:     mount,
					Status:    DiagnosticStatusStopped,
					Class:     DiagnosticClassSourceDisconnect,
					Reason:    "webrtc source disconnected",
					Actor:     DiagnosticActorWebRTC,
					Timestamp: time.Now(),
				})
			}
			// Release UDP sockets / DTLS state on terminal states.
			// pion's PC keeps internal goroutines alive until Close;
			// without this the FD count creeps up across reconnects.
			_ = peerConnection.Close()
		}
	})

	// pion invokes OnTrack once per remote track, each on its own
	// goroutine that nothing recovers. An offer carrying audio+video (a
	// browser with getUserMedia({audio, video}), or a WHIP tool that adds
	// a video track) therefore ran this closure twice: two pumps wrote
	// interleaved Ogg pages into the same Stream.Buffer, and when the
	// publisher left, the second deferred close(doneCh) panicked with
	// "close of closed channel" and took the whole process down. One
	// track owns the pump; the rest are declined; doneCh closes once.
	var pumpClaimed atomic.Bool
	var videoClaimed atomic.Bool
	go func() {
		<-doneCh
		wm.mu.Lock()
		if wm.sourceDone[mount] == doneCh {
			delete(wm.sourceDone, mount)
		}
		wm.mu.Unlock()
	}()
	var doneOnce sync.Once
	var pumpMu sync.Mutex
	activePumps := 0
	pumpsClosing := false
	finishPump := func() {
		pumpMu.Lock()
		activePumps--
		if pumpsClosing && activePumps == 0 {
			doneOnce.Do(func() { close(doneCh) })
		}
		pumpMu.Unlock()
	}
	go func() {
		<-srcCtx.Done()
		pumpMu.Lock()
		pumpsClosing = true
		if activePumps == 0 {
			doneOnce.Do(func() { close(doneCh) })
		}
		pumpMu.Unlock()
	}()
	peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		defer func() {
			if r := recover(); r != nil {
				logger.L.Errorw("WebRTC Source: track pump panicked", "mount", mount, "panic", fmt.Sprintf("%v", r))
			}
		}()
		pumpMu.Lock()
		if pumpsClosing {
			pumpMu.Unlock()
			return
		}
		activePumps++
		pumpMu.Unlock()
		defer finishPump()
		codec := track.Codec().MimeType
		// A publisher sending camera or screen capture offers audio and
		// video on one peer connection. Audio feeds the mount as Ogg
		// Opus; video feeds the /video sibling as Annex-B H.264, which
		// is the same shape the RTMP ingest produces, so HLS and WHEP
		// playback need no changes. One track of each kind is accepted;
		// extras are declined rather than interleaved.
		if strings.EqualFold(codec, webrtc.MimeTypeH264) {
			if !videoClaimed.CompareAndSwap(false, true) {
				logger.L.Warnw("WebRTC Source: ignoring additional video track; the first one feeds the mount", "track", track.ID(), "mount", mount)
				return
			}
			wm.pumpVideoTrack(srcCtx, mount, track, peerConnection)
			return
		}
		if !strings.EqualFold(codec, webrtc.MimeTypeOpus) {
			logger.L.Warnw("WebRTC Source: ignoring unsupported track", "track", track.ID(), "codec", codec, "mount", mount)
			return
		}
		if !pumpClaimed.CompareAndSwap(false, true) {
			logger.L.Warnw("WebRTC Source: ignoring additional audio track; the first one feeds the mount", "track", track.ID(), "mount", mount)
			return
		}
		logger.L.Infow("WebRTC Source: Received track", "track", track.ID(), "mount", mount)
		// doneCh handshake with a successor source — close on exit so
		// the next HandleSourceOffer can stop waiting for us.

		stream := wm.relay.GetOrCreateStream(mount)
		token, claimed := stream.ClaimSource("webrtc-source")
		if !claimed {
			return
		}
		defer stream.ReleaseSourceIfCurrent(token)
		stream.SetSourceCloserIfCurrent(token, func() { srcCancel(); _ = peerConnection.Close() })
		defer stream.SetSourceCloserIfCurrent(token, nil)
		defer stream.SetCurrentSong("", wm.relay)
		if wm.runtimeRegistry != nil {
			wm.runtimeRegistry.AttachClaim(stream, token, SourceWebRTC, "default")
			defer wm.runtimeRegistry.DetachClaim(stream, token)
		}
		stream.mu.Lock()
		stream.ContentType = "audio/ogg"
		stream.IsOggStream = true
		stream.mu.Unlock()
		// Fresh producer session: wipes Ogg page-tracking state from any
		// previous source on this mount and snaps existing listeners to
		// the live edge.
		stream.BeginSession()

		// Capture headers written by oggwriter.NewWith
		var headerBuf bytes.Buffer
		multi := io.MultiWriter(&headerBuf, &relayWriter{relay: wm.relay, stream: stream, token: token})

		writer, err := oggwriter.NewWith(multi, 48000, 2)
		if err != nil {
			logger.L.Errorf("Failed to create Ogg writer for WebRTC source: %v", err)
			return
		}
		defer writer.Close()

		// Save captured headers immediately
		stream.mu.Lock()
		h := make([]byte, headerBuf.Len())
		copy(h, headerBuf.Bytes())
		stream.OggHead = h
		logger.L.Infow("WebRTC Source: Captured Ogg/Opus headers", "bytes", len(h), "offset", stream.OggHeaderOffset)
		stream.mu.Unlock()

		for {
			select {
			case <-srcCtx.Done():
				return
			default:
			}
			rtpPacket, _, err := track.ReadRTP()
			if err != nil {
				if err != io.EOF {
					logger.L.Errorf("Error reading RTP packet from source: %v", err)
				}
				return
			}
			if err := writer.WriteRTP(rtpPacket); err != nil {
				logger.L.Errorf("Error writing RTP to Ogg muxer: %v", err)
				return
			}
		}
	})

	if err = peerConnection.SetRemoteDescription(offer); err != nil {
		return nil, err
	}

	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		return nil, err
	}

	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)
	if err = peerConnection.SetLocalDescription(answer); err != nil {
		return nil, err
	}
	<-gatherComplete

	handedOff = true
	return peerConnection.LocalDescription(), nil
}

func (wm *WebRTCManager) streamToTrack(ctx context.Context, pc *webrtc.PeerConnection, track *webrtc.TrackLocalStaticSample, stream *Stream) {
	// ctx is shared with the matching streamVideoToTrack goroutine and
	// is cancelled by the single OnConnectionStateChange registered in
	// HandleWHEPOffer. Don't register another one here — pion replaces
	// the prior handler and the loser leaks.

	id := fmt.Sprintf("webrtc-%d", time.Now().UnixNano())
	// For WebRTC we start at the current head (no burst)
	offset, signal := stream.Subscribe(id, 0)
	defer stream.Unsubscribe(id)

	// Seek to next "OggS" magic
	syncBuf := make([]byte, 16384)
	foundSync := false
	var searched int64 = 0
	for !foundSync && searched < 512*1024 {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-signal:
			// Stream.Close / DisconnectListeners close the signal
			// channel. A closed channel is always ready, so without
			// this check the loop spun at 100% of a core forever on a
			// mount that went away before the viewer found its first
			// page. Mirrors StreamReader in io.go.
			if !ok {
				return
			}
			n, next, _ := stream.Buffer.ReadAt(offset, syncBuf)
			if n == 0 {
				continue
			}
			// Use a robust sliding window to find "OggS"
			for i := 0; i <= n-4; i++ {
				if syncBuf[i] == 'O' && syncBuf[i+1] == 'g' && syncBuf[i+2] == 'g' && syncBuf[i+3] == 'S' {
					offset += int64(i)
					foundSync = true
					break
				}
			}
			if !foundSync {
				// Keep the last 3 bytes in case "OggS" is split between reads
				offset = next - 3
				searched += int64(n)
			}
		}
	}

	if !foundSync {
		logger.L.Errorw("WebRTC: Could not find Ogg sync in first 512KB. Is it an Opus stream?", "mount", stream.MountName)
		return
	}

	reader := NewStreamReader(stream.Buffer, offset, signal, ctx, id).WithOggSync(stream)

	// We wrap the reader to prepend the OggHead if available.
	// This ensures NewOpusReader always sees the ID/Tag headers.
	// Use GetOggHead to read under the stream mutex — direct field
	// access races with StoreOggHead's locked write and could yield
	// a torn slice header.
	var finalReader io.Reader = reader
	if oggHead := stream.GetOggHead(); oggHead != nil {
		finalReader = io.MultiReader(bytes.NewReader(oggHead), reader)
	}

	opusReader, err := ogg.NewOpusReader(finalReader)
	if err != nil {
		logger.L.Errorf("Failed to initialize Ogg/Opus reader for WebRTC: %v", err)
		return
	}

	pacer := &SimplePacer{}
	logger.L.Infof("WebRTC: Beginning packet transmission for %s", stream.MountName)
	sentCount := 0

	// Default Opus duration is 20ms. If we detect drift, the pacer will self-correct.
	const defaultDuration = 20 * time.Millisecond

	for {
		packet, err := opusReader.ReadAudioPacket()
		if err != nil {
			if err != io.EOF && !errors.Is(err, context.Canceled) {
				logger.L.Errorf("Error reading Opus packet: %v", err)
			}
			return
		}

		// WebRTC expects 48kHz Opus samples.
		pacer.Pace(defaultDuration)
		if err := track.WriteSample(media.Sample{
			Data:     packet.Data,
			Duration: defaultDuration,
		}); err != nil {
			return
		}
		sentCount++
		if sentCount%100 == 0 {
			logger.L.Debugw("WebRTC: Sent 100 packets", "mount", stream.MountName)
		}
	}
}

// HandleWHEPOffer accepts a raw SDP offer from a browser (WHEP egress)
// and returns a raw SDP answer. Unlike HandleOffer (which only attaches
// an Opus track from the legacy /webrtc/offer path), this picks both
// audio and video tracks when the mount has them. The returned string
// is the SDP answer only — the HTTP layer wraps it with the WHEP
// headers (Location, 201 status).
func (wm *WebRTCManager) HandleWHEPOffer(mount, sdpOffer string, details ...ListenerSnapshot) (string, error) {
	stream, ok := wm.relay.GetStream(mount)
	if !ok {
		return "", fmt.Errorf("stream not found")
	}

	listener, err := wm.admitPlayback(mount, details...)
	if err != nil {
		return "", err
	}
	playbackHandedOff := false
	defer func() {
		if !playbackHandedOff {
			wm.relay.Listeners.Unregister(listener.ID)
		}
	}()
	pc, err := wm.api.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	})
	if err != nil {
		return "", err
	}
	// Every early return below used to leak the peer connection — pion
	// keeps four goroutines and RTCP tickers alive until Close — so a
	// rejected or malformed offer cost the process resources for good.
	// Unauthenticated /webrtc/offer made that a free leak per request.
	handedOff := false
	defer func() {
		if !handedOff {
			_ = pc.Close()
		}
	}()

	// Audio: only attach when the source is Opus. Browsers don't decode
	// MP3 over WebRTC, so mounts without Opus get video-only playback.
	var audioTrack *webrtc.TrackLocalStaticSample
	ct := strings.ToLower(stream.ContentType)
	if strings.Contains(ct, "ogg") || strings.Contains(ct, "opus") {
		audioTrack, err = webrtc.NewTrackLocalStaticSample(
			webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus},
			"audio", "tinyice-audio")
		if err != nil {
			pc.Close()
			return "", err
		}
		if _, err = pc.AddTrack(audioTrack); err != nil {
			pc.Close()
			return "", err
		}
	}

	// Video: attach when a /video sibling stream exists with H.264.
	var videoTrack *webrtc.TrackLocalStaticSample
	var videoStream *Stream
	if vs, ok := wm.relay.GetStream(mount + "/video"); ok {
		videoStream = vs
		videoTrack, err = webrtc.NewTrackLocalStaticSample(
			webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264},
			"video", "tinyice-video")
		if err != nil {
			pc.Close()
			return "", err
		}
		if _, err = pc.AddTrack(videoTrack); err != nil {
			pc.Close()
			return "", err
		}
	}

	if audioTrack == nil && videoTrack == nil {
		pc.Close()
		return "", fmt.Errorf("mount has no Opus audio or H.264 video to egress")
	}

	offer := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sdpOffer}
	if err = pc.SetRemoteDescription(offer); err != nil {
		pc.Close()
		return "", err
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		pc.Close()
		return "", err
	}
	gather := webrtc.GatheringCompletePromise(pc)
	if err = pc.SetLocalDescription(answer); err != nil {
		pc.Close()
		return "", err
	}
	<-gather

	// Register OnConnectionStateChange ONCE per peer connection.
	// pion replaces the previous handler on each call, so when both
	// streamToTrack and streamVideoToTrack registered their own,
	// only the second won and the first goroutine never saw the
	// PC close — leaking one Stream subscription + goroutine per
	// WHEP listener disconnect. Cancel a shared context here that
	// both pump goroutines select on.
	pumpCtx, pumpCancel := context.WithCancel(context.Background())
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateClosed ||
			state == webrtc.PeerConnectionStateFailed ||
			state == webrtc.PeerConnectionStateDisconnected {
			pumpCancel()
			wm.relay.Listeners.Unregister(listener.ID)
			// Explicitly close the PC so pion releases its UDP
			// sockets / DTLS state machine. Without this, the FD
			// count creeps up over the course of many WHEP
			// listener disconnects.
			_ = pc.Close()
		}
	})

	if audioTrack != nil {
		go wm.streamToTrack(pumpCtx, pc, audioTrack, stream)
	}
	if videoTrack != nil {
		go wm.streamVideoToTrack(pumpCtx, pc, videoTrack, videoStream)
	}

	go func() {
		select {
		case <-pumpCtx.Done():
		case <-listener.DisconnectCh:
			pumpCancel()
			_ = pc.Close()
		}
		wm.relay.Listeners.Unregister(listener.ID)
	}()
	playbackHandedOff = true
	handedOff = true
	return pc.LocalDescription().SDP, nil
}

// streamVideoToTrack pulls per-frame Annex-B H.264 NALUs from the stream's
// FrameHub and hands them to the WebRTC sample track. TrackLocalStaticSample
// runs RTP packetization (FU-A fragmentation for NAL units larger than
// MTU) internally; we just need to feed it one full access unit per
// WriteSample call with a realistic Duration.
func (wm *WebRTCManager) streamVideoToTrack(ctx context.Context, pc *webrtc.PeerConnection, track *webrtc.TrackLocalStaticSample, stream *Stream) {
	if stream == nil || stream.Frames == nil {
		return
	}
	// ctx comes from HandleWHEPOffer's shared pump context — see
	// streamToTrack for why we don't register OnConnectionStateChange here.

	frames := stream.Frames.Subscribe(ctx)
	var lastDTS int64 = -1
	// Wait for the first keyframe so the decoder has a valid IDR +
	// SPS / PPS to start from; without this Chrome shows a black
	// frame until the next GOP cycles around.
	gotKeyframe := false

	for {
		select {
		case <-ctx.Done():
			return
		case f, ok := <-frames:
			if !ok {
				return
			}
			if !gotKeyframe {
				if !f.Keyframe {
					continue
				}
				gotKeyframe = true
			}
			dur := 33 * time.Millisecond // 30 fps sane default
			if lastDTS >= 0 {
				// Frame.DTS is in 90 kHz ticks. Accept anything from
				// 5 fps (18 000 ticks) down to 240 fps (375 ticks);
				// outside that window the source has stalled or sent
				// a bogus timestamp and falling back to the default
				// keeps RTP timestamps monotonic.
				delta := f.DTS - lastDTS
				if delta >= 375 && delta <= 18000 {
					dur = time.Duration(delta) * time.Second / 90000
				}
			}
			lastDTS = f.DTS
			if err := track.WriteSample(media.Sample{Data: f.Data, Duration: dur}); err != nil {
				return
			}
		}
	}
}

func (wm *WebRTCManager) DisconnectSource(mount string) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	pc, ok := wm.sources[mount]
	if !ok {
		return fmt.Errorf("no WebRTC source for mount %s", mount)
	}
	delete(wm.sources, mount)
	return pc.Close()
}
