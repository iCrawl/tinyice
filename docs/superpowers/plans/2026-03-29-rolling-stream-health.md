# Rolling Stream Health Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make displayed stream health recover based on recent conditions instead of permanently degrading from lifetime dropped-byte totals.

**Architecture:** Keep lifetime `BytesIn` and `BytesDropped` counters for metrics and observability, but add a small rolling accounting window on each `Stream` for recent input and dropped bytes. Compute snapshot `Health` from that rolling window plus the existing stall penalty so the UI and API reflect current stream quality while preserving long-term counters.

**Tech Stack:** Go, relay stream stats, existing HTTP/API handlers, Go tests

---

### Task 1: Cover health semantics with tests

**Files:**
- Modify: `relay/health_test.go`

- [ ] **Step 1: Write failing tests for recoverable rolling health**

```go
func TestSnapshotHealthUsesRollingWindowAndCanRecover(t *testing.T) {
	r := NewRelay(false, nil)
	s := r.GetOrCreateStream("/health")

	s.recordHealthInput(1000)
	s.recordHealthDrop(200)

	if got := s.Snapshot().Health; got >= 100 {
		t.Fatalf("expected degraded health after recent drop, got %.2f", got)
	}

	s.recordHealthInput(1000)
	s.recordHealthInput(1000)
	s.recordHealthInput(1000)

	if got := s.Snapshot().Health; got != 100 {
		t.Fatalf("expected health to recover to 100 after clean window, got %.2f", got)
	}
}
```

- [ ] **Step 2: Run the relay health tests to verify failure**

Run: `go test ./relay -run 'TestHealth(Status|MonitorDetectsStateChange|SnapshotHealthUsesRollingWindowAndCanRecover)' -v`
Expected: FAIL because rolling health helpers do not exist yet

### Task 2: Implement rolling-window health accounting

**Files:**
- Modify: `relay/stream.go`
- Modify: `relay/stats.go`
- Modify: `server/handlers_stream.go`

- [ ] **Step 1: Add a small fixed rolling health window to `Stream`**

```go
type healthBucket struct {
	ts      time.Time
	bytesIn int64
	dropped int64
}
```

- [ ] **Step 2: Record recent input and dropped bytes where counters already move**

```go
s.recordHealthInput(int64(len(data)))
stream.recordHealthDrop(next - offset)
```

- [ ] **Step 3: Compute snapshot health from the recent window**

```go
recentIn, recentDropped := s.healthTotalsLocked(now)
health := 100.0
total := recentIn + recentDropped
if total > 0 {
	health = (float64(recentIn) / float64(total)) * 100.0
}
```

- [ ] **Step 4: Keep lifetime metrics unchanged**

```go
atomic.AddInt64(&s.BytesIn, int64(len(data)))
atomic.AddInt64(&stream.BytesDropped, next-offset)
```

### Task 3: Verify the behavior end-to-end

**Files:**
- Modify: `relay/health_test.go`

- [ ] **Step 1: Run targeted relay tests**

Run: `go test ./relay -run 'TestHealth(Status|MonitorDetectsStateChange|SnapshotHealthUsesRollingWindowAndCanRecover)' -v`
Expected: PASS

- [ ] **Step 2: Run the full test suite**

Run: `go test ./...`
Expected: PASS
