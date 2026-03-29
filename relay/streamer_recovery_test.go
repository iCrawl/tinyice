package relay

import (
	"context"
	"errors"
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
	stream.LastDataReceived = time.Now().Add(-time.Minute)

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
	stream.LastDataReceived = time.Now()
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
