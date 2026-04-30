package relay

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

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
	stream.LastDataReceived = time.Now().Add(-time.Minute)

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
		stream.LastDataReceived = time.Now()
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
	stream.LastDataReceived = time.Now().Add(-time.Minute)

	sm.recoveryExecSongCommand = func(*Streamer) (string, error) {
		return "/tmp/recovered.mp3", nil
	}
	sm.recoveryAfter = func(time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch
	}
	sm.recoveryActivatePath = func(ctx context.Context, sm *StreamerManager, s *Streamer, path string) error {
		stream.LastDataReceived = time.Now()
		return nil
	}

	sm.RecoverDeadSongCommandMount("/dead")

	deadline := time.After(500 * time.Millisecond)
	for sm.DeadRecoveryActive("/dead") {
		select {
		case <-deadline:
			t.Fatal("expected recovery worker to exit after success")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	current, ok := r.Diagnostics.Current("/dead")
	if !ok {
		t.Fatal("expected recovery diagnostic")
	}
	if current.Class != DiagnosticClassRecoverySucceeded {
		t.Fatalf("expected recovery_succeeded, got %q", current.Class)
	}
	if current.Status != DiagnosticStatusRunning {
		t.Fatalf("expected running status, got %q", current.Status)
	}
	if current.LastRecoveryResult != "success" {
		t.Fatalf("expected success recovery result, got %q", current.LastRecoveryResult)
	}
	if len(current.History) < 2 || current.History[0].Class != DiagnosticClassRecoveryStarted {
		t.Fatalf("expected recovery_started history before success, got %#v", current.History)
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

	block := make(chan time.Time)
	sm.recoveryExecSongCommand = func(*Streamer) (string, error) {
		return "", errors.New("still dead")
	}
	sm.recoveryAfter = func(time.Duration) <-chan time.Time {
		return block
	}

	sm.RecoverDeadSongCommandMount("/dead")

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		current, ok := r.Diagnostics.Current("/dead")
		if ok && current.Class == DiagnosticClassRecoveryFailed {
			if current.Status != DiagnosticStatusError {
				t.Fatalf("expected error status, got %q", current.Status)
			}
			if current.Error != "still dead" {
				t.Fatalf("expected recovery error, got %q", current.Error)
			}
			if current.LastRecoveryResult != "failed" {
				t.Fatalf("expected failed recovery result, got %q", current.LastRecoveryResult)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("expected recovery_failed diagnostic")
}

func TestStopStreamerCancelsDeadSongCommandRecovery(t *testing.T) {
	r := NewRelay(false, nil)
	sm := NewStreamerManager(r, nil)
	sm.instances["/dead"] = &Streamer{
		Name:               "Cmd",
		OutputMount:        "/dead",
		State:              StatePlaying,
		SongCommand:        "printf track.mp3",
		SongCommandTimeout: 1,
		relay:              r,
		stateCh:            make(chan struct{}, 1),
	}

	started := make(chan struct{}, 1)
	block := make(chan time.Time)
	sm.recoveryExecSongCommand = func(*Streamer) (string, error) {
		started <- struct{}{}
		return "", errors.New("still dead")
	}
	sm.recoveryAfter = func(time.Duration) <-chan time.Time {
		return block
	}

	sm.RecoverDeadSongCommandMount("/dead")
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("expected recovery worker to start")
	}

	sm.StopStreamer("/dead")

	deadline := time.After(time.Second)
	for sm.DeadRecoveryActive("/dead") {
		select {
		case <-deadline:
			t.Fatal("expected StopStreamer to cancel recovery worker")
		default:
			time.Sleep(10 * time.Millisecond)
		}
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
	stream.LastDataReceived = time.Now().Add(-time.Minute)

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
		stream.LastDataReceived = time.Now()
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
