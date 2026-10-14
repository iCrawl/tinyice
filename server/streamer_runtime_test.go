package server

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DatanoiseTV/tinyice/relay"
)

func makeServerRuntimeWAV(channels, sampleRate, bitsPerSample, dataBytes int) []byte {
	byteRate := sampleRate * channels * bitsPerSample / 8
	blockAlign := channels * bitsPerSample / 8
	buf := &bytes.Buffer{}
	buf.WriteString("RIFF")
	_ = binary.Write(buf, binary.LittleEndian, uint32(36+dataBytes))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	_ = binary.Write(buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(buf, binary.LittleEndian, uint16(channels))
	_ = binary.Write(buf, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(buf, binary.LittleEndian, uint32(byteRate))
	_ = binary.Write(buf, binary.LittleEndian, uint16(blockAlign))
	_ = binary.Write(buf, binary.LittleEndian, uint16(bitsPerSample))
	buf.WriteString("data")
	_ = binary.Write(buf, binary.LittleEndian, uint32(dataBytes))
	buf.Write(make([]byte, dataBytes))
	return buf.Bytes()
}

func TestNewServerWiresAutoDJRuntimeRegistry(t *testing.T) {
	s := newListenerAPITestServer(t)

	dir := t.TempDir()
	wavPath := filepath.Join(dir, "silence.wav")
	if err := os.WriteFile(wavPath, makeServerRuntimeWAV(2, 48000, 16, 48000*4/5), 0600); err != nil {
		t.Fatalf("write wav fixture: %v", err)
	}

	streamer, err := s.StreamerM.StartStreamer("AutoDJ", "/auto", dir, true, "mp3", 128, true, []string{wavPath}, false, "", "", true, "", "", 0, "", 0)
	if err != nil {
		t.Fatalf("StartStreamer: %v", err)
	}
	defer s.StreamerM.DeleteStreamer("/auto")

	streamer.Play()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if rt, ok := s.RuntimeRegistry.Get("/auto"); ok && rt.Source == relay.SourceAutoDJ {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("expected AutoDJ started through server manager to register runtime source")
}
