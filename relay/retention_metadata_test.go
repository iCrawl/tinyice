package relay

import (
	"fmt"
	"github.com/DatanoiseTV/tinyice/config"
	"testing"
	"time"
)

func TestTranscodeMirrorReconcilesDroppedEventsBroadFieldsAndInputFlaps(t *testing.T) {
	r := NewRelay(false, nil)
	input := r.GetOrCreateStream("/input")
	input.Name = "Station"
	input.SetCurrentSong("First", r)
	tm := NewTranscoderManager(r)
	defer tm.StopAll()
	tm.StartTranscoder(&config.TranscoderConfig{Name: "mirror", InputMount: "/input", OutputMount: "/output", Format: "mp3", Bitrate: 128})
	waitRetention(t, func() bool { out, ok := r.GetStream("/output"); return ok && out.GetCurrentSong() == "First" })
	out, _ := r.GetStream("/output")
	initial := out.PlaybackSnapshot()
	if initial.PlaybackID != input.PlaybackSnapshot().PlaybackID || !initial.StartedAt.Equal(input.PlaybackSnapshot().StartedAt) {
		t.Fatal("mirror lost input playback identity or timestamp")
	}
	out.mu.Lock()
	input.SetCurrentSong("blocked mirror", r)
	time.Sleep(30 * time.Millisecond)
	for i := 0; i < 40; i++ {
		input.SetCurrentSong(fmt.Sprintf("Track %d", i), r)
	}
	input.mu.Lock()
	input.Genre = "New genre"
	input.Description = "New description"
	input.URL = "https://radio.example"
	input.Public = true
	input.mu.Unlock()
	out.mu.Unlock()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		state := out.Snapshot()
		if state.CurrentSong == "Track 39" && state.Genre == "New genre" && state.Description == "New description" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	state := out.Snapshot()
	if state.CurrentSong != "Track 39" || state.Genre != "New genre" || state.Description != "New description" {
		t.Fatal("dropped metadata or broad fields were not reconciled")
	}
	mirrored := out.PlaybackSnapshot()
	if mirrored.PlaybackID != input.PlaybackSnapshot().PlaybackID || mirrored.Revision <= input.PlaybackSnapshot().Revision {
		t.Fatal("output did not assign its own publication revision")
	}
	replay := false
	for _, mc := range r.LastMetadata() {
		if mc.Mount == "/output" && mc.PlaybackID == mirrored.PlaybackID {
			replay = true
		}
	}
	if !replay {
		t.Fatal("mirror did not publish replayable output event")
	}
	stableRevision := mirrored.Revision
	time.Sleep(5100 * time.Millisecond)
	if out.PlaybackSnapshot().Revision != stableRevision {
		t.Fatal("unchanged five-second reconciliation republished metadata")
	}
	r.RemoveStream("/input")
	waitRetention(t, func() bool { return out.GetCurrentSong() == "" })
	cleared := out.PlaybackSnapshot()
	if cleared.PlaybackID != "" {
		t.Fatal("input disappearance did not clear output playback")
	}
	next := r.GetOrCreateStream("/input")
	next.SetCurrentSong("Recreated source", r)
	waitRetention(t, func() bool { return out.GetCurrentSong() == "Recreated source" })
	if out.PlaybackSnapshot().PlaybackID != next.PlaybackSnapshot().PlaybackID || out.PlaybackSnapshot().Revision <= cleared.Revision {
		t.Fatal("mirror stayed attached to previous input stream")
	}
	old := input.PlaybackSnapshot()
	input.SetCurrentSong("delayed old cleanup", r)
	r.NotifyMetadataChange(old)
	if out.GetCurrentSong() != "Recreated source" {
		t.Fatal("old source restored stale mirrored title")
	}
}
