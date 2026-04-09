# Mount Runtime Registry Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the unfinished audio-first pipeline direction with a smaller `MountRuntime` registry that cleanly owns per-mount lifecycle and metadata while keeping `Relay` and `Stream` as the live data path.

**Architecture:** The runtime remains audio-first. `Relay` continues to own buffering, listener fanout, and byte transport. A new `RuntimeRegistry` in `relay/` becomes the control-plane owner for mount metadata: source kind, tenant ownership, registered outputs, lifecycle timestamps, and compatibility snapshots. This gives the codebase a real ownership model without pretending it is a multi-track media graph.

**Tech Stack:** Go 1.25, net/http, existing `relay` package, SSE, HLS, WebRTC, AutoDJ, RTMP/SRT ingest already present in the repo

---

## Scope

**In scope:**
- audio-first per-mount control-plane registry
- mount ownership for Icecast, AutoDJ, WebRTC source, relay-pull, RTMP, and SRT
- output registration for HLS and any future audio-first outputs
- tenant association for live mounts
- API/admin compatibility using the existing `Relay` snapshot surface
- removal of misleading pipeline-manager claims from runtime docs

**Out of scope:**
- true multi-track pipelines
- video synchronization or presentation clocks
- replacing `Relay`/`Stream` transport internals
- rewriting public/admin SSE contracts again

---

## Design Summary

The replacement abstraction is deliberately smaller:

- `MountRuntime`
  - one logical mount
  - points at one live `*Stream`
  - records source type and source health owner
  - records tenant ID
  - tracks registered outputs
  - exposes timestamps and lifecycle state

- `RuntimeRegistry`
  - create/get/remove mount runtimes
  - attach/detach sources
  - attach/detach outputs
  - resolve stream by mount
  - provide runtime snapshots for future admin/debug APIs

This solves the practical problem the pipeline code was reaching for:
- one place to own mount lifecycle
- one place to associate tenants and outputs
- one place to hang future behavior

Without introducing:
- tracks as first-class runtime coordination
- unused ingest/output interface layers everywhere
- a fake control plane that the server never actually uses

---

## File Structure

### New runtime ownership layer

- Create: `relay/mount_runtime.go`
  - `MountRuntime`, `RuntimeRegistry`, source/output enums, registration methods
- Create: `relay/mount_runtime_test.go`
  - lifecycle, source attach/detach, output registration, snapshot tests

### Relay/server wiring

- Modify: `server/server.go`
  - add `RuntimeRegistry` to `Server`
- Modify: `server/handlers_stream.go`
  - register and clean up mount ownership on source connect/disconnect
- Modify: `server/handlers_hls.go`
  - use runtime registry for HLS registration metadata

### Internal source integration

- Modify: `relay/webrtc.go`
  - register WebRTC source mounts in the registry
- Modify: `relay/client.go`
  - register pulled relay mounts in the registry
- Modify: `relay/autodj_output_session.go`
  - register AutoDJ output mounts in the registry
- Modify: `relay/ingest_rtmp.go`
  - register RTMP source mounts in the registry
- Modify: `relay/ingest_srt.go`
  - register SRT source mounts in the registry

### Tenant ownership

- Modify: `relay/tenant.go`
  - track live mounts instead of fake pipeline ownership
- Modify: `server/middleware.go`
  - expose tenant to source registration paths
- Modify: `server/handlers_tenant.go`
  - keep usage endpoints consistent

### API compatibility and docs

- Modify: `server/handlers_api.go`
  - add compatibility helpers where admin APIs need runtime metadata
- Modify: `server/api_streams.go`
  - keep stream listings unchanged while optionally enriching with runtime data
- Modify: `ARCHITECTURE.md`
  - describe `RuntimeRegistry` + `Relay` split
- Modify: `docs/relay-pipeline-clarification.md`
  - replace pipeline-centric guidance with mount-runtime guidance
- Create: `docs/superpowers/specs/2026-03-30-mount-runtime-rollout-notes.md`
  - operational notes and deferred work

### Cleanup of misleading abstraction

- Modify: `relay/pipeline_manager.go`
  - keep only if still needed for tests/legacy experiments, otherwise mark deprecated
- Modify: `relay/pipeline.go`
  - keep `Track` if still useful for HLS, but stop presenting `Pipeline` as the practical direction

---

## Task 1: Introduce `MountRuntime` And `RuntimeRegistry`

**Files:**
- Create: `relay/mount_runtime.go`
- Create: `relay/mount_runtime_test.go`

- [ ] **Step 1: Write the failing registry tests**

```go
package relay

import "testing"

func TestRuntimeRegistryGetOrCreateMount(t *testing.T) {
	r := NewRelay(false, nil)
	reg := NewRuntimeRegistry(r)

	rt := reg.GetOrCreate("/live")
	if rt.Mount != "/live" {
		t.Fatalf("expected /live, got %q", rt.Mount)
	}
	if rt.Stream == nil {
		t.Fatal("expected runtime to resolve a backing stream")
	}
}

func TestRuntimeRegistryAttachSource(t *testing.T) {
	r := NewRelay(false, nil)
	reg := NewRuntimeRegistry(r)

	rt := reg.GetOrCreate("/live")
	reg.AttachSource("/live", SourceIcecast, "default")

	if rt.Source != SourceIcecast {
		t.Fatalf("expected source icecast, got %q", rt.Source)
	}
	if rt.TenantID != "default" {
		t.Fatalf("expected tenant default, got %q", rt.TenantID)
	}
}

func TestRuntimeRegistryRemoveMount(t *testing.T) {
	r := NewRelay(false, nil)
	reg := NewRuntimeRegistry(r)
	reg.GetOrCreate("/live")

	reg.Remove("/live")

	if _, ok := reg.Get("/live"); ok {
		t.Fatal("expected runtime to be removed")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./relay -run 'TestRuntimeRegistryGetOrCreateMount|TestRuntimeRegistryAttachSource|TestRuntimeRegistryRemoveMount'`
Expected: FAIL because `RuntimeRegistry` does not exist.

- [ ] **Step 3: Write minimal implementation**

```go
package relay

import (
	"sync"
	"time"
)

type MountSource string

const (
	SourceUnknown MountSource = "unknown"
	SourceIcecast MountSource = "icecast"
	SourceAutoDJ  MountSource = "autodj"
	SourceRelay   MountSource = "relay"
	SourceWebRTC  MountSource = "webrtc"
	SourceRTMP    MountSource = "rtmp"
	SourceSRT     MountSource = "srt"
)

type MountOutput string

const (
	OutputHLS MountOutput = "hls"
)

type MountRuntime struct {
	Mount      string
	Stream     *Stream
	TenantID   string
	Source     MountSource
	Outputs    map[MountOutput]time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
	LastActive time.Time
}

type RuntimeRegistry struct {
	relay    *Relay
	runtimes map[string]*MountRuntime
	mu       sync.RWMutex
}

func NewRuntimeRegistry(r *Relay) *RuntimeRegistry {
	return &RuntimeRegistry{
		relay:    r,
		runtimes: make(map[string]*MountRuntime),
	}
}

func (rr *RuntimeRegistry) GetOrCreate(mount string) *MountRuntime {
	rr.mu.Lock()
	defer rr.mu.Unlock()

	if rt, ok := rr.runtimes[mount]; ok {
		return rt
	}

	now := time.Now()
	rt := &MountRuntime{
		Mount:      mount,
		Stream:     rr.relay.GetOrCreateStream(mount),
		Source:     SourceUnknown,
		Outputs:    make(map[MountOutput]time.Time),
		CreatedAt:  now,
		UpdatedAt:  now,
		LastActive: now,
	}
	rr.runtimes[mount] = rt
	return rt
}

func (rr *RuntimeRegistry) Get(mount string) (*MountRuntime, bool) {
	rr.mu.RLock()
	defer rr.mu.RUnlock()
	rt, ok := rr.runtimes[mount]
	return rt, ok
}

func (rr *RuntimeRegistry) AttachSource(mount string, src MountSource, tenantID string) {
	rt := rr.GetOrCreate(mount)
	rr.mu.Lock()
	defer rr.mu.Unlock()
	rt.Source = src
	rt.TenantID = tenantID
	rt.UpdatedAt = time.Now()
	rt.LastActive = rt.UpdatedAt
}

func (rr *RuntimeRegistry) Remove(mount string) {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	delete(rr.runtimes, mount)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./relay -run 'TestRuntimeRegistryGetOrCreateMount|TestRuntimeRegistryAttachSource|TestRuntimeRegistryRemoveMount'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add relay/mount_runtime.go relay/mount_runtime_test.go
git commit -m "feat: add mount runtime registry"
```

## Task 2: Wire The Registry Into `Server`

**Files:**
- Modify: `server/server.go`
- Test: `server/review_regressions_test.go`

- [ ] **Step 1: Write the failing server wiring test**

```go
func TestNewServerInitializesRuntimeRegistry(t *testing.T) {
	s := newTestServer(t)
	if s.RuntimeRegistry == nil {
		t.Fatal("expected runtime registry to be initialized")
	}
	if s.RuntimeRegistry.GetOrCreate("/live").Stream != s.Relay.GetOrCreateStream("/live") {
		t.Fatal("expected runtime registry to wrap the live relay streams")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./server -run TestNewServerInitializesRuntimeRegistry`
Expected: FAIL because `Server` has no registry field.

- [ ] **Step 3: Add minimal wiring**

```go
type Server struct {
	// ...
	RuntimeRegistry *relay.RuntimeRegistry
}
```

```go
srv := &Server{
	// ...
	RuntimeRegistry: relay.NewRuntimeRegistry(r),
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./server -run TestNewServerInitializesRuntimeRegistry`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add server/server.go server/review_regressions_test.go
git commit -m "feat: wire mount runtime registry into server"
```

## Task 3: Register Icecast Source Mounts In The Registry

**Files:**
- Modify: `server/handlers_stream.go`
- Modify: `server/middleware.go`
- Test: `server/review_regressions_test.go`

- [ ] **Step 1: Write the failing Icecast registration tests**

```go
func TestHandleSourceRegistersIcecastMountRuntime(t *testing.T) {
	s := newTestServer(t)
	req := newAuthorizedSourceRequest(t, "/live")
	w := newHijackRecorder()

	go s.handleSource(w, req)

	assertEventually(t, func() bool {
		rt, ok := s.RuntimeRegistry.Get("/live")
		return ok && rt.Source == relay.SourceIcecast
	})
}

func TestHandleSourceRemovesRuntimeOnDisconnect(t *testing.T) {
	s := newTestServer(t)
	req := newAuthorizedSourceRequest(t, "/live")
	w := newHijackRecorderThatDisconnects()

	s.handleSource(w, req)

	if _, ok := s.RuntimeRegistry.Get("/live"); ok {
		t.Fatal("expected runtime to be removed when source disconnects")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./server -run 'TestHandleSourceRegistersIcecastMountRuntime|TestHandleSourceRemovesRuntimeOnDisconnect'`
Expected: FAIL because `handleSource` does not touch the registry.

- [ ] **Step 3: Register/deregister the mount around the existing source loop**

```go
tenantID := "default"
if tenant := TenantFromContext(r.Context()); tenant != nil {
	tenantID = tenant.ID
}

stream := s.Relay.GetOrCreateStream(mount)
rt := s.RuntimeRegistry.GetOrCreate(mount)
s.RuntimeRegistry.AttachSource(mount, relay.SourceIcecast, tenantID)
rt.Stream = stream
```

```go
defer s.RuntimeRegistry.Remove(mount)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./server -run 'TestHandleSourceRegistersIcecastMountRuntime|TestHandleSourceRemovesRuntimeOnDisconnect'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add server/handlers_stream.go server/middleware.go server/review_regressions_test.go
git commit -m "feat: register icecast source mounts in runtime registry"
```

## Task 4: Register Internal Audio Sources

**Files:**
- Modify: `relay/autodj_output_session.go`
- Modify: `relay/client.go`
- Modify: `relay/webrtc.go`
- Modify: `relay/ingest_rtmp.go`
- Modify: `relay/ingest_srt.go`
- Test: `relay/review_stream_lifecycle_test.go`

- [ ] **Step 1: Write the failing source ownership tests**

```go
func TestAutoDJOutputRegistersRuntime(t *testing.T) {
	r := NewRelay(false, nil)
	reg := NewRuntimeRegistry(r)
	stream := r.GetOrCreateStream("/autodj")

	rt := reg.GetOrCreate("/autodj")
	rt.Stream = stream
	reg.AttachSource("/autodj", SourceAutoDJ, "default")

	got, ok := reg.Get("/autodj")
	if !ok || got.Source != SourceAutoDJ {
		t.Fatal("expected autodj runtime registration")
	}
}

func TestRelayPullRegistersRuntime(t *testing.T) {
	r := NewRelay(false, nil)
	reg := NewRuntimeRegistry(r)
	reg.AttachSource("/relay", SourceRelay, "default")

	got, ok := reg.Get("/relay")
	if !ok || got.Source != SourceRelay {
		t.Fatal("expected relay runtime registration")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./relay -run 'TestAutoDJOutputRegistersRuntime|TestRelayPullRegistersRuntime'`
Expected: FAIL until real code paths use the registry.

- [ ] **Step 3: Thread `RuntimeRegistry` to source owners and register mounts**

```go
// server/server.go
srv.StreamerM = relay.NewStreamerManager(r, cfg)
srv.StreamerM.SetRuntimeRegistry(srv.RuntimeRegistry)
srv.RelayM.SetRuntimeRegistry(srv.RuntimeRegistry)
srv.WebRTCM.SetRuntimeRegistry(srv.RuntimeRegistry)
srv.RTMP.SetRuntimeRegistry(srv.RuntimeRegistry)
srv.SRT.SetRuntimeRegistry(srv.RuntimeRegistry)
```

```go
// examples in source owners
rr.AttachSource(mount, SourceAutoDJ, "default")
rr.AttachSource(mount, SourceRelay, "default")
rr.AttachSource(mount, SourceWebRTC, "default")
rr.AttachSource(mount, SourceRTMP, "default")
rr.AttachSource(mount, SourceSRT, "default")
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./relay -run 'TestAutoDJOutputRegistersRuntime|TestRelayPullRegistersRuntime|TestWebRTCCleanupRecordsSourceDisconnectDiagnostic'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add relay/autodj_output_session.go relay/client.go relay/webrtc.go relay/ingest_rtmp.go relay/ingest_srt.go relay/review_stream_lifecycle_test.go server/server.go
git commit -m "feat: register internal audio sources in runtime registry"
```

## Task 5: Register HLS As A Mount Output

**Files:**
- Modify: `server/handlers_hls.go`
- Modify: `relay/mount_runtime.go`
- Test: `server/review_regressions_test.go`

- [ ] **Step 1: Write the failing HLS output test**

```go
func TestRegisterHLSRegistersRuntimeOutput(t *testing.T) {
	s := newTestServer(t)
	stream := s.Relay.GetOrCreateStream("/live")
	s.RuntimeRegistry.GetOrCreate("/live").Stream = stream

	hls := s.RegisterHLS("/live")
	if hls == nil {
		t.Fatal("expected hls output")
	}

	rt, ok := s.RuntimeRegistry.Get("/live")
	if !ok {
		t.Fatal("expected runtime for /live")
	}
	if _, ok := rt.Outputs[relay.OutputHLS]; !ok {
		t.Fatal("expected hls output registration")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./server -run TestRegisterHLSRegistersRuntimeOutput`
Expected: FAIL because HLS registration does not update runtime metadata.

- [ ] **Step 3: Add output registration helpers and call them**

```go
func (rr *RuntimeRegistry) AttachOutput(mount string, output MountOutput) {
	rt := rr.GetOrCreate(mount)
	rr.mu.Lock()
	defer rr.mu.Unlock()
	rt.Outputs[output] = time.Now()
	rt.UpdatedAt = time.Now()
}

func (rr *RuntimeRegistry) DetachOutput(mount string, output MountOutput) {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	if rt, ok := rr.runtimes[mount]; ok {
		delete(rt.Outputs, output)
		rt.UpdatedAt = time.Now()
	}
}
```

```go
// server/handlers_hls.go
s.RuntimeRegistry.AttachOutput(mount, relay.OutputHLS)
```

```go
// server/handlers_hls.go
s.RuntimeRegistry.DetachOutput(mount, relay.OutputHLS)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./server -run 'TestRegisterHLSRegistersRuntimeOutput|TestRegisterHLSRejectsOpusStreams'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add relay/mount_runtime.go server/handlers_hls.go server/review_regressions_test.go
git commit -m "feat: register hls as a mount runtime output"
```

## Task 6: Replace Fake Tenant Pipeline Ownership With Live Mount Ownership

**Files:**
- Modify: `relay/tenant.go`
- Modify: `relay/tenant_test.go`
- Modify: `server/handlers_tenant.go`

- [ ] **Step 1: Write the failing tenant mount tests**

```go
func TestTenantAttachMountTracksLiveCount(t *testing.T) {
	tm := NewTenantManager()
	tenant := tm.GetOrCreateDefaultTenant()

	tenant.AttachMount("/live")
	if tenant.StreamCount() != 1 {
		t.Fatalf("expected 1 stream, got %d", tenant.StreamCount())
	}

	tenant.DetachMount("/live")
	if tenant.StreamCount() != 0 {
		t.Fatalf("expected 0 streams, got %d", tenant.StreamCount())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./relay -run TestTenantAttachMountTracksLiveCount`
Expected: FAIL because tenant ownership is still pipeline-based.

- [ ] **Step 3: Change `Tenant` to track live mounts, not pipelines**

```go
type Tenant struct {
	// ...
	mounts map[string]struct{}
}
```

```go
func (t *Tenant) AttachMount(mount string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.mounts[mount] = struct{}{}
}

func (t *Tenant) DetachMount(mount string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.mounts, mount)
}

func (t *Tenant) StreamCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.mounts)
}
```

- [ ] **Step 4: Update source registration paths to attach/detach tenant mounts**

```go
if tenant := s.TenantM.GetTenant(tenantID); tenant != nil {
	tenant.AttachMount(mount)
}
defer tenant.DetachMount(mount)
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./relay -run 'TestTenantAttachMountTracksLiveCount|TestTenantDefaultTenant'`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add relay/tenant.go relay/tenant_test.go server/handlers_tenant.go server/handlers_stream.go
git commit -m "refactor: derive tenant stream ownership from live mounts"
```

## Task 7: Keep Admin/API Compatibility While Enriching Runtime Metadata

**Files:**
- Modify: `server/handlers_api.go`
- Modify: `server/api_streams.go`
- Test: `server/review_regressions_test.go`

- [ ] **Step 1: Write the failing compatibility tests**

```go
func TestRuntimeRegistryDoesNotChangeRelaySnapshotShape(t *testing.T) {
	s := newTestServer(t)
	stream := s.Relay.GetOrCreateStream("/live")
	stream.SetCurrentSong("test", s.Relay)
	s.RuntimeRegistry.AttachSource("/live", relay.SourceIcecast, "default")

	stats := s.Relay.Snapshot()
	if len(stats) != 1 {
		t.Fatalf("expected 1 stream snapshot, got %d", len(stats))
	}
}

func TestAdminRuntimeMetadataCanBeReadWithoutChangingExistingEvents(t *testing.T) {
	s := newTestServer(t)
	s.RuntimeRegistry.AttachSource("/live", relay.SourceAutoDJ, "default")

	rt, ok := s.RuntimeRegistry.Get("/live")
	if !ok || rt.Source != relay.SourceAutoDJ {
		t.Fatal("expected runtime metadata for admin use")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./server -run 'TestRuntimeRegistryDoesNotChangeRelaySnapshotShape|TestAdminRuntimeMetadataCanBeReadWithoutChangingExistingEvents'`
Expected: FAIL if the integration has no runtime read path.

- [ ] **Step 3: Add read helpers without rewriting existing APIs**

```go
func (s *Server) runtimeForMount(mount string) (*relay.MountRuntime, bool) {
	if s.RuntimeRegistry == nil {
		return nil, false
	}
	return s.RuntimeRegistry.Get(mount)
}
```

```go
// use runtime data only for optional enrichment fields or admin-only diagnostics
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./server -run 'TestRuntimeRegistryDoesNotChangeRelaySnapshotShape|TestAdminRuntimeMetadataCanBeReadWithoutChangingExistingEvents'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add server/handlers_api.go server/api_streams.go server/review_regressions_test.go
git commit -m "refactor: add mount runtime read helpers for admin compatibility"
```

## Task 8: Downgrade Or Remove The Misleading Pipeline Direction

**Files:**
- Modify: `relay/pipeline.go`
- Modify: `relay/pipeline_manager.go`
- Modify: `ARCHITECTURE.md`
- Modify: `docs/relay-pipeline-clarification.md`
- Create: `docs/superpowers/specs/2026-03-30-mount-runtime-rollout-notes.md`

- [ ] **Step 1: Write the documentation regression checks**

Run: `rg -n "PipelineManager.*main runtime|pipeline-based runtime|multi-track runtime today" ARCHITECTURE.md docs/relay-pipeline-clarification.md relay/pipeline_manager.go`
Expected: matches current stale wording that must be removed.

- [ ] **Step 2: Rewrite the docs to the new truth**

```md
- `RuntimeRegistry` owns per-mount runtime metadata and lifecycle.
- `Relay` and `Stream` remain the live audio data plane.
- `Pipeline` remains experimental and is not the recommended runtime path for TinyIce's current audio-first design.
```

- [ ] **Step 3: Mark pipeline code as experimental only**

```go
// PipelineManager is retained for experimental work and tests.
// TinyIce's active audio runtime uses RuntimeRegistry + Relay instead.
```

- [ ] **Step 4: Verify docs are consistent**

Run: `rg -n "RuntimeRegistry|active audio runtime|experimental" ARCHITECTURE.md docs/relay-pipeline-clarification.md docs/superpowers/specs/2026-03-30-mount-runtime-rollout-notes.md relay/pipeline_manager.go`
Expected: all matches describe the same runtime story.

- [ ] **Step 5: Commit**

```bash
git add relay/pipeline.go relay/pipeline_manager.go ARCHITECTURE.md docs/relay-pipeline-clarification.md docs/superpowers/specs/2026-03-30-mount-runtime-rollout-notes.md
git commit -m "docs: clarify mount runtime registry as the audio-first direction"
```

---

## Verification Matrix

Run after each task that changes runtime behavior:

```bash
go test ./relay
go test ./server
go test -race ./server ./relay
node --test server/frontend/src/**/*.test.mjs
npm --prefix server/frontend run build
```

Run before calling the migration complete:

```bash
go test ./...
go test -race ./server ./relay
```

---

## Risk Notes

This plan is lower risk than finishing `PipelineManager`, but it still changes runtime ownership.

The main risk areas are:
- source disconnect cleanup
- tenant attach/detach bookkeeping
- HLS registration cleanup
- preserving `Relay` snapshot semantics for admin APIs

The design is intentionally conservative to reduce those risks:
- no feature flag required
- no listener data-path rewrite
- no multi-track synchronization
- no change to `Stream.Broadcast` or `serveStreamData`

---

## Deferred Follow-On Work

If this lands cleanly and proves useful, a later plan can decide whether to:

1. Add richer admin/runtime introspection from `RuntimeRegistry`
2. Introduce output-specific objects beyond HLS
3. Promote tenant ownership into quota enforcement
4. Delete the experimental pipeline files entirely
5. Revisit video support from a fresh design, not by reviving the old pipeline plan

Do not mix those into this implementation unless the plan is deliberately expanded.
