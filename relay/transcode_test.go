package relay

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
)

func TestEncodeMP3UsesConfiguredBitrate(t *testing.T) {
	r := NewRelay(false, nil)
	stream := r.GetOrCreateStream("/mp3")

	// One stereo MP3 frame worth of silent PCM at 44.1kHz.
	pcm := bytes.NewReader(make([]byte, 4608))
	var bytesEncoded int64

	EncodeMP3(context.Background(), r, stream, pcm, 64, &bytesEncoded, false, 44100)

	if bytesEncoded == 0 {
		t.Fatal("expected encoded MP3 bytes")
	}

	data := make([]byte, stream.Buffer.HeadPosition())
	n, _, skipped := stream.Buffer.ReadAt(0, data)
	if skipped {
		t.Fatal("unexpected buffer skip")
	}
	data = data[:n]
	if len(data) < 4 {
		t.Fatalf("expected MP3 frame header, got %d bytes", len(data))
	}

	if got := bitrateFromMP3Header(data[:4]); got != 64 {
		t.Fatalf("expected encoded bitrate 64 kbps, got %d kbps", got)
	}
}

func bitrateFromMP3Header(header []byte) int {
	if len(header) < 4 {
		return -1
	}
	if header[0] != 0xFF || (header[1]&0xE0) != 0xE0 {
		return -1
	}

	versionID := (header[1] >> 3) & 0x03
	layer := (header[1] >> 1) & 0x03
	bitrateIndex := (header[2] >> 4) & 0x0F

	if versionID != 0x03 || layer != 0x01 || bitrateIndex == 0 || bitrateIndex == 0x0F {
		return -1
	}

	// MPEG-1 Layer III bitrate lookup table.
	return []int{
		0, 32, 40, 48, 56, 64, 80, 96,
		112, 128, 160, 192, 224, 256, 320, 0,
	}[bitrateIndex]
}

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

	headAfterFirst := stream.Buffer.HeadPosition()
	if headAfterFirst == 0 {
		t.Fatal("expected encoded bytes after first frame")
	}

	if err := session.WriteFrame(frame); err != nil {
		t.Fatalf("WriteFrame #2: %v", err)
	}
	if stream.Buffer.HeadPosition() <= headAfterFirst {
		t.Fatal("expected second frame to append encoded bytes")
	}
}

func TestOpusEncoderSessionWritesHeadersOnlyOnce(t *testing.T) {
	if raceEnabled {
		t.Skip("opus-go encoder is not race-safe under the Go race detector")
	}

	r := NewRelay(false, nil)
	stream := r.GetOrCreateStream("/opus-session")

	session, err := NewOpusEncoderSession(stream, r, 96, 48000, 2, nil)
	if err != nil {
		t.Fatalf("NewOpusEncoderSession: %v", err)
	}
	defer session.Close()

	frame := make([]int16, (48000/50)*2)
	if err := session.WriteFrame(frame); err != nil {
		t.Fatalf("WriteFrame #1: %v", err)
	}

	oggHead := append([]byte(nil), stream.OggHead...)
	headAfterFirst := stream.Buffer.HeadPosition()

	if err := session.WriteFrame(frame); err != nil {
		t.Fatalf("WriteFrame #2: %v", err)
	}
	if stream.Buffer.HeadPosition() <= headAfterFirst {
		t.Fatal("expected second frame to append opus packets")
	}
	if len(stream.OggHead) != len(oggHead) {
		t.Fatal("expected Ogg head to remain stable across writes")
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
	inst := &TranscoderInstance{
		Config: &config.TranscoderConfig{
			Name:        "fallback",
			InputMount:  "/source",
			OutputMount: "/fallback",
			Format:      "mp3",
			Bitrate:     64,
		},
	}
	go tm.performTranscode(ctx, inst)

	time.Sleep(250 * time.Millisecond)

	out, ok := r.GetStream("/fallback")
	if !ok || out.Buffer.HeadPosition() == 0 {
		t.Fatal("expected transcoder output to accumulate bytes from a continuous source")
	}
}

func TestTranscoderPacingDoesNotDrainBufferedBurstImmediately(t *testing.T) {
	r := NewRelay(false, nil)
	source := r.GetOrCreateStream("/paced-source")
	source.ContentType = "audio/mpeg"

	session, err := NewMP3EncoderSession(source, r, 64, 44100, nil)
	if err != nil {
		t.Fatalf("NewMP3EncoderSession: %v", err)
	}
	defer session.Close()

	frame := make([]int16, 1152*2)
	for i := 0; i < 120; i++ {
		if err := session.WriteFrame(frame); err != nil {
			t.Fatalf("WriteFrame #%d: %v", i+1, err)
		}
	}

	sourceHead := source.Buffer.HeadPosition()
	if sourceHead == 0 {
		t.Fatal("expected buffered source audio")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tm := NewTranscoderManager(r)
	inst := &TranscoderInstance{
		Config: &config.TranscoderConfig{
			Name:        "paced-fallback",
			InputMount:  "/paced-source",
			OutputMount: "/paced-fallback",
			Format:      "mp3",
			Bitrate:     64,
		},
	}
	go tm.performTranscode(ctx, inst)

	time.Sleep(300 * time.Millisecond)

	out, ok := r.GetStream("/paced-fallback")
	if !ok {
		t.Fatal("expected transcoder output stream")
	}
	if out.Buffer.HeadPosition() == 0 {
		t.Fatal("expected paced transcoder to emit some output")
	}
	if out.Buffer.HeadPosition() >= sourceHead/2 {
		t.Fatalf("expected paced transcoder to avoid draining buffered burst immediately, got %d bytes from %d-byte source buffer", out.Buffer.HeadPosition(), sourceHead)
	}
}
