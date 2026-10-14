package relay

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/logger"
	shine "github.com/braheezy/shine-mp3/pkg/mp3"
	"github.com/kazzmir/opus-go/ogg"
	"github.com/kazzmir/opus-go/opus"
)

type TranscoderInstance struct {
	output *Stream
	token  int64
	done   chan struct{}
	Config *config.TranscoderConfig
	cancel context.CancelFunc
	active bool
	mu     sync.Mutex

	// Stats
	FramesProcessed int64
	BytesEncoded    int64
	StartTime       time.Time
}

type TranscoderManager struct {
	runtimeRegistry *RuntimeRegistry
	instances       map[string]*TranscoderInstance // key is OutputMount
	mu              sync.RWMutex
	relay           *Relay

	// hub holds shared decoders keyed by input mount. The first
	// transcoder for an input opens the decoder; subsequent ones
	// attach to the existing PCM fanout. Saves a real CPU chunk
	// when several outputs share an input (auto-mp3 + opus combos).
	hub *DecoderHub
}

func NewTranscoderManager(r *Relay) *TranscoderManager {
	return &TranscoderManager{
		instances: make(map[string]*TranscoderInstance),
		relay:     r,
		hub:       NewDecoderHub(r),
	}
}

func (tm *TranscoderManager) SetRuntimeRegistry(rr *RuntimeRegistry) { tm.runtimeRegistry = rr }

func (tm *TranscoderManager) StartTranscoder(cfg *config.TranscoderConfig) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if inst, ok := tm.instances[cfg.OutputMount]; ok {
		inst.Stop()
	}

	ctx, cancel := context.WithCancel(context.Background())
	inst := &TranscoderInstance{
		Config:    cfg,
		cancel:    cancel,
		active:    true,
		StartTime: time.Now(),
	}
	tm.instances[cfg.OutputMount] = inst

	output := tm.relay.GetOrCreateStream(inst.Config.OutputMount)
	token, claimed := output.ClaimSource("transcode-" + inst.Config.Name)
	if !claimed {
		cancel()
		inst.active = false
		return
	}
	output.SetSourceCloserIfCurrent(token, cancel)
	if tm.runtimeRegistry != nil {
		tm.runtimeRegistry.AttachClaim(output, token, SourceTranscode, "default")
	}
	inst.output = output
	inst.token = token
	inst.done = make(chan struct{})
	go func() {
		defer close(inst.done)
		mirrorDone := make(chan struct{})
		go func() { defer close(mirrorDone); tm.runMetadataMirror(ctx, inst) }()
		tm.runTranscoder(ctx, inst)
		cancel()
		<-mirrorDone
		output.SetCurrentSong("", tm.relay)
		if tm.runtimeRegistry != nil {
			tm.runtimeRegistry.DetachClaim(output, token)
		}
		output.SetSourceCloserIfCurrent(token, nil)
		output.ReleaseSourceIfCurrent(token)
	}()
}

func (tm *TranscoderManager) StopTranscoder(outputMount string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if inst, ok := tm.instances[outputMount]; ok {
		inst.Stop()
		delete(tm.instances, outputMount)
	}
}

func (tm *TranscoderManager) StopAll() {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	for _, inst := range tm.instances {
		inst.Stop()
	}
	tm.instances = make(map[string]*TranscoderInstance)
}

func (inst *TranscoderInstance) Stop() {
	inst.mu.Lock()
	cancel, done := inst.cancel, inst.done
	inst.active = false
	inst.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (tm *TranscoderManager) runTranscoder(ctx context.Context, inst *TranscoderInstance) {
	logger.L.Infow("Starting transcoder",
		"name", inst.Config.Name,
		"input", inst.Config.InputMount,
		"output", inst.Config.OutputMount,
		"format", inst.Config.Format,
		"bitrate", inst.Config.Bitrate,
	)

	backoff := 5 * time.Second
	for {
		select {
		case <-ctx.Done():
			return
		default:
			start := time.Now()
			tm.safePerformTranscode(ctx, inst)
			if time.Since(start) > 30*time.Second {
				backoff = 5 * time.Second
			}
			retryAfter := nextTranscoderRetryBackoff(&backoff)
			logger.L.Infow("Transcoder: retrying", "name", inst.Config.Name, "backoff", retryAfter)
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryAfter):
			}
		}
	}
}

func nextTranscoderRetryBackoff(current *time.Duration) time.Duration {
	const maxBackoff = 5 * time.Minute

	if *current <= 0 {
		*current = 5 * time.Second
	}
	delay := *current
	*current *= 2
	if *current > maxBackoff {
		*current = maxBackoff
	}
	return delay
}

// safePerformTranscode wraps performTranscode with a panic recovery. Some
// third-party decoders (e.g. go-mp3) can panic on malformed or non-MP3 input
// instead of returning an error. Recovering here prevents a bad input stream
// from taking down the whole server and lets the retry loop try again.
func (tm *TranscoderManager) safePerformTranscode(ctx context.Context, inst *TranscoderInstance) {
	defer func() {
		if r := recover(); r != nil {
			logger.L.Errorw("Transcoder: recovered from panic, will retry",
				"name", inst.Config.Name,
				"input", inst.Config.InputMount,
				"output", inst.Config.OutputMount,
				"panic", fmt.Sprintf("%v", r),
				"stack", string(debug.Stack()),
			)
		}
	}()
	tm.performTranscode(ctx, inst)
}

func (tm *TranscoderManager) performTranscode(ctx context.Context, inst *TranscoderInstance) {
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	// 1. Acquire a shared decoder for this input. The first
	//    transcoder per input mount kicks off the underlying
	//    decoder + PCM fanout pump; subsequent transcoders attach
	//    to the existing PCM stream. Same-input encoders therefore
	//    share one decode pass instead of running N redundantly.
	subID := fmt.Sprintf("transcoder-%s", inst.Config.Name)
	pcmReader, decoderRate, releaseDecoder, err := tm.hub.Acquire(runCtx, inst.Config.InputMount, subID)
	if err != nil {
		logger.L.Errorw("Transcoder: failed to acquire shared decoder",
			"name", inst.Config.Name, "input", inst.Config.InputMount, "error", err)
		return
	}
	defer releaseDecoder()

	logger.L.Infow("Transcoder: shared decoder ready",
		"name", inst.Config.Name, "input", inst.Config.InputMount, "rate", decoderRate)

	// 2. Look up the input Stream so we can copy display metadata
	//    (name, visibility) onto the output. Best-effort: if the
	//    source disconnects between Acquire and now, we still proceed
	//    with whatever we can produce; the output stream just gets a
	//    generic name.
	input, _ := tm.relay.GetStream(inst.Config.InputMount)
	var inputName string
	if input != nil {
		input.mu.RLock()
		inputName = input.Name
		input.mu.RUnlock()
	}

	output := inst.output
	// Resolve visibility BEFORE taking output.mu. Holding a stream's
	// mutex while taking relay.mu is an ABBA setup against
	// Relay.Snapshot / RemoveStream (which take relay.mu first, then a
	// stream's mu). Combined with Go's writer-preferring RWMutex this
	// deadlocked production transcoder restarts.
	visible, explicit := inst.Config.ResolveVisibility()
	if !explicit {
		visible = tm.relay.GetStreamVisibility(inst.Config.InputMount)
	}
	output.mu.Lock()
	if inputName != "" {
		output.Name = fmt.Sprintf("%s (%s %dK)", inputName, inst.Config.Format, inst.Config.Bitrate)
	} else {
		output.Name = fmt.Sprintf("%s (%s %dK)", inst.Config.OutputMount, inst.Config.Format, inst.Config.Bitrate)
	}
	output.Bitrate = fmt.Sprintf("%d", inst.Config.Bitrate)
	output.IsTranscoded = true
	output.Visible = visible
	output.mu.Unlock()

	// New producer session: snap listeners to the live edge AND wipe
	// the Ogg page-tracking state. The previous encoder run used a
	// different Ogg serial, so any tracked PageOffsets / OggHead from
	// it now point at bytes that don't belong to this serial — a new
	// subscriber that aligned to one of them would decode garbage.
	// On the very first iteration the buffer is empty so the byte-
	// level state is a no-op; the field clears are cheap.
	output.BeginSession()

	// pcmReader is an io.Reader producing S16LE stereo PCM at
	// decoderRate. Wrap with a tiny shim so anything downstream that
	// expects a PCMDecoder (NewLinearResampler, etc.) gets a
	// SampleRate() method.
	decoder := &readerDecoder{r: pcmReader, sr: decoderRate}

	if inst.Config.Format == "mp3" {
		output.mu.Lock()
		output.ContentType = "audio/mpeg"
		output.mu.Unlock()
		sr := inst.Config.SampleRate
		if sr == 0 {
			sr = decoder.SampleRate()
		}
		// If the operator picked an explicit output rate different from
		// the decoder's native rate, resample before encoding so shine
		// sees the rate it was configured with.
		var pcm io.Reader = decoder
		if sr != decoder.SampleRate() {
			pcm = NewLinearResampler(decoder, decoder.SampleRate(), sr)
		}
		EncodeMP3(runCtx, tm.relay, output, pcm, inst.Config.Bitrate, &inst.BytesEncoded, false, sr, inst.token)
	} else if inst.Config.Format == "opus" {
		output.mu.Lock()
		output.ContentType = "audio/ogg"
		output.mu.Unlock()
		// Opus is always 48 kHz internally. Anything else needs a
		// resampler; without this the encoder plays 44.1 kHz MP3 input
		// ~8.8 % too fast (chipmunk voice).
		var pcm io.Reader = decoder
		if decoder.SampleRate() != 48000 {
			pcm = NewLinearResampler(decoder, decoder.SampleRate(), 48000)
			logger.L.Infow("Transcoder: resampling input for Opus",
				"name", inst.Config.Name,
				"from", decoder.SampleRate(),
				"to", 48000,
			)
		}
		EncodeOpus(runCtx, tm.relay, output, pcm, inst.Config.Bitrate, &inst.BytesEncoded, false, inst.token,
			OpusEncoderSettings{
				Application: inst.Config.OpusApplication,
				VBR:         inst.Config.OpusVBR,
				Complexity:  inst.Config.OpusComplexity,
				FrameSizeMS: inst.Config.OpusFrameSizeMS,
			})
	}
}

// OpusEncoderSettings carries optional Opus tuning. Zero / empty values fall
// back to encoder defaults.
type OpusEncoderSettings struct {
	Application string // "audio" | "voip" | "lowdelay"
	VBR         *bool  // nil = VBR on
	Complexity  int    // 0..10, 0 = encoder default
	FrameSizeMS int    // 2/5/10/20/40/60, 0 = 20
}

func resolveOpusApplication(s string) int {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "voip":
		return opus.ApplicationVoIP
	case "lowdelay", "restricted_lowdelay", "restricted-lowdelay":
		return opus.ApplicationRestrictedLowDelay
	default:
		return opus.ApplicationAudio
	}
}

// shineBitRates mirrors the unexported bitRates table in shine-mp3. Indexed
// as [bitrate_index][mpegVersion], with:
//   - mpegVersion 0 = MPEG-2.5, 1 = reserved, 2 = MPEG-II, 3 = MPEG-I
//   - bitrate_index 0..14 correspond to kbps values (15 is "invalid").
//
// -1 entries are unsupported bitrate/version combinations.
var shineBitRates = [16][4]int64{
	{-1, -1, -1, -1}, {8, -1, 8, 32}, {16, -1, 16, 40}, {24, -1, 24, 48},
	{32, -1, 32, 56}, {40, -1, 40, 64}, {48, -1, 48, 80}, {56, -1, 56, 96},
	{64, -1, 64, 112}, {-1, -1, 80, 128}, {-1, -1, 96, 160}, {-1, -1, 112, 192},
	{-1, -1, 128, 224}, {-1, -1, 144, 256}, {-1, -1, 160, 320}, {-1, -1, -1, -1},
}

// applyShineBitrate reaches into a shine.Encoder and overrides the bitrate
// (and its derived BitrateIndex / WholeSlotsPerFrame / FracSlotsPerFrame /
// SlotLag fields) so the encoded output actually respects the operator's
// bitrate choice. Returns false if the combination isn't supported by the
// MPEG bitrate table for this sample rate.
//
// shine-mp3 has no bitrate parameter on NewEncoder (still hard-coded to
// 128 as of v0.2.0), which is why this pokes the struct. The field names
// are an unstable surface: v0.2.0 renamed Slot_lag to SlotLag and changed
// CheckConfig's signature. The bitrate assertion in the transcode tests is
// what catches the next rename.
func applyShineBitrate(enc *shine.Encoder, sampleRate, bitrate int) bool {
	if _, err := shine.CheckConfig(sampleRate, bitrate); err != nil {
		return false
	}
	version := int(enc.Mpeg.Version)
	if version < 0 || version >= 4 {
		return false
	}
	bitrateIndex := -1
	for i := 0; i < 16; i++ {
		if shineBitRates[i][version] == int64(bitrate) {
			bitrateIndex = i
			break
		}
	}
	if bitrateIndex < 0 {
		return false
	}
	enc.Mpeg.Bitrate = int64(bitrate)
	enc.Mpeg.BitrateIndex = int64(bitrateIndex)
	// Recompute the frame-size constants from shine's own formula.
	avg := (float64(enc.Mpeg.GranulesPerFrame) * 576.0 / float64(enc.Wave.SampleRate)) *
		(float64(enc.Mpeg.Bitrate) * 1000.0 / float64(enc.Mpeg.BitsPerSlot))
	enc.Mpeg.WholeSlotsPerFrame = int64(avg)
	enc.Mpeg.FracSlotsPerFrame = avg - float64(enc.Mpeg.WholeSlotsPerFrame)
	enc.Mpeg.SlotLag = -enc.Mpeg.FracSlotsPerFrame
	if enc.Mpeg.FracSlotsPerFrame == 0 {
		enc.Mpeg.Padding = 0
	}
	return true
}

func resolveOpusFrameSizeMS(ms int) int {
	switch ms {
	case 2, 5, 10, 20, 40, 60:
		return ms
	default:
		return 20
	}
}

func (tm *TranscoderManager) runMetadataMirror(ctx context.Context, inst *TranscoderInstance) {
	events := tm.relay.SubscribeMetadata()
	defer tm.relay.UnsubscribeMetadata(events)
	reconcile := func() {
		input, ok := tm.relay.GetStream(inst.Config.InputMount)
		if !ok {
			inst.output.SetCurrentSong("", tm.relay)
			return
		}
		mc := input.PlaybackSnapshot()
		if mc.Revision == 0 && input.GetCurrentSong() != "" {
			mc.Title = input.GetCurrentSong()
		}
		inst.output.MirrorPlayback(mc, tm.relay)
		input.mu.RLock()
		genre, url, desc, pub, vis := input.Genre, input.URL, input.Description, input.Public, input.Visible
		input.mu.RUnlock()
		if explicitVis, explicit := inst.Config.ResolveVisibility(); explicit {
			vis = explicitVis
		}
		inst.output.mu.Lock()
		inst.output.Genre = genre
		inst.output.URL = url
		inst.output.Description = desc
		inst.output.Public = pub
		inst.output.Visible = vis
		inst.output.mu.Unlock()
	}
	reconcile()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case mc := <-events:
			if mc.Mount == inst.Config.InputMount {
				reconcile()
			}
		case <-ticker.C:
			reconcile()
		}
	}
}

func EncodeMP3(ctx context.Context, relay *Relay, output *Stream, decoder io.Reader, bitrate int, stats *int64, pace bool, sampleRate int, token int64) {
	if !relay.HasSourceClaim(output, token) {
		return
	}
	if sampleRate <= 0 {
		sampleRate = 44100 // Fallback
	}
	// Shine MP3 initialization. NewEncoder hard-codes the bitrate at 128,
	// so we reach into the Mpeg field to pick up the caller's bitrate. If
	// the combination isn't valid for the MPEG bitrate table we fall back
	// to the 128 kbps default rather than producing a malformed stream.
	encoder := shine.NewEncoder(sampleRate, 2)
	if bitrate > 0 && bitrate != 128 {
		if !applyShineBitrate(encoder, sampleRate, bitrate) {
			logger.L.Warnw("EncodeMP3: requested bitrate not supported for sample rate, falling back to 128 kbps",
				"sample_rate", sampleRate, "requested_bitrate", bitrate)
		}
	}

	// Output buffer - shine Write uses int16 samples
	pcmBuf := make([]byte, 4608) // 1152 samples * 2 bytes * 2 channels
	samples := make([]int16, 2304)

	startTime := time.Now()
	totalSamples := int64(0)

	// Reuse a single streamWriter across iterations — recreating it on
	// every frame churned 38 allocations/sec per transcoder for no
	// benefit; the struct holds no per-call state.
	writer := &streamWriter{token: token, stream: output, relay: relay, stats: stats}

	for {
		select {
		case <-ctx.Done():
			return
		default:
			n, err := io.ReadFull(decoder, pcmBuf)
			if err != nil {
				return
			}

			// Account for this frame before anything can skip ahead.
			// Pacing is driven by how much audio we have CONSUMED, not
			// by how much we encoded, so the counter has to advance on
			// idle frames too — otherwise `expected` falls permanently
			// behind `elapsed` and pacing never re-engages for the rest
			// of the track once a listener arrives.
			if pace {
				totalSamples += int64(n / 4) // 2 channels, 2 bytes per sample
			}

			// Idle gate — when the output mount has no listeners, skip
			// the encode + broadcast. Decoder still drains so the
			// upstream input buffer doesn't overrun us; encoder side
			// CPU is the dominant cost so this is the lever. The
			// encoder's bit-reservoir state may produce one suboptimal
			// frame on the next listener join, which is inaudible.
			//
			// This skips the encode ONLY. It must never skip the pacing
			// sleep below: on a file-backed source (AutoDJ) the sleep is
			// the only thing tying playback to real time, and a bare
			// `continue` here made an idle mount decode its whole
			// playlist as fast as the CPU allowed. Measured on r4dio
			// 2026-09-30: two idle AutoDJ mounts burned 207% CPU, ~90%
			// of it inside go-mp3, with only 27 goroutines alive.
			if output.ListenersCount() == 0 {
				// Producer is alive and on schedule, just not emitting.
				// Keep the mount out of the HealthMonitor's dead-stream
				// sweep; see Stream.MarkProducerAlive.
				output.MarkProducerAlive()
			} else {
				// Convert PCM bytes to int16 for Shine
				for i := 0; i < n/2; i++ {
					samples[i] = int16(pcmBuf[i*2]) | int16(pcmBuf[i*2+1])<<8
				}

				// Feed the frame to the encoder directly rather than through
				// shine's Encoder.Write: as of v0.2.0 that slices each chunk
				// as data[i:i+samplesPerPass] while stepping by
				// samplesPerPass*channels, so for stereo it encodes only the
				// first half of every frame (zero-padded) and drops the rest,
				// which is the distortion reported in #63. pcmBuf is exactly
				// one frame (1152 stereo samples), which is what
				// EncodeBufferInterleaved expects.
				frame, written := encoder.EncodeBufferInterleaved(samples[:n/2])
				if written > 0 {
					if _, err := writer.Write(frame[:written]); err != nil {
						return
					}
				}
			}

			if pace {
				elapsed := activeElapsed(startTime, decoder)
				expected := time.Duration(totalSamples) * time.Second / time.Duration(sampleRate)
				if expected > elapsed {
					time.Sleep(expected - elapsed)
				}
			}
		}
	}
}

func EncodeOpus(ctx context.Context, relay *Relay, output *Stream, decoder io.Reader, bitrate int, stats *int64, pace bool, token int64, settings ...OpusEncoderSettings) {
	if !relay.HasSourceClaim(output, token) {
		return
	}
	// 48kHz is the canonical Opus sample rate.
	const sampleRate = 48000
	const channels = 2
	var cfg OpusEncoderSettings
	if len(settings) > 0 {
		cfg = settings[0]
	}
	frameMS := resolveOpusFrameSizeMS(cfg.FrameSizeMS)
	frameSize := sampleRate * frameMS / 1000

	enc, err := opus.NewEncoder(sampleRate, channels, resolveOpusApplication(cfg.Application))
	if err != nil {
		logger.L.Errorf("Failed to create Opus encoder: %v", err)
		return
	}
	defer enc.Close()

	if bitrate > 0 {
		enc.SetBitrate(bitrate * 1000)
	}
	if cfg.Complexity > 0 && cfg.Complexity <= 10 {
		_ = enc.SetComplexity(cfg.Complexity)
	}
	if cfg.VBR != nil {
		_ = enc.SetVBR(*cfg.VBR)
	}

	// Ogg encapsulation
	writer := &streamWriter{token: token, stream: output, relay: relay, stats: stats, capture: true}
	serial := uint32(time.Now().UnixNano())
	pw := ogg.NewPacketWriter(writer, serial)

	// 1. ID Header
	head := ogg.OpusHead{
		Version:         1,
		Channels:        uint8(channels),
		InputSampleRate: uint32(sampleRate),
	}
	headPacket, _ := ogg.BuildOpusHeadPacket(head)
	pw.WritePacket(headPacket, 0, true, false)
	pw.Flush()

	// 2. Tags Header
	tags := ogg.OpusTags{Vendor: "tinyice-opus"}
	tagsPacket, _ := ogg.BuildOpusTagsPacket(tags)
	pw.WritePacket(tagsPacket, 0, false, false)
	pw.Flush()

	// Store for mid-stream listeners
	// Store captured headers + the buffer offset at which the first audio
	// byte will be written, under the stream mutex so listeners that
	// subscribe concurrently see a consistent pair.
	output.StoreOggHead(writer.headerBuf.Bytes(), output.Buffer.Head)
	writer.capture = false // Stop capturing headers

	pcmBuf := make([]byte, frameSize*channels*2)
	pcmSamples := make([]int16, frameSize*channels)
	opusPacket := make([]byte, 4000) // Max opus packet size

	var granulePos uint64
	var sentCount int64 = 0
	startTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		default:
			_, rerr := io.ReadFull(decoder, pcmBuf)
			if rerr != nil {
				return
			}

			// Frame accounting first — see EncodeMP3 for why this must
			// not sit behind the idle gate.
			sentCount++

			// Idle gate — see EncodeMP3 for rationale, including why it
			// guards the encode only and never the pacing sleep.
			if output.ListenersCount() == 0 {
				output.MarkProducerAlive()
			} else {
				for i := 0; i < len(pcmSamples); i++ {
					pcmSamples[i] = int16(pcmBuf[i*2]) | int16(pcmBuf[i*2+1])<<8
				}

				en, eerr := enc.Encode(pcmSamples, frameSize, opusPacket)
				if eerr != nil {
					logger.L.Errorf("Opus encode error: %v", eerr)
					return
				}

				granulePos += uint64(frameSize)
				if err := pw.WritePacket(opusPacket[:en], granulePos, false, false); err != nil {
					return
				}
				pw.Flush()
			}

			if pace {
				elapsed := activeElapsed(startTime, decoder)
				expected := time.Duration(sentCount*int64(frameMS)) * time.Millisecond
				if expected > elapsed {
					time.Sleep(expected - elapsed)
				}
			}
		}
	}
}

// pauseAware is implemented by readers that can block for a while by
// design (the AutoDJ's pauseGate). The encoders pace output against
// wall-clock time since the track started; without subtracting the time
// spent paused they would think they were behind schedule on resume and
// flush the rest of the file into the ring buffer at full speed.
type pauseAware interface {
	PausedTotal() time.Duration
}

// activeElapsed is wall-clock time since start, minus any time the reader
// reports having spent paused.
func activeElapsed(start time.Time, r io.Reader) time.Duration {
	elapsed := time.Since(start)
	if pa, ok := r.(pauseAware); ok {
		if paused := pa.PausedTotal(); paused > 0 {
			elapsed -= paused
		}
	}
	if elapsed < 0 {
		return 0
	}
	return elapsed
}

type streamWriter struct {
	token     int64
	stream    *Stream
	relay     *Relay
	stats     *int64
	headerBuf bytes.Buffer
	capture   bool
}

func (w *streamWriter) Write(p []byte) (n int, err error) {
	if !w.relay.PublishSource(w.stream, w.token, p) {
		return 0, ErrSourceOwnershipLost
	}
	if w.capture {
		w.headerBuf.Write(p)
	}
	if w.stats != nil {
		atomic.AddInt64(w.stats, int64(len(p)))
	}
	return len(p), nil
}

func (tm *TranscoderManager) GetInstance(outputMount string) *TranscoderInstance {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.instances[outputMount]
}

// snapshotInstances returns a flat slice of all live transcoder
// instances for read-only iteration. Holds the manager mutex only
// while copying — callers iterating the result must NOT mutate the
// instances themselves; they're shared.
func (tm *TranscoderManager) snapshotInstances() []*TranscoderInstance {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	out := make([]*TranscoderInstance, 0, len(tm.instances))
	for _, inst := range tm.instances {
		out = append(out, inst)
	}
	return out
}

type TranscoderStats struct {
	Name            string `json:"name"`
	Input           string `json:"input"`
	Output          string `json:"output"`
	Format          string `json:"format"`
	Bitrate         int    `json:"bitrate"`
	Active          bool   `json:"active"`
	FramesProcessed int64  `json:"frames"`
	BytesEncoded    int64  `json:"bytes"`
	Uptime          string `json:"uptime"`
}

// mirrorTranscodeMetadata keeps the transcoded output stream's display
// metadata (current song, genre, URL, description, public/visible flags)
// in sync with the input stream while the transcode runs. Without this,
// listeners on /<mount>-128 never see StreamTitle updates from the
// upstream encoder, and the public Icecast directory listing shows
// generic info on the transcoded sibling.
// EnsureAutoMP3Transcoders is called when a source connects on `inputMount`.
// For each bitrate in the list it spawns a temporary mp3 transcoder named
// `<inputMount>-mp3-<bitrate>` if no transcoder for that output is already
// running. Auto-spawned transcoders are not persisted to config; they go
// away with the source and reappear on the next connect. Manual entries
// in cfg.Transcoders take precedence (we don't clobber them).
func (tm *TranscoderManager) EnsureAutoMP3Transcoders(inputMount string, bitrates []int, manualConfigs []*config.TranscoderConfig) {
	if len(bitrates) == 0 {
		return
	}
	for _, br := range bitrates {
		if br < 32 || br > 320 {
			continue
		}
		outputMount := fmt.Sprintf("%s-mp3-%d", inputMount, br)
		if tm.GetInstance(outputMount) != nil {
			continue
		}
		// Skip when any existing transcoder — manual or already-running
		// auto — produces the same input+format+bitrate combo, even
		// under a different output-mount name. Without this check a
		// manual entry like /dnb -> /dnb-128 (mp3 128) and an auto
		// /dnb -> /dnb-mp3-128 (mp3 128) both run, doubling the
		// encode work for zero listener benefit.
		skip := false
		for _, mc := range manualConfigs {
			if mc == nil {
				continue
			}
			if mc.OutputMount == outputMount {
				skip = true
				break
			}
			if mc.InputMount == inputMount &&
				strings.EqualFold(mc.Format, "mp3") &&
				mc.Bitrate == br {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		// Also dedupe against running auto/manual transcoder instances —
		// covers the case where a manual transcoder was added at runtime
		// via the admin API after we already spawned the auto twin.
		dup := false
		for _, other := range tm.snapshotInstances() {
			if other == nil || other.Config == nil {
				continue
			}
			if other.Config.InputMount == inputMount &&
				strings.EqualFold(other.Config.Format, "mp3") &&
				other.Config.Bitrate == br {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		cfg := &config.TranscoderConfig{
			Name:        fmt.Sprintf("auto-%s-mp3-%d", strings.TrimPrefix(inputMount, "/"), br),
			InputMount:  inputMount,
			OutputMount: outputMount,
			Format:      "mp3",
			Bitrate:     br,
			Enabled:     true,
		}
		tm.StartTranscoder(cfg)
		logger.L.Infow("auto-spawned MP3 transcoder",
			"input", inputMount, "output", outputMount, "bitrate", br)
	}
}
