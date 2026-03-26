package relay

import (
	"bytes"
	"context"
	"testing"
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

	data := make([]byte, stream.Buffer.Head)
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
