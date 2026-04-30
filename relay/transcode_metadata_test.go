package relay

import (
	"testing"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
)

func TestTranscoderMirrorsInputCurrentSongWithoutWaitingForDecoder(t *testing.T) {
	r := NewRelay(false, nil)
	source := r.GetOrCreateStream("/source")
	source.SetCurrentSong("Artist - Title", r)

	tm := NewTranscoderManager(r)
	defer tm.StopAll()
	tm.StartTranscoder(&config.TranscoderConfig{
		Name:        "metadata-follow",
		InputMount:  "/source",
		OutputMount: "/fallback",
		Format:      "mp3",
		Bitrate:     64,
	})

	assertCurrentSongEventually(t, r, "/fallback", "Artist - Title")

	source.SetCurrentSong("Next Artist - Next Title", r)
	assertCurrentSongEventually(t, r, "/fallback", "Next Artist - Next Title")
}

func TestStoppedTranscoderStopsMirroringInputCurrentSong(t *testing.T) {
	r := NewRelay(false, nil)
	source := r.GetOrCreateStream("/source")
	source.SetCurrentSong("Artist - Title", r)

	tm := NewTranscoderManager(r)
	tm.StartTranscoder(&config.TranscoderConfig{
		Name:        "metadata-stop",
		InputMount:  "/source",
		OutputMount: "/fallback",
		Format:      "mp3",
		Bitrate:     64,
	})

	assertCurrentSongEventually(t, r, "/fallback", "Artist - Title")

	tm.StopAll()
	source.SetCurrentSong("Next Artist - Next Title", r)
	time.Sleep(100 * time.Millisecond)

	out, ok := r.GetStream("/fallback")
	if !ok {
		t.Fatal("expected transcoder output stream")
	}
	if got := out.GetCurrentSong(); got != "Artist - Title" {
		t.Fatalf("expected output metadata to stop changing after transcoder stop, got %q", got)
	}
}

func assertCurrentSongEventually(t *testing.T, r *Relay, mount, want string) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		out, ok := r.GetStream(mount)
		if ok && out.GetCurrentSong() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	out, ok := r.GetStream(mount)
	if !ok {
		t.Fatalf("expected stream %s", mount)
	}
	t.Fatalf("expected current song %q, got %q", want, out.GetCurrentSong())
}
