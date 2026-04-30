package relay

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/DatanoiseTV/tinyice/config"
)

func TestStreamerManagerSetRuntimeRegistryPropagatesToExistingStreamers(t *testing.T) {
	r := NewRelay(false, nil)
	sm := NewStreamerManager(r, &config.Config{})
	streamer, err := sm.StartStreamer("AutoDJ", "/auto", t.TempDir(), false, "mp3", 128, true, nil, false, "", "", true, "", "", 0)
	if err != nil {
		t.Fatalf("StartStreamer: %v", err)
	}

	rr := NewRuntimeRegistry(r)
	sm.SetRuntimeRegistry(rr)

	if streamer.runtimeRegistry != rr {
		t.Fatal("expected existing streamer to receive runtime registry")
	}
}

func TestStreamerFileAttachesAutoDJRuntime(t *testing.T) {
	r := NewRelay(false, nil)
	rr := NewRuntimeRegistry(r)
	sm := NewStreamerManager(r, &config.Config{})
	sm.SetRuntimeRegistry(rr)

	dir := t.TempDir()
	path := filepath.Join(dir, "silence.wav")
	if err := os.WriteFile(path, makeWavHeader(2, 48000, 16, 48000*4/10), 0600); err != nil {
		t.Fatalf("write wav fixture: %v", err)
	}

	streamer := &Streamer{
		Name:            "AutoDJ",
		OutputMount:     "/auto",
		MusicDir:        dir,
		Format:          "mp3",
		Bitrate:         128,
		InjectMetadata:  true,
		Visible:         true,
		relay:           r,
		runtimeRegistry: rr,
	}

	if err := sm.streamFile(context.Background(), streamer, path, 0, 1); err != nil {
		t.Fatalf("streamFile: %v", err)
	}

	rt, ok := rr.Get("/auto")
	if !ok {
		t.Fatal("expected AutoDJ runtime to be registered")
	}
	if rt.Source != SourceAutoDJ {
		t.Fatalf("expected AutoDJ source, got %q", rt.Source)
	}
	if rt.Stream != r.GetOrCreateStream("/auto") {
		t.Fatal("expected runtime to reference the AutoDJ output stream")
	}
}
