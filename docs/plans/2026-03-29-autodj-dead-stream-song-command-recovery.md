# AutoDJ Dead Stream Song Command Recovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Retry `song_command` with the existing relay backoff policy when an AutoDJ mount is marked `dead`, and hand successful recovery back to the normal AutoDJ playback loop.

**Architecture:** Keep the health monitor passive and use the existing server health-event callback as the trigger. Add a guarded recovery entry point on `StreamerManager` that owns one recovery worker per mount, reuses the relay `backoff` helper, and reactivates a valid track through the existing AutoDJ output-session path instead of creating a parallel streaming path.

**Tech Stack:** Go, existing `relay` and `server` packages, `go test`

---

## File Map

- Modify: `server/server.go`
  - Extend the health-monitor callback so `StatusDead` events can trigger AutoDJ recovery.
  - Add a small helper method to keep the callback logic testable and focused.
- Modify: `relay/streamer.go`
  - Add manager-owned dead-stream recovery bookkeeping.
  - Add a public manager entry point for dead AutoDJ recovery.
  - Add internal helpers for guarded worker launch, output-session reuse, and recovery cancellation.
  - Reuse the package-local `backoff` type from `relay/client.go`.
- Modify: `relay/stream.go`
  - Add a small helper for reading recent stream activity without duplicating lock logic in recovery code.
- Create: `relay/streamer_recovery_test.go`
  - Cover no-op behavior, duplicate suppression, successful recovery, and natural recovery exit.
- Modify: `relay/health_test.go`
  - Add a regression test that confirms `StatusDead` events are still emitted as expected for the recovery trigger path.
- Create: `server/stream_health_recovery_test.go`
  - Add a focused regression test for the server helper that handles health events.

### Task 1: Add The Recovery API And Test Seams

**Files:**
- Modify: `relay/streamer.go`
- Create: `relay/streamer_recovery_test.go`
- Test: `relay/streamer_recovery_test.go`

- [ ] **Step 1: Write the failing recovery-entry tests**

```go
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

	streamer, err := sm.StartStreamer("NoCmd", "/dead", ".", false, "mp3", 64, false, nil, false, "", "", true, "", "", 0)
	if err != nil {
		t.Fatalf("StartStreamer: %v", err)
	}
	streamer.Play()

	sm.RecoverDeadSongCommandMount("/dead")

	if sm.DeadRecoveryActive("/dead") {
		t.Fatal("expected no recovery worker for mount without song_command")
	}
}

func TestRecoverDeadSongCommandMountStartsOnlyOneWorker(t *testing.T) {
	r := NewRelay(false, nil)
	sm := NewStreamerManager(r, nil)

	streamer, err := sm.StartStreamer("Cmd", "/dead", ".", false, "mp3", 64, false, nil, false, "", "", true, "", "printf song.mp3", 1)
	if err != nil {
		t.Fatalf("StartStreamer: %v", err)
	}
	streamer.Play()

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
```

- [ ] **Step 2: Run the relay recovery tests to verify they fail**

Run: `go test ./relay -run 'TestRecoverDeadSongCommandMount(SkipsMountsWithoutSongCommand|StartsOnlyOneWorker)$'`

Expected: FAIL with errors such as `sm.RecoverDeadSongCommandMount undefined`, `sm.DeadRecoveryActive undefined`, or missing recovery hook fields.

- [ ] **Step 3: Add manager-owned recovery bookkeeping and a public recovery entry point**

```go
type StreamerManager struct {
	instances map[string]*Streamer
	mu        sync.RWMutex
	relay     *Relay
	config    *config.Config

	deadRecovery map[string]context.CancelFunc

	recoveryExecSongCommand func(*Streamer) (string, error)
	recoveryAfter           func(time.Duration) <-chan time.Time
	recoveryActivatePath    func(context.Context, *StreamerManager, *Streamer, string) error
}

func NewStreamerManager(r *Relay, cfg *config.Config) *StreamerManager {
	sm := &StreamerManager{
		instances:     make(map[string]*Streamer),
		relay:         r,
		config:        cfg,
		deadRecovery:  make(map[string]context.CancelFunc),
	}
	sm.recoveryExecSongCommand = func(s *Streamer) (string, error) { return s.execSongCommand() }
	sm.recoveryAfter = time.After
	sm.recoveryActivatePath = func(ctx context.Context, sm *StreamerManager, s *Streamer, path string) error {
		return sm.activateRecoveredSongCommandPath(ctx, s, path)
	}
	return sm
}

func (sm *StreamerManager) DeadRecoveryActive(mount string) bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	_, ok := sm.deadRecovery[mount]
	return ok
}

func (sm *StreamerManager) RecoverDeadSongCommandMount(mount string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	streamer, ok := sm.instances[mount]
	if !ok || streamer.SongCommand == "" {
		return
	}
	if _, running := sm.deadRecovery[mount]; running {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	sm.deadRecovery[mount] = cancel
	go sm.runDeadSongCommandRecovery(ctx, streamer)
}
```

- [ ] **Step 4: Run the relay recovery tests to verify they pass**

Run: `go test ./relay -run 'TestRecoverDeadSongCommandMount(SkipsMountsWithoutSongCommand|StartsOnlyOneWorker)$'`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add relay/streamer.go relay/streamer_recovery_test.go
git commit -m "feat: add AutoDJ dead-stream recovery entrypoint"
```

### Task 2: Implement The Recovery Worker And Fresh-Data Checks

**Files:**
- Modify: `relay/streamer.go`
- Modify: `relay/stream.go`
- Create: `relay/streamer_recovery_test.go`
- Test: `relay/streamer_recovery_test.go`

- [ ] **Step 1: Write the failing recovery-worker tests**

```go
func TestRecoverDeadSongCommandMountRetriesUntilActivationSucceeds(t *testing.T) {
	r := NewRelay(false, nil)
	sm := NewStreamerManager(r, nil)

	streamer, err := sm.StartStreamer("Cmd", "/dead", ".", false, "mp3", 64, false, nil, false, "", "", true, "", "printf track.mp3", 1)
	if err != nil {
		t.Fatalf("StartStreamer: %v", err)
	}
	streamer.Play()

	stream := r.GetOrCreateStream("/dead")
	stream.LastDataReceived = time.Now().Add(-time.Minute)

	var execs int32
	sm.recoveryExecSongCommand = func(*Streamer) (string, error) {
		atomic.AddInt32(&execs, 1)
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
			t.Fatal("expected recovery worker to exit after successful activation")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	if got := atomic.LoadInt32(&execs); got == 0 {
		t.Fatal("expected song_command to be retried at least once")
	}
}

func TestRecoverDeadSongCommandMountExitsWhenMountRecoversNaturally(t *testing.T) {
	r := NewRelay(false, nil)
	sm := NewStreamerManager(r, nil)

	streamer, err := sm.StartStreamer("Cmd", "/dead", ".", false, "mp3", 64, false, nil, false, "", "", true, "", "printf track.mp3", 1)
	if err != nil {
		t.Fatalf("StartStreamer: %v", err)
	}
	streamer.Play()

	stream := r.GetOrCreateStream("/dead")
	stream.LastDataReceived = time.Now().Add(-time.Minute)

	blocked := make(chan struct{})
	sm.recoveryExecSongCommand = func(*Streamer) (string, error) {
		<-blocked
		return "", errors.New("should not reach activation")
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
```

- [ ] **Step 2: Run the recovery-worker tests to verify they fail**

Run: `go test ./relay -run 'TestRecoverDeadSongCommandMount(RetriesUntilActivationSucceeds|ExitsWhenMountRecoversNaturally)$'`

Expected: FAIL because the recovery worker does not yet retry, observe fresh data, or clear in-flight state.

- [ ] **Step 3: Add a stream helper for recent activity and implement the recovery worker**

```go
func (s *Stream) LastDataAt() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.LastDataReceived
}

func (sm *StreamerManager) mountHasFreshData(mount string) bool {
	stream, ok := sm.relay.GetStream(mount)
	if !ok {
		return false
	}
	last := stream.LastDataAt()
	return !last.IsZero() && time.Since(last) <= 5*time.Second
}

func (sm *StreamerManager) runDeadSongCommandRecovery(ctx context.Context, s *Streamer) {
	defer sm.clearDeadRecovery(s.OutputMount)

	bo := &backoff{base: 1 * time.Second, max: 60 * time.Second}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if !sm.streamerEligibleForDeadRecovery(s) || sm.mountHasFreshData(s.OutputMount) {
			return
		}

		path, err := sm.recoveryExecSongCommand(s)
		if err == nil {
			err = sm.recoveryActivatePath(ctx, sm, s, path)
		}
		if err == nil && sm.mountHasFreshData(s.OutputMount) {
			return
		}

		delay := bo.next()
		select {
		case <-ctx.Done():
			return
		case <-sm.recoveryAfter(delay):
		}
	}
}

func (sm *StreamerManager) activateRecoveredSongCommandPath(ctx context.Context, s *Streamer, path string) error {
	if err := sm.ensureOutputSession(ctx, s); err != nil {
		return err
	}
	_, err := sm.activateTrackSource(ctx, s, path, -1, -1)
	return err
}
```

- [ ] **Step 4: Cancel recovery on streamer removal and stop**

```go
func (sm *StreamerManager) StopStreamer(mount string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if cancel, ok := sm.deadRecovery[mount]; ok {
		cancel()
		delete(sm.deadRecovery, mount)
	}
	// existing stop logic...
}

func (sm *StreamerManager) RemoveStreamer(mount string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if cancel, ok := sm.deadRecovery[mount]; ok {
		cancel()
		delete(sm.deadRecovery, mount)
	}
	// existing remove logic...
}
```

- [ ] **Step 5: Run the relay recovery tests to verify they pass**

Run: `go test ./relay -run 'TestRecoverDeadSongCommandMount(SkipsMountsWithoutSongCommand|StartsOnlyOneWorker|RetriesUntilActivationSucceeds|ExitsWhenMountRecoversNaturally)$'`

Expected: PASS

- [ ] **Step 6: Run the focused relay regression set**

Run: `go test ./relay -run 'Test(AutoDJOutputSession|RecoverDeadSongCommandMount|HealthMonitorDetectsStateChange)'`

Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add relay/streamer.go relay/stream.go relay/streamer_recovery_test.go
git commit -m "feat: recover dead AutoDJ song-command mounts"
```

### Task 3: Wire Health Events Through The Server

**Files:**
- Modify: `server/server.go`
- Create: `server/stream_health_recovery_test.go`
- Modify: `relay/health_test.go`
- Test: `server/stream_health_recovery_test.go`
- Test: `relay/health_test.go`

- [ ] **Step 1: Write the failing server and health regression tests**

```go
package server

import (
	"testing"

	"github.com/DatanoiseTV/tinyice/relay"
)

func TestHandleStreamHealthEventTriggersDeadAutoDJRecovery(t *testing.T) {
	called := false
	s := &Server{}
	s.deadStreamRecovery = func(mount string) {
		called = true
		if mount != "/dead" {
			t.Fatalf("unexpected mount %q", mount)
		}
	}

	s.handleStreamHealthEvent(relay.StreamHealthEvent{
		Mount:     "/dead",
		NewStatus: relay.StatusDead,
	})

	if !called {
		t.Fatal("expected dead stream recovery hook to run")
	}
}

func TestHandleStreamHealthEventIgnoresNonDeadStates(t *testing.T) {
	called := false
	s := &Server{}
	s.deadStreamRecovery = func(string) { called = true }

	s.handleStreamHealthEvent(relay.StreamHealthEvent{
		Mount:     "/degraded",
		NewStatus: relay.StatusDegraded,
	})

	if called {
		t.Fatal("expected non-dead status to skip recovery")
	}
}
```

```go
func TestHealthMonitorEmitsDeadEvent(t *testing.T) {
	r := NewRelay(false, nil)
	s := r.GetOrCreateStream("/dead")
	s.LastDataReceived = time.Now().Add(-time.Minute)

	hm := NewHealthMonitor(r)
	var got *StreamHealthEvent
	hm.OnEvent(func(evt StreamHealthEvent) {
		if evt.NewStatus == StatusDead {
			got = &evt
		}
	})

	hm.check()

	if got == nil {
		t.Fatal("expected dead health event")
	}
}
```

- [ ] **Step 2: Run the targeted tests to verify they fail**

Run: `go test ./server -run 'TestHandleStreamHealthEvent(TriggersDeadAutoDJRecovery|IgnoresNonDeadStates)$'`

Expected: FAIL because `handleStreamHealthEvent` and the recovery hook do not exist.

Run: `go test ./relay -run 'TestHealthMonitorEmitsDeadEvent$'`

Expected: FAIL if the explicit dead-event regression is not yet present.

- [ ] **Step 3: Add the server helper and wire it into `NewServer`**

```go
type Server struct {
	// existing fields...
	deadStreamRecovery func(string)
}

func (s *Server) handleStreamHealthEvent(e relay.StreamHealthEvent) {
	logger.L.Infow("Stream health event",
		"mount", e.Mount,
		"old_status", e.OldStatus.String(),
		"new_status", e.NewStatus.String(),
	)
	if e.NewStatus == relay.StatusDead && s.deadStreamRecovery != nil {
		s.deadStreamRecovery(e.Mount)
	}
}
```

```go
srv := &Server{
	Config: cfg,
	Relay:  r,
	// existing fields...
}
srv.deadStreamRecovery = srv.StreamerM.RecoverDeadSongCommandMount
healthM.OnEvent(srv.handleStreamHealthEvent)
```

- [ ] **Step 4: Add the dead-event health regression and run targeted tests**

Run: `go test ./server -run 'TestHandleStreamHealthEvent(TriggersDeadAutoDJRecovery|IgnoresNonDeadStates)$'`

Expected: PASS

Run: `go test ./relay -run 'TestHealthMonitor(DetectsStateChange|EmitsDeadEvent)$'`

Expected: PASS

- [ ] **Step 5: Run the end-to-end verification set**

Run: `go test ./relay -run 'Test(AutoDJOutputSession|RecoverDeadSongCommandMount|HealthMonitor)'`

Expected: PASS

Run: `go test ./server -run 'TestHandleStreamHealthEvent'`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add server/server.go server/stream_health_recovery_test.go relay/health_test.go
git commit -m "feat: trigger AutoDJ dead-stream recovery from health events"
```

## Self-Review

### Spec coverage

- Trigger only on `StatusDead`: covered in Task 3 server helper and tests.
- Scope limited to AutoDJ mounts with `song_command`: covered in Task 1 entry-point tests and Task 2 eligibility checks.
- Reuse relay `backoff`: covered in Task 2 worker implementation.
- Prevent duplicate workers: covered in Task 1 duplicate-suppression test and manager bookkeeping.
- Exit on natural recovery / stop / removal: covered in Task 2 worker logic and cancellation step.
- Route recovery through existing AutoDJ activation path: covered in Task 2 `activateRecoveredSongCommandPath`.

### Placeholder scan

- No deferred implementation placeholders remain.
- Every code-changing step names the exact files and includes concrete function signatures or test code.

### Type consistency

- The plan consistently uses `RecoverDeadSongCommandMount`, `DeadRecoveryActive`, `handleStreamHealthEvent`, `mountHasFreshData`, and `activateRecoveredSongCommandPath`.
- The relay-side worker uses the existing package-local `backoff` type rather than inventing a second retry type.
