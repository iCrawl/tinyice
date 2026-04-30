# AutoDJ Gap Filler Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep AutoDJ mounts continuously decodable across track transitions by running one persistent output session per mount and feeding it either normalized track PCM or generated silence PCM.

**Architecture:** Refactor the current per-track `EncodeMP3` / `EncodeOpus` calls into reusable encoder sessions that accept fixed-size PCM frames over time. Add an AutoDJ-specific output session that owns one long-lived encoder loop and swaps its PCM source between decoded track data and silence during handoff. Keep the relay `Stream` identity, listener API, metadata API, queue semantics, and transcoder topology unchanged.

**Tech Stack:** Go, existing `relay` package, `go-mp3`, `shine-mp3`, `opus-go`, existing relay tests

---

## File Structure

- Create: `relay/autodj_output_session.go`
  - Own the persistent AutoDJ output session, PCM source abstraction, silence source, session lifecycle, and source swapping.
- Create: `relay/autodj_output_session_test.go`
  - Unit and integration-style tests for silence handoff, metadata timing, and continuity behavior.
- Modify: `relay/transcode.go`
  - Extract reusable encoder session helpers from `EncodeMP3` and `EncodeOpus` so a long-lived writer can feed frames incrementally.
- Modify: `relay/transcode_test.go`
  - Add tests for encoder-session continuity and encoded output after multiple PCM frame writes.
- Modify: `relay/streamer.go`
  - Replace per-file encode calls with a mount-level output session, keep selection logic, and wire metadata updates to real-track activation only.

### Task 1: Extract reusable encoder sessions from the existing MP3 and Opus encoders

**Files:**
- Modify: `relay/transcode.go`
- Modify: `relay/transcode_test.go`

- [ ] **Step 1: Write the failing encoder-session tests**

```go
func TestMP3EncoderSessionWritesFramesIncrementally(t *testing.T) {
	r := NewRelay(false, nil)
	stream := r.GetOrCreateStream("/mp3-session")
	session, err := NewMP3EncoderSession(stream, r, 64, 44100, nil)
	if err != nil {
		t.Fatalf("NewMP3EncoderSession: %v", err)
	}
	defer session.Close()

	frame := make([]int16, 1152*2)
	if err := session.WriteFrame(frame); err != nil {
		t.Fatalf("WriteFrame #1: %v", err)
	}
	headAfterFirst := stream.Buffer.Head
	if headAfterFirst == 0 {
		t.Fatal("expected encoded bytes after first frame")
	}

	if err := session.WriteFrame(frame); err != nil {
		t.Fatalf("WriteFrame #2: %v", err)
	}
	if stream.Buffer.Head <= headAfterFirst {
		t.Fatal("expected second frame to append encoded bytes")
	}
}

func TestOpusEncoderSessionWritesHeadersOnlyOnce(t *testing.T) {
	r := NewRelay(false, nil)
	stream := r.GetOrCreateStream("/opus-session")
	session, err := NewOpusEncoderSession(stream, r, 96, 48000, 2, nil)
	if err != nil {
		t.Fatalf("NewOpusEncoderSession: %v", err)
	}
	defer session.Close()

	frame := make([]int16, (48000/50)*2) // 20ms stereo frame
	if err := session.WriteFrame(frame); err != nil {
		t.Fatalf("WriteFrame #1: %v", err)
	}
	oggHead := append([]byte(nil), stream.OggHead...)
	headAfterFirst := stream.Buffer.Head

	if err := session.WriteFrame(frame); err != nil {
		t.Fatalf("WriteFrame #2: %v", err)
	}
	if stream.Buffer.Head <= headAfterFirst {
		t.Fatal("expected second frame to append opus packets")
	}
	if len(stream.OggHead) != len(oggHead) {
		t.Fatal("expected Ogg head to remain stable across writes")
	}
}
```

- [ ] **Step 2: Run the new tests to confirm the API does not exist yet**

Run: `go test ./relay -run 'Test(MP3|Opus)EncoderSession' -count=1`

Expected: FAIL with undefined `NewMP3EncoderSession`, `NewOpusEncoderSession`, or `WriteFrame`.

- [ ] **Step 3: Add the encoder-session types and keep the old helpers working**

```go
type PCMFrameWriter interface {
	WriteFrame(frame []int16) error
	Close() error
}

type MP3EncoderSession struct {
	stream     *Stream
	relay      *Relay
	stats      *int64
	encoder    *shine.Encoder
	sampleRate int
}

func NewMP3EncoderSession(stream *Stream, relay *Relay, bitrate, sampleRate int, stats *int64) (*MP3EncoderSession, error) {
	if sampleRate <= 0 {
		sampleRate = 44100
	}
	encoder := shine.NewEncoder(sampleRate, 2)
	configureMP3EncoderBitrate(encoder, bitrate)
	return &MP3EncoderSession{stream: stream, relay: relay, stats: stats, encoder: encoder, sampleRate: sampleRate}, nil
}

func (s *MP3EncoderSession) WriteFrame(frame []int16) error {
	writer := &streamWriter{stream: s.stream, relay: s.relay, stats: s.stats}
	return s.encoder.Write(writer, frame)
}

func (s *MP3EncoderSession) Close() error { return nil }
```

```go
type OpusEncoderSession struct {
	stream      *Stream
	relay       *Relay
	stats       *int64
	encoder     *opus.Encoder
	packetWriter *ogg.PacketWriter
	frameSize   int
	channels    int
	granulePos  uint64
}

func NewOpusEncoderSession(stream *Stream, relay *Relay, bitrate, sampleRate, channels int, stats *int64) (*OpusEncoderSession, error) {
	writer := &streamWriter{stream: stream, relay: relay, stats: stats, capture: true}
	pw := ogg.NewPacketWriter(writer, uint32(time.Now().UnixNano()))
	enc, err := opus.NewEncoder(48000, channels, opus.ApplicationAudio)
	if err != nil {
		return nil, err
	}
	if bitrate > 0 {
		enc.SetBitrate(bitrate * 1000)
	}

	head := ogg.OpusHead{Version: 1, Channels: uint8(channels), InputSampleRate: uint32(sampleRate)}
	headPacket, _ := ogg.BuildOpusHeadPacket(head)
	if err := pw.WritePacket(headPacket, 0, true, false); err != nil {
		return nil, err
	}
	pw.Flush()

	tags := ogg.OpusTags{Vendor: "tinyice-opus"}
	tagsPacket, _ := ogg.BuildOpusTagsPacket(tags)
	if err := pw.WritePacket(tagsPacket, 0, false, false); err != nil {
		return nil, err
	}
	pw.Flush()

	stream.OggHead = append([]byte(nil), writer.headerBuf.Bytes()...)
	stream.OggHeaderOffset = stream.Buffer.Head
	writer.capture = false

	return &OpusEncoderSession{
		stream:       stream,
		relay:        relay,
		stats:        stats,
		encoder:      enc,
		packetWriter: pw,
		frameSize:    960,
		channels:     channels,
	}, nil
}

func (s *OpusEncoderSession) WriteFrame(frame []int16) error {
	packet := make([]byte, 4000)
	n, err := s.encoder.Encode(frame, s.frameSize, packet)
	if err != nil {
		return err
	}
	s.granulePos += uint64(s.frameSize)
	if err := s.packetWriter.WritePacket(packet[:n], s.granulePos, false, false); err != nil {
		return err
	}
	s.packetWriter.Flush()
	return nil
}

func (s *OpusEncoderSession) Close() error {
	return nil
}
```

```go
func EncodeMP3(ctx context.Context, relay *Relay, output *Stream, decoder io.Reader, bitrate int, stats *int64, pace bool, sampleRate int) {
	session, err := NewMP3EncoderSession(output, relay, bitrate, sampleRate, stats)
	if err != nil {
		return
	}
	defer session.Close()
	// Existing loop now reads PCM and calls session.WriteFrame(...)
}
```

- [ ] **Step 4: Run the focused encoder-session tests**

Run: `go test ./relay -run 'Test(MP3|Opus)EncoderSession' -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add relay/transcode.go relay/transcode_test.go
git commit -m "refactor: extract reusable audio encoder sessions"
```

### Task 2: Add the persistent AutoDJ output session and silence PCM source

**Files:**
- Create: `relay/autodj_output_session.go`
- Create: `relay/autodj_output_session_test.go`

- [ ] **Step 1: Write the failing output-session tests**

```go
func TestAutoDJOutputSessionEmitsSilenceUntilTrackIsReady(t *testing.T) {
	r := NewRelay(false, nil)
	streamer := &Streamer{
		Name:        "Gap Filler",
		OutputMount: "/gap",
		Format:      "mp3",
		Bitrate:     64,
		relay:       r,
	}

	session, err := NewAutoDJOutputSession(streamer)
	if err != nil {
		t.Fatalf("NewAutoDJOutputSession: %v", err)
	}
	defer session.Stop()

	if err := session.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	stream, ok := r.GetStream("/gap")
	if !ok || stream.Buffer.Head == 0 {
		t.Fatal("expected encoded bytes while silence source is active")
	}
}

func TestAutoDJOutputSessionDoesNotPublishMetadataForSilence(t *testing.T) {
	r := NewRelay(false, nil)
	streamer := &Streamer{Name: "Gap Filler", OutputMount: "/gap", Format: "mp3", Bitrate: 64, relay: r}
	session, err := NewAutoDJOutputSession(streamer)
	if err != nil {
		t.Fatalf("NewAutoDJOutputSession: %v", err)
	}
	defer session.Stop()

	metaCh := r.SubscribeMetadata()
	defer r.UnsubscribeMetadata(metaCh)

	if err := session.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case evt := <-metaCh:
		t.Fatalf("unexpected silence metadata event: %+v", evt)
	case <-time.After(200 * time.Millisecond):
	}
}
```

- [ ] **Step 2: Run the new session tests to verify they fail**

Run: `go test ./relay -run 'TestAutoDJOutputSession' -count=1`

Expected: FAIL with undefined `NewAutoDJOutputSession`, `Start`, or `Stop`.

- [ ] **Step 3: Implement the persistent session with a fixed PCM bus**

```go
type pcmFrameSource interface {
	ReadFrame(dst []int16) (int, error)
}

type silencePCMSource struct{}

func (s silencePCMSource) ReadFrame(dst []int16) (int, error) {
	for i := range dst {
		dst[i] = 0
	}
	return len(dst), nil
}

type AutoDJOutputSession struct {
	streamer    *Streamer
	stream      *Stream
	frameWriter PCMFrameWriter
	frameSize   int
	channels    int
	sampleRate  int

	mu     sync.RWMutex
	source pcmFrameSource
	cancel context.CancelFunc
}

func NewAutoDJOutputSession(streamer *Streamer) (*AutoDJOutputSession, error) {
	stream := streamer.relay.GetOrCreateStream(streamer.OutputMount)
	sampleRate := 44100
	frameSize := 1152
	stream.ContentType = "audio/mpeg"

	var writer PCMFrameWriter
	var err error
	if streamer.Format == "opus" {
		sampleRate = 48000
		frameSize = 960
		stream.ContentType = "audio/ogg"
		writer, err = NewOpusEncoderSession(stream, streamer.relay, streamer.Bitrate, sampleRate, 2, &streamer.BytesStreamed)
	} else {
		writer, err = NewMP3EncoderSession(stream, streamer.relay, streamer.Bitrate, sampleRate, &streamer.BytesStreamed)
	}
	if err != nil {
		return nil, err
	}

	return &AutoDJOutputSession{
		streamer:    streamer,
		stream:      stream,
		frameWriter: writer,
		frameSize:   frameSize,
		channels:    2,
		sampleRate:  sampleRate,
		source:      silencePCMSource{},
	}, nil
}

func (s *AutoDJOutputSession) Start(ctx context.Context) error {
	if s.cancel != nil {
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	go func() {
		frame := make([]int16, s.frameSize*s.channels)
		frameDuration := time.Second * time.Duration(s.frameSize) / time.Duration(s.sampleRate)
		nextTick := time.Now()

		for {
			select {
			case <-runCtx.Done():
				return
			default:
			}

			s.mu.RLock()
			source := s.source
			s.mu.RUnlock()
			if source == nil {
				source = silencePCMSource{}
			}

			if _, err := source.ReadFrame(frame); err != nil {
				s.SetSource(silencePCMSource{})
				continue
			}
			if err := s.frameWriter.WriteFrame(frame); err != nil {
				return
			}

			nextTick = nextTick.Add(frameDuration)
			if sleepFor := time.Until(nextTick); sleepFor > 0 {
				time.Sleep(sleepFor)
			}
		}
	}()

	return nil
}

func (s *AutoDJOutputSession) SetSource(source pcmFrameSource) {
	s.mu.Lock()
	s.source = source
	s.mu.Unlock()
}

func (s *AutoDJOutputSession) Stop() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	_ = s.frameWriter.Close()
}
```

- [ ] **Step 4: Run the focused session tests**

Run: `go test ./relay -run 'TestAutoDJOutputSession' -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add relay/autodj_output_session.go relay/autodj_output_session_test.go
git commit -m "feat: add persistent autodj output session"
```

### Task 3: Refactor Streamer to feed the session instead of encoding per file

**Files:**
- Modify: `relay/streamer.go`
- Test: `relay/autodj_output_session_test.go`

- [ ] **Step 1: Write the failing streamer handoff tests**

```go
func TestStreamerKeepsWritingDuringTrackHandoff(t *testing.T) {
	r := NewRelay(false, nil)
	sm := NewStreamerManager(r, nil)
	streamer := &Streamer{
		Name:        "Gap Filler",
		OutputMount: "/gap",
		Format:      "mp3",
		Bitrate:     64,
		State:       StatePlaying,
		relay:       r,
	}

	session, err := NewAutoDJOutputSession(streamer)
	if err != nil {
		t.Fatalf("NewAutoDJOutputSession: %v", err)
	}
	defer session.Stop()
	if err := session.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	stream := r.GetOrCreateStream("/gap")
	before := stream.Buffer.Head
	time.Sleep(150 * time.Millisecond)
	after := stream.Buffer.Head
	if after <= before {
		t.Fatal("expected stream buffer to keep growing while waiting for next track")
	}

	_ = sm
}

func TestStreamerPublishesMetadataOnlyOnRealTrackActivation(t *testing.T) {
	r := NewRelay(false, nil)
	streamer := &Streamer{Name: "Gap Filler", OutputMount: "/gap", Format: "mp3", Bitrate: 64, InjectMetadata: true, relay: r}
	stream := r.GetOrCreateStream("/gap")

	stream.SetCurrentSong("Previous Track", r)
	metaCh := r.SubscribeMetadata()
	defer r.UnsubscribeMetadata(metaCh)

	session, err := NewAutoDJOutputSession(streamer)
	if err != nil {
		t.Fatalf("NewAutoDJOutputSession: %v", err)
	}
	defer session.Stop()

	// Silence handoff must not publish anything new here.
	select {
	case evt := <-metaCh:
		_ = evt
	default:
	}
}
```

- [ ] **Step 2: Run the targeted handoff tests and confirm they fail against the old streamer flow**

Run: `go test ./relay -run 'TestStreamer(KeepsWritingDuringTrackHandoff|PublishesMetadataOnlyOnRealTrackActivation)' -count=1`

Expected: FAIL because the streamer still owns per-file encode runs and does not drive the new session lifecycle.

- [ ] **Step 3: Replace per-file encode calls with session-managed PCM sources**

```go
type trackPCMSource struct {
	reader *pcmFrameReader
}

func newTrackPCMSource(file io.ReadSeeker, outputFormat string) (*trackPCMSource, int, error) {
	decoder, err := mp3.NewDecoder(file)
	if err != nil {
		return nil, 0, err
	}
	srcRate := decoder.SampleRate()
	dstRate := 44100
	samplesPerFrame := 1152
	if outputFormat == "opus" {
		dstRate = 48000
		samplesPerFrame = 960
	}
	return &trackPCMSource{
		reader: newPCMFrameReader(decoder, srcRate, dstRate, 2, samplesPerFrame),
	}, srcRate, nil
}

func (sm *StreamerManager) runStreamerLoop(ctx context.Context, s *Streamer) {
	if s.outputSession == nil {
		session, err := NewAutoDJOutputSession(s)
		if err != nil {
			logger.L.Errorf("Streamer %s: failed to create output session: %v", s.Name, err)
			time.Sleep(time.Second)
			return
		}
		s.outputSession = session
		if err := s.outputSession.Start(ctx); err != nil {
			logger.L.Errorf("Streamer %s: failed to start output session: %v", s.Name, err)
			time.Sleep(time.Second)
			return
		}
	}

	filePath, filePos, fileID, ok := s.nextTrackCandidate()
	if !ok {
		s.outputSession.Stop()
		s.outputSession = nil
		s.mu.Lock()
		s.State = StateStopped
		s.mu.Unlock()
		continue
	}

	s.outputSession.SetSource(silencePCMSource{})
	if err := sm.activateTrackSource(s, filePath, filePos, fileID); err != nil {
		logger.L.Errorf("Streamer %s: failed to activate %s: %v", s.Name, filePath, err)
		time.Sleep(time.Second)
		continue
	}
}
```

```go
func (sm *StreamerManager) activateTrackSource(s *Streamer, path string, pos, id int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	trackSource, srcRate, err := newTrackPCMSource(f, s.Format)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.CurrentFilePath = path
	s.CurrentPlayingPos = pos
	s.CurrentPlayingID = id
	s.CurrentSampleRate = srcRate
	s.CurrentChannels = 2
	s.CurrentFileTime = time.Now()
	s.mu.Unlock()

	if s.InjectMetadata {
		sm.relay.UpdateMetadata(s.OutputMount, s.CurrentFile)
	}

	s.outputSession.SetSource(trackSource)
	return nil
}
```

- [ ] **Step 4: Run the focused streamer tests**

Run: `go test ./relay -run 'TestAutoDJOutputSession|TestStreamer' -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add relay/streamer.go relay/autodj_output_session_test.go
git commit -m "refactor: keep autodj output continuous across track transitions"
```

### Task 4: Cover transcoder continuity and full relay regression behavior

**Files:**
- Modify: `relay/autodj_output_session_test.go`
- Modify: `relay/transcode_test.go`

- [ ] **Step 1: Write the failing regression tests for downstream continuity**

```go
func TestAutoDJTransitionContinuityFeedsTranscoderOutput(t *testing.T) {
	r := NewRelay(false, nil)
	source := r.GetOrCreateStream("/source")
	source.ContentType = "audio/mpeg"
	session, err := NewMP3EncoderSession(source, r, 64, 44100, nil)
	if err != nil {
		t.Fatalf("NewMP3EncoderSession: %v", err)
	}
	defer session.Close()

	frame := make([]int16, 1152*2)
	if err := session.WriteFrame(frame); err != nil {
		t.Fatalf("WriteFrame #1: %v", err)
	}
	if err := session.WriteFrame(frame); err != nil {
		t.Fatalf("WriteFrame #2: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tm := NewTranscoderManager(r)
	inst := &TranscoderInstance{Config: &config.TranscoderConfig{Name: "fallback", InputMount: "/source", OutputMount: "/fallback", Format: "mp3", Bitrate: 64}}
	go tm.performTranscode(ctx, inst)

	time.Sleep(250 * time.Millisecond)
	out, ok := r.GetStream("/fallback")
	if !ok || out.Buffer.Head == 0 {
		t.Fatal("expected transcoder output to accumulate bytes from a continuous source")
	}
}

func TestListenerConnectDuringSilenceWindowStillGetsDecodableBytes(t *testing.T) {
	r := NewRelay(false, nil)
	stream := r.GetOrCreateStream("/gap")
	stream.ContentType = "audio/mpeg"

	session, err := NewMP3EncoderSession(stream, r, 64, 44100, nil)
	if err != nil {
		t.Fatalf("NewMP3EncoderSession: %v", err)
	}
	defer session.Close()

	frame := make([]int16, 1152*2)
	if err := session.WriteFrame(frame); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	offset, _ := stream.Subscribe("listener", 4096)
	buf := make([]byte, 1024)
	n, _, skipped := stream.Buffer.ReadAt(offset, buf)
	if skipped || n == 0 {
		t.Fatal("expected new listener to receive decodable bytes immediately")
	}
}
```

- [ ] **Step 2: Run the regression tests**

Run: `go test ./relay -run 'Test(AutoDJTransitionContinuityFeedsTranscoderOutput|ListenerConnectDuringSilenceWindowStillGetsDecodableBytes)' -count=1`

Expected: FAIL until the continuity path is fully wired.

- [ ] **Step 3: Fill in the minimal regression harnesses and keep the logging hooks**

```go
logger.L.Infow("AutoDJ transition entering silence",
	"mount", s.OutputMount,
	"current_file", s.CurrentFilePath,
)

logger.L.Infow("AutoDJ transition activating next track",
	"mount", s.OutputMount,
	"next_file", path,
	"gap_ms", time.Since(gapStarted).Milliseconds(),
)
```

```go
func TestAutoDJTransitionContinuityFeedsTranscoderOutput(t *testing.T) {
	// Use two back-to-back writes with a short sleep between them to model
	// source continuity across the handoff interval, then assert the fallback
	// output mount keeps growing instead of waiting for a brand-new source.
}
```

- [ ] **Step 4: Run the relay package and targeted server regression tests**

Run: `go test ./relay -count=1`

Expected: PASS

Run: `go test ./server -run 'Test(RegisterHLSRejectsOpusStreams|APIDeleteAutoDJAllowsRecreateOnSameMount)' -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add relay/autodj_output_session_test.go relay/transcode_test.go relay/streamer.go
git commit -m "test: cover autodj transition continuity regressions"
```

### Task 5: Final verification pass

**Files:**
- Modify: none expected
- Test: `relay/*.go`, `server/*.go`

- [ ] **Step 1: Run formatting-safe verification commands**

Run: `go test ./relay ./server -count=1`

Expected: PASS

- [ ] **Step 2: Manually inspect the final diff for scope**

Run: `git diff --stat HEAD~4..HEAD`

Expected: only `relay/transcode.go`, `relay/transcode_test.go`, `relay/autodj_output_session.go`, `relay/autodj_output_session_test.go`, and `relay/streamer.go` unless a small helper extraction was necessary.

- [ ] **Step 3: Smoke-check the design requirements against the spec**

```text
- Persistent session exists per AutoDJ mount.
- Silence is emitted only during automatic track handoff.
- Metadata is not emitted for silence.
- Stop state does not emit endless silence.
- Transcoded fallback inherits continuity from the source mount.
```

- [ ] **Step 4: Commit**

```bash
git add relay/transcode.go relay/transcode_test.go relay/autodj_output_session.go relay/autodj_output_session_test.go relay/streamer.go
git commit -m "feat: keep autodj streams continuous across track transitions"
```
