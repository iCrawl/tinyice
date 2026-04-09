# Stream Diagnostics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add mount-scoped diagnostics with current status, reason, and short history, expose them in the API/admin UI, and emit structured logs for the same transitions.

**Architecture:** Introduce a shared in-memory diagnostics store in the `relay` package so both runtime components and server handlers can write mount-scoped state. Update diagnostics at the actual lifecycle decision points for health changes, AutoDJ failures/recovery, and source connect/disconnect events, then merge diagnostic snapshots into `/api/streams` and `/api/autodj` for the admin UI.

**Tech Stack:** Go, Preact/TypeScript, node:test, existing TinyIce relay/server packages

---

## File Map

- Create: `relay/diagnostics.go`
  - Shared mount diagnostics types, ring-buffer history, and snapshot helpers.
- Create: `relay/diagnostics_test.go`
  - Unit coverage for current snapshot replacement and bounded history retention.
- Modify: `relay/relay.go`
  - Own one diagnostics store per relay and initialize it in `NewRelay`.
- Modify: `relay/health.go`
  - Record degraded/dead health reasons into diagnostics when state changes.
- Modify: `relay/health_test.go`
  - Assert health events also write the expected diagnostic reason.
- Modify: `relay/streamer.go`
  - Record manual stop, automatic stop, playlist exhaustion, `song_command` failure, recovery start/success/failure, and running state transitions.
- Modify: `relay/streamer_recovery_test.go`
  - Extend recovery coverage to assert diagnostic transitions and reason selection.
- Modify: `server/handlers_stream.go`
  - Record Icecast/live source connect/disconnect reasons.
- Modify: `relay/client.go`
  - Record relay pull connection failure/disconnect/reconnect diagnostics.
- Modify: `relay/webrtc.go`
  - Record WebRTC source lifecycle diagnostics.
- Modify: `relay/ingest_rtmp.go`
  - Record RTMP source lifecycle diagnostics.
- Modify: `relay/ingest_srt.go`
  - Record SRT source lifecycle diagnostics.
- Create: `server/stream_diagnostics_api_test.go`
  - API-level regression tests for `/api/streams` and `/api/autodj`.
- Modify: `server/handlers_api_v2.go`
  - Merge relay snapshot data with diagnostic snapshot/history.
- Modify: `server/openapi.yaml`
  - Document the new response fields.
- Modify: `server/frontend/src/types.ts`
  - Add shared TS types for diagnostics/history.
- Modify: `server/frontend/src/pages/admin/Streams.tsx`
  - Render status badge, reason, and short recent history.
- Create: `server/frontend/src/pages/admin/Streams.test.mjs`
  - Source-based regression tests for the new UI fields.

## Task 1: Build The Shared Diagnostics Store

**Files:**
- Create: `relay/diagnostics.go`
- Create: `relay/diagnostics_test.go`
- Modify: `relay/relay.go`
- Test: `relay/diagnostics_test.go`

- [ ] **Step 1: Write the failing diagnostics store tests**

```go
package relay

import (
	"testing"
	"time"
)

func TestDiagnosticsStoreKeepsLatestCurrentState(t *testing.T) {
	store := NewDiagnosticsStore(10)
	now := time.Unix(1_700_000_000, 0)

	store.Record(DiagnosticUpdate{
		Mount:     "/live",
		Status:    DiagnosticStatusDegraded,
		Class:     DiagnosticClassHealthDegraded,
		Reason:    "no data for 8s",
		Actor:     DiagnosticActorHealthMonitor,
		Timestamp: now,
	})
	store.Record(DiagnosticUpdate{
		Mount:     "/live",
		Status:    DiagnosticStatusDead,
		Class:     DiagnosticClassHealthDead,
		Reason:    "no data for 34s",
		Actor:     DiagnosticActorHealthMonitor,
		Timestamp: now.Add(time.Second),
	})

	current, ok := store.Current("/live")
	if !ok {
		t.Fatal("expected current diagnostic snapshot")
	}
	if current.Status != DiagnosticStatusDead {
		t.Fatalf("expected dead status, got %q", current.Status)
	}
	if current.Reason != "no data for 34s" {
		t.Fatalf("expected latest reason, got %q", current.Reason)
	}
}

func TestDiagnosticsStoreBoundsHistoryPerMount(t *testing.T) {
	store := NewDiagnosticsStore(3)
	base := time.Unix(1_700_000_000, 0)

	for i := 0; i < 5; i++ {
		store.Record(DiagnosticUpdate{
			Mount:     "/live",
			Status:    DiagnosticStatusError,
			Class:     DiagnosticClassRecoveryFailed,
			Reason:    "retry failed",
			Actor:     DiagnosticActorAutoDJ,
			Timestamp: base.Add(time.Duration(i) * time.Second),
		})
	}

	current, ok := store.Current("/live")
	if !ok {
		t.Fatal("expected current diagnostic snapshot")
	}
	if len(current.History) != 3 {
		t.Fatalf("expected bounded history of 3, got %d", len(current.History))
	}
	if current.History[0].Timestamp != base.Add(2*time.Second) {
		t.Fatalf("expected oldest retained entry to be the third update, got %v", current.History[0].Timestamp)
	}
}
```

- [ ] **Step 2: Run the relay diagnostics tests and verify they fail**

Run: `go test ./relay -run 'TestDiagnosticsStore(KeepsLatestCurrentState|BoundsHistoryPerMount)$'`

Expected: FAIL with missing identifiers such as `NewDiagnosticsStore`, `DiagnosticUpdate`, or `DiagnosticStatusDead`.

- [ ] **Step 3: Implement the diagnostics store and wire it into Relay**

```go
package relay

import (
	"sync"
	"time"
)

type DiagnosticStatus string
type DiagnosticClass string
type DiagnosticActor string

const (
	DiagnosticStatusRunning    DiagnosticStatus = "running"
	DiagnosticStatusDegraded   DiagnosticStatus = "degraded"
	DiagnosticStatusDead       DiagnosticStatus = "dead"
	DiagnosticStatusRecovering DiagnosticStatus = "recovering"
	DiagnosticStatusStopped    DiagnosticStatus = "stopped"
	DiagnosticStatusError      DiagnosticStatus = "error"
)

const (
	DiagnosticClassManualStop         DiagnosticClass = "manual_stop"
	DiagnosticClassAutomaticStop      DiagnosticClass = "automatic_stop"
	DiagnosticClassHealthDegraded     DiagnosticClass = "health_degraded"
	DiagnosticClassHealthDead         DiagnosticClass = "health_dead"
	DiagnosticClassSongCommandFailure DiagnosticClass = "song_command_failure"
	DiagnosticClassSongCommandEmpty   DiagnosticClass = "song_command_empty_output"
	DiagnosticClassSongCommandInvalid DiagnosticClass = "song_command_invalid_file"
	DiagnosticClassPlaylistExhausted  DiagnosticClass = "playlist_exhausted"
	DiagnosticClassRecoveryStarted    DiagnosticClass = "recovery_started"
	DiagnosticClassRecoverySucceeded  DiagnosticClass = "recovery_succeeded"
	DiagnosticClassRecoveryFailed     DiagnosticClass = "recovery_failed"
	DiagnosticClassSourceDisconnect   DiagnosticClass = "source_disconnect"
	DiagnosticClassStartupFailure     DiagnosticClass = "startup_failure"
)

const (
	DiagnosticActorAdmin         DiagnosticActor = "admin"
	DiagnosticActorHealthMonitor DiagnosticActor = "health_monitor"
	DiagnosticActorAutoDJ        DiagnosticActor = "autodj"
	DiagnosticActorRelay         DiagnosticActor = "relay"
	DiagnosticActorIcecastSource DiagnosticActor = "icecast_source"
	DiagnosticActorWebRTC        DiagnosticActor = "webrtc"
	DiagnosticActorRTMP          DiagnosticActor = "rtmp"
	DiagnosticActorSRT           DiagnosticActor = "srt"
	DiagnosticActorSystem        DiagnosticActor = "system"
)

type DiagnosticEntry struct {
	Timestamp time.Time         `json:"timestamp"`
	Status    DiagnosticStatus  `json:"status"`
	Class     DiagnosticClass   `json:"class"`
	Reason    string            `json:"reason"`
	Error     string            `json:"error,omitempty"`
	Actor     DiagnosticActor   `json:"actor"`
	Details   map[string]string `json:"details,omitempty"`
}

type MountDiagnostic struct {
	Mount              string            `json:"mount"`
	Status             DiagnosticStatus  `json:"status"`
	Class              DiagnosticClass   `json:"class"`
	Reason             string            `json:"reason"`
	Error              string            `json:"error,omitempty"`
	Actor              DiagnosticActor   `json:"actor"`
	UpdatedAt          time.Time         `json:"updated_at"`
	LastRecoveryAt     time.Time         `json:"last_recovery_at,omitempty"`
	LastRecoveryResult string            `json:"last_recovery_result,omitempty"`
	History            []DiagnosticEntry `json:"history"`
}

type DiagnosticUpdate struct {
	Mount              string
	Status             DiagnosticStatus
	Class              DiagnosticClass
	Reason             string
	Error              string
	Actor              DiagnosticActor
	Timestamp          time.Time
	Details            map[string]string
	LastRecoveryAt     time.Time
	LastRecoveryResult string
}

type DiagnosticsStore struct {
	mu           sync.RWMutex
	historyLimit int
	mounts       map[string]*MountDiagnostic
}

func NewDiagnosticsStore(historyLimit int) *DiagnosticsStore {
	if historyLimit <= 0 {
		historyLimit = 10
	}
	return &DiagnosticsStore{
		historyLimit: historyLimit,
		mounts:       make(map[string]*MountDiagnostic),
	}
}
```

```go
func (d *DiagnosticsStore) Record(update DiagnosticUpdate) {
	if update.Mount == "" {
		return
	}
	if update.Timestamp.IsZero() {
		update.Timestamp = time.Now()
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	current, ok := d.mounts[update.Mount]
	if !ok {
		current = &MountDiagnostic{Mount: update.Mount}
		d.mounts[update.Mount] = current
	}

	entry := DiagnosticEntry{
		Timestamp: update.Timestamp,
		Status:    update.Status,
		Class:     update.Class,
		Reason:    update.Reason,
		Error:     update.Error,
		Actor:     update.Actor,
		Details:   cloneDiagnosticDetails(update.Details),
	}

	current.Status = update.Status
	current.Class = update.Class
	current.Reason = update.Reason
	current.Error = update.Error
	current.Actor = update.Actor
	current.UpdatedAt = update.Timestamp
	if !update.LastRecoveryAt.IsZero() {
		current.LastRecoveryAt = update.LastRecoveryAt
	}
	if update.LastRecoveryResult != "" {
		current.LastRecoveryResult = update.LastRecoveryResult
	}
	current.History = append(current.History, entry)
	if len(current.History) > d.historyLimit {
		current.History = append([]DiagnosticEntry(nil), current.History[len(current.History)-d.historyLimit:]...)
	}
}

func (d *DiagnosticsStore) Current(mount string) (MountDiagnostic, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	current, ok := d.mounts[mount]
	if !ok {
		return MountDiagnostic{}, false
	}
	return cloneMountDiagnostic(*current), true
}
```

```go
type Relay struct {
	Streams      map[string]*Stream
	mu           sync.RWMutex
	LowLatency   bool
	BytesIn      int64
	BytesOut     int64
	History      *HistoryManager
	Diagnostics  *DiagnosticsStore
	metaSubs     []chan MetadataChange
	lastMeta     map[string]MetadataChange
	metaSubsMu   sync.Mutex
}

func NewRelay(lowLatency bool, history *HistoryManager) *Relay {
	return &Relay{
		Streams:      make(map[string]*Stream),
		LowLatency:   lowLatency,
		History:      history,
		Diagnostics:  NewDiagnosticsStore(10),
	}
}
```

- [ ] **Step 4: Run the relay diagnostics tests and verify they pass**

Run: `go test ./relay -run 'TestDiagnosticsStore(KeepsLatestCurrentState|BoundsHistoryPerMount)$'`

Expected: PASS

## Task 2: Record Health And AutoDJ Diagnostic Reasons

**Files:**
- Modify: `relay/health.go`
- Modify: `relay/health_test.go`
- Modify: `relay/streamer.go`
- Modify: `relay/streamer_recovery_test.go`
- Test: `relay/health_test.go`
- Test: `relay/streamer_recovery_test.go`

- [ ] **Step 1: Write failing health and AutoDJ diagnostic regression tests**

```go
func TestHealthMonitorWritesDeadDiagnosticReason(t *testing.T) {
	r := NewRelay(false, nil)
	s := r.GetOrCreateStream("/dead")
	s.LastDataReceived = time.Now().Add(-time.Minute)

	hm := NewHealthMonitor(r)
	hm.check()

	current, ok := r.Diagnostics.Current("/dead")
	if !ok {
		t.Fatal("expected diagnostic for dead mount")
	}
	if current.Status != DiagnosticStatusDead {
		t.Fatalf("expected dead diagnostic, got %q", current.Status)
	}
	if current.Class != DiagnosticClassHealthDead {
		t.Fatalf("expected health_dead class, got %q", current.Class)
	}
	if current.Reason == "" {
		t.Fatal("expected dead diagnostic reason to be populated")
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

	sm.recoveryExecSongCommand = func(*Streamer) (string, error) { return "/tmp/recovered.mp3", nil }
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
```

- [ ] **Step 2: Run the focused relay tests and verify they fail**

Run: `go test ./relay -run 'Test(HealthMonitorWritesDeadDiagnosticReason|RecoverDeadSongCommandMountRecordsRecoveryStartAndSuccess|NextTrackCandidateUsesPlaylistExhaustedOnlyWithoutSongCommand)$'`

Expected: FAIL because diagnostics are not yet written from health or AutoDJ transitions.

- [ ] **Step 3: Record health and AutoDJ transitions into diagnostics**

```go
func (hm *HealthMonitor) check() {
	// existing snapshot loop...
	if newStatus != oldStatus {
		reason := ""
		switch newStatus {
		case StatusDegraded:
			reason = formatHealthReason("no data for %s", time.Since(checkTime))
			hm.relay.Diagnostics.Record(DiagnosticUpdate{
				Mount:     ss.MountName,
				Status:    DiagnosticStatusDegraded,
				Class:     DiagnosticClassHealthDegraded,
				Reason:    reason,
				Actor:     DiagnosticActorHealthMonitor,
				Timestamp: time.Now(),
			})
		case StatusDead:
			reason = formatHealthReason("no data for %s", time.Since(checkTime))
			hm.relay.Diagnostics.Record(DiagnosticUpdate{
				Mount:     ss.MountName,
				Status:    DiagnosticStatusDead,
				Class:     DiagnosticClassHealthDead,
				Reason:    reason,
				Actor:     DiagnosticActorHealthMonitor,
				Timestamp: time.Now(),
			})
		}
	}
}
```

```go
func (s *Streamer) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.State = StateStopped
	s.manualStop = true
	if s.relay != nil && s.OutputMount != "" {
		s.relay.Diagnostics.Record(DiagnosticUpdate{
			Mount:     s.OutputMount,
			Status:    DiagnosticStatusStopped,
			Class:     DiagnosticClassManualStop,
			Reason:    "manual stop requested",
			Actor:     DiagnosticActorAdmin,
			Timestamp: time.Now(),
		})
	}
	if s.fileCancel != nil {
		s.fileCancel()
	}
	s.signalStateChange()
}
```

```go
if err != nil {
	s.relay.Diagnostics.Record(DiagnosticUpdate{
		Mount:     s.OutputMount,
		Status:    DiagnosticStatusError,
		Class:     classifySongCommandError(err),
		Reason:    songCommandReason(err),
		Error:     err.Error(),
		Actor:     DiagnosticActorAutoDJ,
		Timestamp: time.Now(),
	})
	logger.L.Warnw("AutoDJ song_command failed",
		"mount", s.OutputMount,
		"status", DiagnosticStatusError,
		"class", classifySongCommandError(err),
		"reason", songCommandReason(err),
		"error", err,
	)
}

if len(s.Playlist) == 0 && s.SongCommand == "" {
	s.relay.Diagnostics.Record(DiagnosticUpdate{
		Mount:     s.OutputMount,
		Status:    DiagnosticStatusStopped,
		Class:     DiagnosticClassPlaylistExhausted,
		Reason:    "no playable files remain and no song_command is configured",
		Actor:     DiagnosticActorAutoDJ,
		Timestamp: time.Now(),
	})
	return "", 0, 0, false
}
```

```go
func (sm *StreamerManager) runDeadSongCommandRecovery(ctx context.Context, s *Streamer) {
	s.relay.Diagnostics.Record(DiagnosticUpdate{
		Mount:     s.OutputMount,
		Status:    DiagnosticStatusRecovering,
		Class:     DiagnosticClassRecoveryStarted,
		Reason:    "retrying song_command after dead health event",
		Actor:     DiagnosticActorAutoDJ,
		Timestamp: time.Now(),
	})
	// existing recovery loop...
	if err == nil && sm.mountHasFreshData(s.OutputMount) {
		s.relay.Diagnostics.Record(DiagnosticUpdate{
			Mount:              s.OutputMount,
			Status:             DiagnosticStatusRunning,
			Class:              DiagnosticClassRecoverySucceeded,
			Reason:             "dead mount recovered and resumed playback",
			Actor:              DiagnosticActorAutoDJ,
			Timestamp:          time.Now(),
			LastRecoveryAt:     time.Now(),
			LastRecoveryResult: "success",
		})
		return
	}
}
```

- [ ] **Step 4: Run the focused relay tests and verify they pass**

Run: `go test ./relay -run 'Test(HealthMonitorWritesDeadDiagnosticReason|RecoverDeadSongCommandMountRecordsRecoveryStartAndSuccess|NextTrackCandidateUsesPlaylistExhaustedOnlyWithoutSongCommand)$'`

Expected: PASS

## Task 3: Record Source Connect And Disconnect Diagnostics

**Files:**
- Modify: `server/handlers_stream.go`
- Modify: `relay/client.go`
- Modify: `relay/webrtc.go`
- Modify: `relay/ingest_rtmp.go`
- Modify: `relay/ingest_srt.go`
- Create: `relay/source_diagnostics_test.go`
- Test: `relay/source_diagnostics_test.go`

- [ ] **Step 1: Write failing lifecycle diagnostics tests for non-AutoDJ sources**

```go
package relay

import (
	"context"
	"testing"
	"time"
)

func TestRelayPerformPullRecordsDisconnectDiagnosticOnFailure(t *testing.T) {
	r := NewRelay(false, nil)
	rm := NewRelayManager(r)
	inst := &RelayInstance{URL: "http://127.0.0.1:1/live", Mount: "/relay", Active: true}

	rm.performPull(context.Background(), inst)

	current, ok := r.Diagnostics.Current("/relay")
	if !ok {
		t.Fatal("expected relay diagnostic entry")
	}
	if current.Actor != DiagnosticActorRelay {
		t.Fatalf("expected relay actor, got %q", current.Actor)
	}
	if current.Status != DiagnosticStatusError {
		t.Fatalf("expected relay error status, got %q", current.Status)
	}
}

func TestWebRTCCleanupRecordsSourceDisconnectDiagnostic(t *testing.T) {
	r := NewRelay(false, nil)
	r.GetOrCreateStream("/webrtc")
	r.Diagnostics.Record(DiagnosticUpdate{
		Mount:     "/webrtc",
		Status:    DiagnosticStatusRunning,
		Class:     DiagnosticClassRecoverySucceeded,
		Reason:    "stream active",
		Actor:     DiagnosticActorWebRTC,
		Timestamp: time.Now(),
	})

	wm := NewWebRTCManager(r)
	wm.cleanupSource("/webrtc", nil)

	current, ok := r.Diagnostics.Current("/webrtc")
	if !ok {
		t.Fatal("expected webrtc diagnostic snapshot")
	}
	if current.Class != DiagnosticClassSourceDisconnect {
		t.Fatalf("expected source_disconnect class, got %q", current.Class)
	}
}
```

- [ ] **Step 2: Run the focused source diagnostics tests and verify they fail**

Run: `go test ./relay -run 'Test(RelayPerformPullRecordsDisconnectDiagnosticOnFailure|WebRTCCleanupRecordsSourceDisconnectDiagnostic)$'`

Expected: FAIL because the source lifecycle paths do not record diagnostics yet.

- [ ] **Step 3: Record source connect/disconnect transitions and emit structured logs**

```go
func recordSourceRunning(r *Relay, mount string, actor DiagnosticActor, reason string) {
	if r == nil {
		return
	}
	r.Diagnostics.Record(DiagnosticUpdate{
		Mount:     mount,
		Status:    DiagnosticStatusRunning,
		Class:     DiagnosticClassRecoverySucceeded,
		Reason:    reason,
		Actor:     actor,
		Timestamp: time.Now(),
	})
	logger.L.Infow("Stream diagnostic transition",
		"mount", mount,
		"status", DiagnosticStatusRunning,
		"class", DiagnosticClassRecoverySucceeded,
		"reason", reason,
		"actor", actor,
	)
}

func recordSourceDisconnect(r *Relay, mount string, actor DiagnosticActor, reason string, err error) {
	if r == nil {
		return
	}
	update := DiagnosticUpdate{
		Mount:     mount,
		Status:    DiagnosticStatusStopped,
		Class:     DiagnosticClassSourceDisconnect,
		Reason:    reason,
		Actor:     actor,
		Timestamp: time.Now(),
	}
	if err != nil {
		update.Error = err.Error()
	}
	r.Diagnostics.Record(update)
	logger.L.Warnw("Stream diagnostic transition",
		"mount", mount,
		"status", update.Status,
		"class", update.Class,
		"reason", update.Reason,
		"actor", update.Actor,
		"error", update.Error,
	)
}
```

```go
// server/handlers_stream.go
logger.L.Infow("Source connected", "mount", mount, "ip", r.RemoteAddr, "ua", r.Header.Get("User-Agent"))
s.Relay.Diagnostics.Record(DiagnosticUpdate{
	Mount:     mount,
	Status:    DiagnosticStatusRunning,
	Class:     DiagnosticClassRecoverySucceeded,
	Reason:    "source connected",
	Actor:     DiagnosticActorIcecastSource,
	Timestamp: time.Now(),
})

defer s.Relay.Diagnostics.Record(DiagnosticUpdate{
	Mount:     mount,
	Status:    DiagnosticStatusStopped,
	Class:     DiagnosticClassSourceDisconnect,
	Reason:    "source disconnected",
	Actor:     DiagnosticActorIcecastSource,
	Timestamp: time.Now(),
})
```

```go
// relay/client.go
if err != nil {
	rm.relay.Diagnostics.Record(DiagnosticUpdate{
		Mount:     inst.Mount,
		Status:    DiagnosticStatusError,
		Class:     DiagnosticClassSourceDisconnect,
		Reason:    "relay pull connection failed",
		Error:     err.Error(),
		Actor:     DiagnosticActorRelay,
		Timestamp: time.Now(),
	})
	return
}
```

- [ ] **Step 4: Run the focused source diagnostics tests and verify they pass**

Run: `go test ./relay -run 'Test(RelayPerformPullRecordsDisconnectDiagnosticOnFailure|WebRTCCleanupRecordsSourceDisconnectDiagnostic)$'`

Expected: PASS

## Task 4: Expose Diagnostics In The API

**Files:**
- Modify: `server/handlers_api_v2.go`
- Create: `server/stream_diagnostics_api_test.go`
- Modify: `server/openapi.yaml`
- Test: `server/stream_diagnostics_api_test.go`

- [ ] **Step 1: Write failing API regression tests**

```go
package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/relay"
)

func TestAPIGetStreamsIncludesDiagnosticsForOfflineConfiguredMount(t *testing.T) {
	s := newTestServer(t)
	s.Config.Mounts["/offline"] = "hashed"
	s.Relay.Diagnostics.Record(relay.DiagnosticUpdate{
		Mount:     "/offline",
		Status:    relay.DiagnosticStatusDead,
		Class:     relay.DiagnosticClassHealthDead,
		Reason:    "no data for 91s",
		Actor:     relay.DiagnosticActorHealthMonitor,
		Timestamp: time.Unix(1_700_000_000, 0),
	})
	s.sessions["sid-1"] = &session{User: s.Config.Users["admin"], CSRFToken: "csrf-ok"}

	req := httptest.NewRequest(http.MethodGet, "/api/streams", nil)
	req.AddCookie(&http.Cookie{Name: "sid", Value: "sid-1"})
	rr := httptest.NewRecorder()

	s.apiGetStreams(rr, req)

	var payload []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(payload) != 1 {
		t.Fatalf("expected one stream entry, got %d", len(payload))
	}
	if payload[0]["status"] != "dead" {
		t.Fatalf("expected dead status, got %#v", payload[0]["status"])
	}
	if payload[0]["status_reason"] != "no data for 91s" {
		t.Fatalf("expected reason field, got %#v", payload[0]["status_reason"])
	}
}

func TestAPIGetAutoDJIncludesDiagnosticHistory(t *testing.T) {
	s := newTestServer(t)
	s.sessions["sid-1"] = &session{User: s.Config.Users["admin"], CSRFToken: "csrf-ok"}
	s.Config.AutoDJs = []*config.AutoDJConfig{{
		Name:     "Auto",
		Mount:    "/auto",
		MusicDir: "/tmp",
		Format:   "mp3",
		Bitrate:  128,
		Enabled:  true,
	}}
	s.StreamerM = relay.NewStreamerManager(s.Relay, s.Config)
	s.Relay.Diagnostics.Record(relay.DiagnosticUpdate{
		Mount:     "/auto",
		Status:    relay.DiagnosticStatusRecovering,
		Class:     relay.DiagnosticClassRecoveryStarted,
		Reason:    "retrying song_command after dead health event",
		Actor:     relay.DiagnosticActorAutoDJ,
		Timestamp: time.Now(),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/autodj", nil)
	req.AddCookie(&http.Cookie{Name: "sid", Value: "sid-1"})
	rr := httptest.NewRecorder()

	s.apiGetAutoDJ(rr, req)

	var payload []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(payload) != 1 {
		t.Fatalf("expected one autodj entry, got %d", len(payload))
	}
	if payload[0]["status"] != "recovering" {
		t.Fatalf("expected recovering status, got %#v", payload[0]["status"])
	}
	history, ok := payload[0]["history"].([]any)
	if !ok {
		t.Fatalf("expected history array, got %#v", payload[0]["history"])
	}
	if len(history) == 0 {
		t.Fatal("expected autodj diagnostic history entries")
	}
}
```

- [ ] **Step 2: Run the focused server API tests and verify they fail**

Run: `go test ./server -run 'TestAPIGet(StreamsIncludesDiagnosticsForOfflineConfiguredMount|AutoDJIncludesDiagnosticHistory)$'`

Expected: FAIL because `/api/streams` and `/api/autodj` do not yet include diagnostic fields.

- [ ] **Step 3: Merge diagnostics into `/api/streams` and `/api/autodj` payloads**

```go
type diagnosticInfo struct {
	Status             string                   `json:"status"`
	StatusClass        string                   `json:"status_class"`
	StatusReason       string                   `json:"status_reason"`
	LastError          string                   `json:"last_error,omitempty"`
	StatusUpdatedAt    int64                    `json:"status_updated_at"`
	LastRecoveryAt     int64                    `json:"last_recovery_at,omitempty"`
	LastRecoveryResult string                   `json:"last_recovery_result,omitempty"`
	History            []relay.DiagnosticEntry  `json:"history"`
}

func diagnosticInfoFor(r *relay.Relay, mount string) diagnosticInfo {
	current, ok := r.Diagnostics.Current(mount)
	if !ok {
		return diagnosticInfo{History: []relay.DiagnosticEntry{}}
	}
	return diagnosticInfo{
		Status:             string(current.Status),
		StatusClass:        string(current.Class),
		StatusReason:       current.Reason,
		LastError:          current.Error,
		StatusUpdatedAt:    current.UpdatedAt.Unix(),
		LastRecoveryAt:     current.LastRecoveryAt.Unix(),
		LastRecoveryResult: current.LastRecoveryResult,
		History:            current.History,
	}
}
```

```go
type streamInfo struct {
	Mount              string                  `json:"mount"`
	ContentType        string                  `json:"content_type"`
	Bitrate            string                  `json:"bitrate"`
	Listeners          int                     `json:"listeners"`
	SourceIP           string                  `json:"source_ip"`
	Visible            bool                    `json:"visible"`
	Enabled            bool                    `json:"enabled"`
	Health             float64                 `json:"health"`
	Uptime             string                  `json:"uptime"`
	CurrentSong        string                  `json:"current_song"`
	Name               string                  `json:"name"`
	Status             string                  `json:"status"`
	StatusClass        string                  `json:"status_class"`
	StatusReason       string                  `json:"status_reason"`
	LastError          string                  `json:"last_error,omitempty"`
	StatusUpdatedAt    int64                   `json:"status_updated_at"`
	LastRecoveryAt     int64                   `json:"last_recovery_at,omitempty"`
	LastRecoveryResult string                  `json:"last_recovery_result,omitempty"`
	History            []relay.DiagnosticEntry `json:"history"`
}
```

```go
diag := diagnosticInfoFor(s.Relay, st.MountName)
result = append(result, streamInfo{
	Mount:              st.MountName,
	ContentType:        st.ContentType,
	Bitrate:            st.Bitrate,
	Listeners:          st.ListenersCount,
	SourceIP:           st.SourceIP,
	Visible:            st.Visible,
	Enabled:            st.Enabled,
	Health:             st.Health,
	Uptime:             st.Uptime,
	CurrentSong:        st.CurrentSong,
	Name:               st.Name,
	Status:             diag.Status,
	StatusClass:        diag.StatusClass,
	StatusReason:       diag.StatusReason,
	LastError:          diag.LastError,
	StatusUpdatedAt:    diag.StatusUpdatedAt,
	LastRecoveryAt:     diag.LastRecoveryAt,
	LastRecoveryResult: diag.LastRecoveryResult,
	History:            diag.History,
})
```

- [ ] **Step 4: Document the new API fields in OpenAPI**

```yaml
status:
  type: string
  example: dead
status_class:
  type: string
  example: health_dead
status_reason:
  type: string
  example: no data for 63s
last_error:
  type: string
  example: song command returned empty output
history:
  type: array
  items:
    type: object
    properties:
      timestamp: { type: string, format: date-time }
      status: { type: string }
      class: { type: string }
      reason: { type: string }
      error: { type: string }
      actor: { type: string }
```

- [ ] **Step 5: Run the focused server API tests and verify they pass**

Run: `go test ./server -run 'TestAPIGet(StreamsIncludesDiagnosticsForOfflineConfiguredMount|AutoDJIncludesDiagnosticHistory)$'`

Expected: PASS

## Task 5: Render Diagnostics In The Admin Streams UI

**Files:**
- Modify: `server/frontend/src/types.ts`
- Modify: `server/frontend/src/pages/admin/Streams.tsx`
- Create: `server/frontend/src/pages/admin/Streams.test.mjs`
- Test: `server/frontend/src/pages/admin/Streams.test.mjs`

- [ ] **Step 1: Write failing frontend source-based regression tests**

```js
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const streamsPath = fileURLToPath(new URL('./Streams.tsx', import.meta.url))
const typesPath = fileURLToPath(new URL('../../types.ts', import.meta.url))

test('Streams page renders explicit diagnostic fields', async () => {
  const source = await readFile(streamsPath, 'utf8')

  assert.match(source, /status_reason/, 'Streams page should render the latest status reason')
  assert.match(source, /history/, 'Streams page should render recent diagnostic history')
  assert.match(source, /Recovering|Stopped|Dead|Degraded|Running|Error/, 'Streams page should map status badges')
})

test('shared frontend types include diagnostics payload fields', async () => {
  const source = await readFile(typesPath, 'utf8')

  assert.match(source, /status:\s*string/, 'Stream types should include diagnostic status')
  assert.match(source, /status_reason:\s*string/, 'Stream types should include status reason')
  assert.match(source, /history:\s*DiagnosticHistoryEntry\[\]/, 'Stream types should include diagnostic history')
})
```

- [ ] **Step 2: Run the frontend tests and verify they fail**

Run: `node --test server/frontend/src/pages/admin/Streams.test.mjs`

Expected: FAIL because the UI and shared types do not yet include diagnostic fields.

- [ ] **Step 3: Add diagnostic types and render badges, reason, and short history**

```ts
export interface DiagnosticHistoryEntry {
  timestamp: string | number
  status: string
  class: string
  reason: string
  error?: string
  actor: string
}

export interface StreamEvent {
  mount: string
  title: string
  artist: string
  format: string
  bitrate: number
  listeners: number
  health: number
  status?: string
  status_reason?: string
}
```

```ts
interface Stream {
  mount: string
  source_ip: string
  content_type: string
  listeners: number
  enabled: boolean
  visible: boolean
  health: number
  bitrate: string
  current_song: string
  name: string
  status: string
  status_class: string
  status_reason: string
  last_error?: string
  history: DiagnosticHistoryEntry[]
}

function statusBadgeClass(status: string) {
  switch (status) {
    case 'running': return 'bg-live/15 text-live'
    case 'recovering': return 'bg-accent/15 text-accent'
    case 'degraded': return 'bg-yellow-500/15 text-yellow-400'
    case 'dead':
    case 'error': return 'bg-danger/15 text-danger'
    default: return 'bg-surface-overlay text-text-tertiary'
  }
}
```

```tsx
<td class="px-4 py-3.5">
  <div class="flex flex-col gap-1">
    <span class={`inline-flex items-center rounded-full px-2 py-1 font-mono text-[10px] uppercase ${statusBadgeClass(s.status)}`}>
      {s.status || (s.source_ip ? 'running' : 'stopped')}
    </span>
    <span class="text-xs text-text-secondary">
      {s.status_reason || (s.source_ip ? 'source connected' : 'no source')}
    </span>
    {s.history?.length > 0 && (
      <div class="text-[11px] text-text-tertiary">
        Recent: {s.history.slice(-3).reverse().map((entry) => entry.reason).join(' • ')}
      </div>
    )}
  </div>
</td>
```

- [ ] **Step 4: Run the frontend tests and the frontend build**

Run: `node --test server/frontend/src/pages/admin/Streams.test.mjs`

Expected: PASS

Run: `cd server/frontend && npm run build`

Expected: PASS

## Task 6: Full Verification

**Files:**
- Verify only: existing files from Tasks 1-5

- [ ] **Step 1: Run the targeted Go tests**

Run: `go test ./relay -run 'Test(DiagnosticsStore|HealthMonitorWritesDeadDiagnosticReason|RecoverDeadSongCommandMountRecordsRecoveryStartAndSuccess|NextTrackCandidateUsesPlaylistExhaustedOnlyWithoutSongCommand|RelayPerformPullRecordsDisconnectDiagnosticOnFailure|WebRTCCleanupRecordsSourceDisconnectDiagnostic)'`

Expected: PASS

- [ ] **Step 2: Run the targeted server tests**

Run: `go test ./server -run 'TestAPIGet(StreamsIncludesDiagnosticsForOfflineConfiguredMount|AutoDJIncludesDiagnosticHistory)$'`

Expected: PASS

- [ ] **Step 3: Run the frontend regression test**

Run: `node --test server/frontend/src/pages/admin/Streams.test.mjs`

Expected: PASS

- [ ] **Step 4: Run the full test suite**

Run: `go test ./...`

Expected: PASS

- [ ] **Step 5: Run the frontend build**

Run: `cd server/frontend && npm run build`

Expected: PASS
