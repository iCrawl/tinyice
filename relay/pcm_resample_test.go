package relay

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

type countingReader struct {
	r     io.Reader
	bytes int
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.bytes += n
	return n, err
}

func buildPCMBytes(samplesPerChannel, channels int) []byte {
	pcm := make([]byte, samplesPerChannel*channels*2)
	for i := 0; i < samplesPerChannel; i++ {
		for ch := 0; ch < channels; ch++ {
			sample := int16((i + 1) * (ch + 1))
			binary.LittleEndian.PutUint16(pcm[(i*channels+ch)*2:], uint16(sample))
		}
	}
	return pcm
}

func TestPCMFrameReaderPassThrough48000Stereo(t *testing.T) {
	const (
		srcRate         = 48000
		dstRate         = 48000
		channels        = 2
		samplesPerFrame = 960
		frameCount      = 3
	)

	source := buildPCMBytes(samplesPerFrame*frameCount, channels)
	reader := newPCMFrameReader(bytes.NewReader(source), srcRate, dstRate, channels, samplesPerFrame)

	frame := make([]int16, samplesPerFrame*channels)
	for i := 0; i < frameCount; i++ {
		n, err := reader.ReadFrame(frame)
		if err != nil {
			t.Fatalf("frame %d: unexpected error: %v", i, err)
		}
		if n != len(frame) {
			t.Fatalf("frame %d: expected %d samples, got %d", i, len(frame), n)
		}
	}

	if _, err := reader.ReadFrame(frame); err != io.EOF {
		t.Fatalf("expected EOF after exact pass-through frames, got %v", err)
	}
}

func TestPCMFrameReaderResamples44100StereoWithoutDrift(t *testing.T) {
	const (
		srcRate         = 44100
		dstRate         = 48000
		channels        = 2
		samplesPerFrame = 960
		frameCount      = 50
	)

	source := buildPCMBytes(srcRate, channels)
	counter := &countingReader{r: bytes.NewReader(source)}
	reader := newPCMFrameReader(counter, srcRate, dstRate, channels, samplesPerFrame)

	frame := make([]int16, samplesPerFrame*channels)
	for i := 0; i < frameCount; i++ {
		n, err := reader.ReadFrame(frame)
		if err != nil {
			t.Fatalf("frame %d: unexpected error: %v", i, err)
		}
		if n != len(frame) {
			t.Fatalf("frame %d: expected %d samples, got %d", i, len(frame), n)
		}
	}

	if counter.bytes != len(source) {
		t.Fatalf("expected to consume %d source bytes after 1 second of output, got %d", len(source), counter.bytes)
	}

	if _, err := reader.ReadFrame(frame); err != io.EOF {
		t.Fatalf("expected EOF after 50 Opus frames from 1 second of 44.1kHz PCM, got %v", err)
	}
}

func TestPCMFrameReaderResamples32000Mono(t *testing.T) {
	const (
		srcRate         = 32000
		dstRate         = 48000
		channels        = 1
		samplesPerFrame = 960
		frameCount      = 25
	)

	source := buildPCMBytes(srcRate/2, channels)
	counter := &countingReader{r: bytes.NewReader(source)}
	reader := newPCMFrameReader(counter, srcRate, dstRate, channels, samplesPerFrame)

	frame := make([]int16, samplesPerFrame*channels)
	for i := 0; i < frameCount; i++ {
		n, err := reader.ReadFrame(frame)
		if err != nil {
			t.Fatalf("frame %d: unexpected error: %v", i, err)
		}
		if n != len(frame) {
			t.Fatalf("frame %d: expected %d samples, got %d", i, len(frame), n)
		}
	}

	if counter.bytes != len(source) {
		t.Fatalf("expected to consume %d source bytes after half-second output, got %d", len(source), counter.bytes)
	}

	if _, err := reader.ReadFrame(frame); err != io.EOF {
		t.Fatalf("expected EOF after 25 Opus frames from half-second of 32kHz mono PCM, got %v", err)
	}
}
