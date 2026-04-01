package relay

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestRecoverDeadSongCommandMountSkipsMountsWithoutSongCommand(t *testing.T) {
	r := NewRelay(false, nil)
	sm := NewStreamerManager(r, nil)
	sm.instances["/dead"] = &Streamer{
		Name:        "NoCmd",
		OutputMount: "/dead",
		State:       StatePlaying,
		relay:       r,
	}

	sm.RecoverDeadSongCommandMount("/dead")

	if sm.DeadRecoveryActive("/dead") {
		t.Fatal("expected no recovery worker for mount without song_command")
	}
}

func TestRecoverDeadSongCommandMountStartsOnlyOneWorker(t *testing.T) {
	r := NewRelay(false, nil)
	sm := NewStreamerManager(r, nil)
	sm.instances["/dead"] = &Streamer{
		Name:               "Cmd",
		OutputMount:        "/dead",
		State:              StatePlaying,
		SongCommand:        "printf song.mp3",
		SongCommandTimeout: 1,
		relay:              r,
	}

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var launches int32
	sm.recoveryExecSongCommand = func(*Streamer) (string, error) {
		if atomic.AddInt32(&launches, 1) == 1 {
			started <- struct{}{}
		}
		<-release
		return "", errors.New("blocked")
	}

	sm.RecoverDeadSongCommandMount("/dead")
	<-started
	sm.RecoverDeadSongCommandMount("/dead")

	if got := atomic.LoadInt32(&launches); got != 1 {
		t.Fatalf("expected one recovery worker launch, got %d", got)
	}

	close(release)
}

func TestRecoverDeadSongCommandMountRetriesUntilActivationSucceeds(t *testing.T) {
	r := NewRelay(false, nil)
	sm := NewStreamerManager(r, nil)
	sm.instances["/dead"] = &Streamer{
		Name:               "Cmd",
		OutputMount:        "/dead",
		State:              StatePlaying,
		SongCommand:        "printf track.mp3",
		SongCommandTimeout: 1,
		relay:              r,
	}

	stream := r.GetOrCreateStream("/dead")
	stream.SetLastDataAt(time.Now().Add(-time.Minute))

	var execs int32
	var activationCalls int32
	sm.recoveryExecSongCommand = func(*Streamer) (string, error) {
		attempt := atomic.AddInt32(&execs, 1)
		if attempt == 1 {
			return "", errors.New("first attempt fails")
		}
		return "/tmp/recovered.mp3", nil
	}
	sm.recoveryAfter = func(time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch
	}
	sm.recoveryActivatePath = func(ctx context.Context, sm *StreamerManager, s *Streamer, path string) error {
		atomic.AddInt32(&activationCalls, 1)
		stream.SetLastDataAt(time.Now())
		return nil
	}

	sm.RecoverDeadSongCommandMount("/dead")

	deadline := time.After(500 * time.Millisecond)
	for sm.DeadRecoveryActive("/dead") {
		select {
		case <-deadline:
			t.Fatal("expected recovery worker to exit after successful activation")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	if got := atomic.LoadInt32(&execs); got < 2 {
		t.Fatalf("expected song_command retry before success, got %d attempts", got)
	}
	if got := atomic.LoadInt32(&activationCalls); got != 1 {
		t.Fatalf("expected one activation after successful retry, got %d", got)
	}
}

func TestRecoverDeadSongCommandMountExitsWhenMountRecoversNaturally(t *testing.T) {
	r := NewRelay(false, nil)
	sm := NewStreamerManager(r, nil)
	sm.instances["/dead"] = &Streamer{
		Name:               "Cmd",
		OutputMount:        "/dead",
		State:              StatePlaying,
		SongCommand:        "printf track.mp3",
		SongCommandTimeout: 1,
		relay:              r,
	}

	stream := r.GetOrCreateStream("/dead")
	stream.SetLastDataAt(time.Now().Add(-time.Minute))

	blocked := make(chan struct{})
	sm.recoveryExecSongCommand = func(*Streamer) (string, error) {
		<-blocked
		return "", errors.New("retry after natural recovery")
	}
	sm.recoveryAfter = func(time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch
	}

	sm.RecoverDeadSongCommandMount("/dead")
	stream.SetLastDataAt(time.Now())
	close(blocked)

	deadline := time.After(500 * time.Millisecond)
	for sm.DeadRecoveryActive("/dead") {
		select {
		case <-deadline:
			t.Fatal("expected natural recovery to stop the worker")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestRecoverDeadSongCommandMountRestartsNonManualStoppedMount(t *testing.T) {
	r := NewRelay(false, nil)
	sm := NewStreamerManager(r, nil)
	sm.instances["/dead"] = &Streamer{
		Name:               "Cmd",
		OutputMount:        "/dead",
		State:              StateStopped,
		SongCommand:        "printf track.mp3",
		SongCommandTimeout: 1,
		relay:              r,
		stateCh:            make(chan struct{}, 1),
	}

	stream := r.GetOrCreateStream("/dead")
	stream.SetLastDataAt(time.Now().Add(-time.Minute))

	var activationCalls int32
	sm.recoveryExecSongCommand = func(*Streamer) (string, error) {
		return "/tmp/recovered.mp3", nil
	}
	sm.recoveryAfter = func(time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch
	}
	sm.recoveryActivatePath = func(ctx context.Context, sm *StreamerManager, s *Streamer, path string) error {
		atomic.AddInt32(&activationCalls, 1)
		stream.SetLastDataAt(time.Now())
		s.mu.Lock()
		s.State = StatePlaying
		s.mu.Unlock()
		return nil
	}

	sm.RecoverDeadSongCommandMount("/dead")

	deadline := time.After(500 * time.Millisecond)
	for sm.DeadRecoveryActive("/dead") {
		select {
		case <-deadline:
			t.Fatal("expected recovery worker to exit after restarting non-manual stop")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	if got := atomic.LoadInt32(&activationCalls); got != 1 {
		t.Fatalf("expected recovery activation for non-manual stopped mount, got %d", got)
	}
	if state := sm.instances["/dead"].GetStats().State; state != StatePlaying {
		t.Fatalf("expected recovery to restore playing state, got %v", state)
	}
}

func TestRecoverDeadSongCommandMountSkipsManualStop(t *testing.T) {
	r := NewRelay(false, nil)
	sm := NewStreamerManager(r, nil)
	streamer := &Streamer{
		Name:               "Cmd",
		OutputMount:        "/dead",
		State:              StatePlaying,
		SongCommand:        "printf track.mp3",
		SongCommandTimeout: 1,
		relay:              r,
		stateCh:            make(chan struct{}, 1),
	}
	streamer.Stop()
	sm.instances["/dead"] = streamer

	stream := r.GetOrCreateStream("/dead")
	stream.SetLastDataAt(time.Now().Add(-time.Minute))

	var activationCalls int32
	sm.recoveryExecSongCommand = func(*Streamer) (string, error) {
		return "/tmp/recovered.mp3", nil
	}
	sm.recoveryActivatePath = func(ctx context.Context, sm *StreamerManager, s *Streamer, path string) error {
		atomic.AddInt32(&activationCalls, 1)
		return nil
	}

	sm.RecoverDeadSongCommandMount("/dead")

	deadline := time.After(500 * time.Millisecond)
	for sm.DeadRecoveryActive("/dead") {
		select {
		case <-deadline:
			t.Fatal("expected manual stop recovery worker to exit")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	if got := atomic.LoadInt32(&activationCalls); got != 0 {
		t.Fatalf("expected manual stop to suppress recovery activation, got %d", got)
	}
	if state := sm.instances["/dead"].GetStats().State; state != StateStopped {
		t.Fatalf("expected manual stop to remain stopped, got %v", state)
	}
}

func TestRecoverDeadSongCommandMountRecordsRecoveryStartAndSuccess(t *testing.T) {
	r := NewRelay(false, nil)
	sm := NewStreamerManager(r, nil)
	sm.instances["/dead"] = &Streamer{
		Name:               "Cmd",
		OutputMount:        "/dead",
		State:              StatePlaying,
		SongCommand:        "printf track.mp3",
		SongCommandTimeout: 1,
		relay:              r,
	}

	stream := r.GetOrCreateStream("/dead")
	stream.SetLastDataAt(time.Now().Add(-time.Minute))

	sm.recoveryExecSongCommand = func(*Streamer) (string, error) {
		return "/tmp/recovered.mp3", nil
	}
	sm.recoveryAfter = func(time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch
	}
	sm.recoveryActivatePath = func(ctx context.Context, sm *StreamerManager, s *Streamer, path string) error {
		stream.SetLastDataAt(time.Now())
		return nil
	}

	sm.RecoverDeadSongCommandMount("/dead")

	deadline := time.After(500 * time.Millisecond)
	for sm.DeadRecoveryActive("/dead") {
		select {
		case <-deadline:
			t.Fatal("expected recovery worker to exit")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	current, ok := r.Diagnostics.Current("/dead")
	if !ok {
		t.Fatal("expected recovery diagnostic snapshot")
	}
	if current.Status != DiagnosticStatusRunning {
		t.Fatalf("expected running after successful recovery, got %q", current.Status)
	}
	if current.LastRecoveryResult != "success" {
		t.Fatalf("expected success recovery result, got %q", current.LastRecoveryResult)
	}
	if len(current.History) < 2 {
		t.Fatalf("expected recovery history entries, got %d", len(current.History))
	}
}

func TestRecoverDeadSongCommandMountRecordsRecoveryFailure(t *testing.T) {
	r := NewRelay(false, nil)
	sm := NewStreamerManager(r, nil)
	sm.instances["/dead"] = &Streamer{
		Name:               "Cmd",
		OutputMount:        "/dead",
		State:              StatePlaying,
		SongCommand:        "printf track.mp3",
		SongCommandTimeout: 1,
		relay:              r,
	}

	stream := r.GetOrCreateStream("/dead")
	stream.SetLastDataAt(time.Now().Add(-time.Minute))

	waitCh := make(chan time.Time)
	sm.recoveryExecSongCommand = func(*Streamer) (string, error) {
		return "", errors.New("boom")
	}
	sm.recoveryAfter = func(time.Duration) <-chan time.Time {
		return waitCh
	}

	sm.RecoverDeadSongCommandMount("/dead")

	deadline := time.After(500 * time.Millisecond)
	for {
		current, ok := r.Diagnostics.Current("/dead")
		if ok && current.Class == DiagnosticClassRecoveryFailed {
			if current.Status != DiagnosticStatusError {
				t.Fatalf("expected error status for failed recovery, got %q", current.Status)
			}
			if current.LastRecoveryResult != "failed" {
				t.Fatalf("expected failed recovery result, got %q", current.LastRecoveryResult)
			}
			break
		}

		select {
		case <-deadline:
			t.Fatal("expected recovery failure diagnostic")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	sm.mu.RLock()
	cancel := sm.deadRecovery["/dead"]
	sm.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
}

func TestNextTrackCandidateUsesPlaylistExhaustedOnlyWithoutSongCommand(t *testing.T) {
	r := NewRelay(false, nil)
	s := &Streamer{
		Name:        "NoCmd",
		OutputMount: "/empty",
		State:       StatePlaying,
		relay:       r,
	}

	_, _, _, ok := s.nextTrackCandidate()
	if ok {
		t.Fatal("expected no track candidate")
	}

	current, ok := r.Diagnostics.Current("/empty")
	if !ok {
		t.Fatal("expected diagnostics for empty playlist")
	}
	if current.Class != DiagnosticClassPlaylistExhausted {
		t.Fatalf("expected playlist_exhausted, got %q", current.Class)
	}
}

func TestNextTrackCandidateStoresInvalidSongCommandPathInDiagnosticDetails(t *testing.T) {
	r := NewRelay(false, nil)
	musicDir := t.TempDir()
	badFile := filepath.Join(musicDir, "bad.txt")
	if err := os.WriteFile(badFile, []byte("not audio"), 0600); err != nil {
		t.Fatalf("write invalid file: %v", err)
	}

	s := &Streamer{
		Name:               "Cmd",
		OutputMount:        "/bad",
		State:              StatePlaying,
		MusicDir:           musicDir,
		SongCommand:        "printf bad.txt",
		SongCommandTimeout: 1,
		relay:              r,
	}

	_, _, _, ok := s.nextTrackCandidate()
	if ok {
		t.Fatal("expected no valid track candidate")
	}

	current, ok := r.Diagnostics.Current("/bad")
	if !ok {
		t.Fatal("expected diagnostics for invalid song_command file")
	}
	if current.Class != DiagnosticClassSongCommandInvalid {
		t.Fatalf("expected song_command_invalid_file, got %q", current.Class)
	}
	if len(current.History) == 0 {
		t.Fatal("expected diagnostic history entry")
	}
	latest := current.History[len(current.History)-1]
	if latest.Details["path"] != badFile {
		t.Fatalf("expected invalid path detail %q, got %#v", badFile, latest.Details)
	}
}
