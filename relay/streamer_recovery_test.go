package relay

import (
	"context"
	"errors"
	"github.com/DatanoiseTV/tinyice/config"
	"testing"
	"time"
)

func TestRecoveryUsesPlaybackOwnerAndStopRevokesIntent(t *testing.T) {
	r := NewRelay(false, nil)
	sm := NewStreamerManager(r, &config.Config{})
	s, err := sm.StartStreamer("recover", "/recover", t.TempDir(), true, "mp3", 128, true, nil, false, "", "", true, "", "printf ''", 1, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sm.Shutdown()
	s.Play()
	waitRetention(t, func() bool { return s.RecoverySnapshot().Attempt > 0 })
	sm.RecoverDeadSongCommandMount("/recover")
	sm.RecoverDeadSongCommandMount("/recover")
	s.Stop()
	if sm.DeadRecoveryActive("/recover") {
		t.Fatal("stopped recovery remained active")
	}
	attempts := s.RecoverySnapshot().Attempt
	time.Sleep(120 * time.Millisecond)
	if s.RecoverySnapshot().Attempt != attempts {
		t.Fatal("recovery continued after stop")
	}
	if out, ok := r.GetStream("/recover"); ok && out.Buffer.HeadOffset() != 0 {
		t.Fatal("failed selection generated audio")
	}
}
func TestRecoveryBackoffCancelledByShutdown(t *testing.T) {
	r := NewRelay(false, nil)
	sm := NewStreamerManager(r, &config.Config{})
	s, err := sm.StartStreamer("cancel", "/cancel", t.TempDir(), true, "mp3", 128, true, nil, false, "", "", true, "", "sleep 30", 60, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	s.Play()
	waitRetention(t, func() bool { s.mu.RLock(); defer s.mu.RUnlock(); return s.commandCancel != nil })
	done := make(chan struct{})
	go func() { sm.Shutdown(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel song_command")
	}
	_ = context.Canceled
}
func TestSongCommandErrorClassification(t *testing.T) {
	if got := classifySongCommandError(errors.New("song command returned empty output")); got != DiagnosticClassSongCommandEmpty {
		t.Fatalf("expected empty output class, got %q", got)
	}
	if got := classifySongCommandError(errors.New("song command returned invalid file \"/tmp/bad\": boom")); got != DiagnosticClassSongCommandInvalid {
		t.Fatalf("expected invalid file class, got %q", got)
	}
	if got := songCommandReason(errors.New("exec failed")); got != "song_command failed" {
		t.Fatalf("expected generic reason, got %q", got)
	}
	details := songCommandDiagnosticDetails(errors.New("song command returned invalid file \"/tmp/bad\": boom"))
	if details["path"] != "/tmp/bad" {
		t.Fatalf("expected invalid file path detail, got %#v", details)
	}
}
