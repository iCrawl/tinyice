# Listener Registry Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a first-class playback listener registry, enforce per-mount listener caps, expose active listeners in the API/admin UI, support per-listener disconnect, and add HTTP listener move support with honest WebRTC limitations.

**Architecture:** Introduce a shared listener runtime model in the `relay` package so both HTTP playback and WebRTC playback can register themselves consistently. Keep stream-local membership for hot-path signaling, add a relay-level registry for global lookup and mount counts, then layer TinyIce-native listener APIs and a dedicated admin page on top of that runtime state.

**Tech Stack:** Go, Preact/TypeScript, node:test, existing TinyIce `relay` and `server` packages

**Workflow Constraint:** Stay on the current branch and do not create git commits while executing this plan unless the user changes that preference.

---

## File Map

- Create: `relay/listener_registry.go`
  - Listener protocol enums, listener runtime object, snapshots, registry methods, and move/disconnect control primitives.
- Create: `relay/listener_registry_test.go`
  - Unit coverage for register/list/unregister, mount counts, and move bookkeeping.
- Modify: `relay/relay.go`
  - Own one listener registry per relay and initialize it in `NewRelay`.
- Modify: `relay/stream.go`
  - Replace anonymous listener channels with listener objects or listener references and preserve broadcast signaling.
- Modify: `relay/stats.go`
  - Keep listener counts based on the new stream-local listener membership.
- Modify: `relay/webrtc.go`
  - Register playback listeners for WebRTC output and unregister them on connection close.
- Modify: `config/config.go`
  - Extend `MountSettings` with `MaxListeners`.
- Modify: `server/handlers_stream.go`
  - Register HTTP listeners, enforce effective mount-level caps, and add a control loop for per-listener move/disconnect.
- Create: `server/api_listeners.go`
  - TinyIce-native listener list, disconnect, and move endpoints.
- Create: `server/listener_api_test.go`
  - API-level coverage for list, disconnect, move, and WebRTC move limitation behavior.
- Modify: `server/routes.go`
  - Register `/api/listeners`, `/api/listeners/disconnect`, and `/api/listeners/move`.
- Modify: `server/routes_regressions_test.go`
  - Assert the new listener routes are wired.
- Modify: `server/api_streams.go`
  - Return per-mount cap fields and accept mount-level settings on create/update.
- Modify: `server/api_admin_support.go`
  - Leave global settings intact; no route changes, but keep `max_listeners` as the fallback setting.
- Modify: `server/review_regressions_test.go`
  - Add focused HTTP listener lifecycle tests around registration, cap enforcement, and move semantics.
- Modify: `server/openapi.yaml`
  - Document listener endpoints and per-mount cap fields.
- Modify: `server/frontend/src/types.ts`
  - Add listener snapshot and listener action payload types.
- Create: `server/frontend/src/pages/admin/Listeners.tsx`
  - Dedicated active-listener table with mount filter, disconnect, and move actions.
- Create: `server/frontend/src/pages/admin/Listeners.test.mjs`
  - Source-level regression tests for the new page and types.
- Modify: `server/frontend/src/pages/admin/AdminLayout.tsx`
  - Add the new listeners route.
- Modify: `server/frontend/src/components/Sidebar.tsx`
  - Add a navigation item for listeners.
- Modify: `server/frontend/src/pages/admin/Streams.tsx`
  - Show mount-level cap fields and add create/update controls for per-mount listener caps.
- Modify: `server/frontend/src/pages/admin/Streams.test.mjs`
  - Assert the cap fields and listener-management navigation are rendered.

## Task 1: Build The Shared Listener Registry

**Files:**
- Create: `relay/listener_registry.go`
- Create: `relay/listener_registry_test.go`
- Modify: `relay/relay.go`
- Test: `relay/listener_registry_test.go`

- [ ] **Step 1: Write the failing relay listener registry tests**

```go
package relay

import (
	"testing"
	"time"
)

func TestListenerRegistryTracksMountMembership(t *testing.T) {
	reg := NewListenerRegistry()
	now := time.Unix(1_700_000_000, 0)

	l1 := &Listener{
		ID:        "http-1",
		Protocol:  ListenerProtocolHTTP,
		Mount:     "/live",
		Connected: now,
	}
	l2 := &Listener{
		ID:        "webrtc-1",
		Protocol:  ListenerProtocolWebRTC,
		Mount:     "/live",
		Connected: now.Add(time.Second),
	}

	reg.Register(l1)
	reg.Register(l2)

	if got := reg.CountForMount("/live"); got != 2 {
		t.Fatalf("expected 2 listeners on /live, got %d", got)
	}

	snapshots := reg.List()
	if len(snapshots) != 2 {
		t.Fatalf("expected 2 snapshots, got %d", len(snapshots))
	}
	if snapshots[0].ID != "webrtc-1" {
		t.Fatalf("expected newest listener first, got %#v", snapshots[0])
	}
}

func TestListenerRegistryMoveUpdatesMountIndexes(t *testing.T) {
	reg := NewListenerRegistry()
	l := &Listener{
		ID:        "http-1",
		Protocol:  ListenerProtocolHTTP,
		Mount:     "/a",
		Connected: time.Unix(1_700_000_000, 0),
	}

	reg.Register(l)
	if err := reg.Move("http-1", "/b", time.Unix(1_700_000_010, 0)); err != nil {
		t.Fatalf("move listener: %v", err)
	}

	if got := reg.CountForMount("/a"); got != 0 {
		t.Fatalf("expected /a to be empty, got %d", got)
	}
	if got := reg.CountForMount("/b"); got != 1 {
		t.Fatalf("expected /b to have 1 listener, got %d", got)
	}

	snapshot, ok := reg.Get("http-1")
	if !ok {
		t.Fatal("expected moved listener snapshot")
	}
	if snapshot.CurrentMount != "/b" {
		t.Fatalf("expected current mount /b, got %q", snapshot.CurrentMount)
	}
}
```

- [ ] **Step 2: Run the relay listener registry tests to verify they fail**

Run: `go test ./relay -run 'TestListenerRegistry(TracksMountMembership|MoveUpdatesMountIndexes)$'`

Expected: FAIL with missing identifiers such as `NewListenerRegistry`, `Listener`, or `ListenerProtocolHTTP`.

- [ ] **Step 3: Implement the listener registry runtime model**

```go
package relay

import (
	"errors"
	"sort"
	"sync"
	"time"
)

type ListenerProtocol string

const (
	ListenerProtocolHTTP   ListenerProtocol = "http"
	ListenerProtocolWebRTC ListenerProtocol = "webrtc"
)

type ListenerCommand struct {
	TargetMount string
}

type Listener struct {
	ID                 string
	Protocol           ListenerProtocol
	RequestedMount     string
	CurrentMount       string
	RemoteAddr         string
	UserAgent          string
	Connected          time.Time
	LastStreamSwitchAt time.Time
	Signal             chan struct{}
	DisconnectCh       chan struct{}
	MoveCh             chan ListenerCommand
}

type ListenerSnapshot struct {
	ID                 string           `json:"id"`
	Protocol           ListenerProtocol `json:"protocol"`
	RequestedMount     string           `json:"requested_mount"`
	CurrentMount       string           `json:"current_mount"`
	RemoteAddr         string           `json:"remote_addr"`
	UserAgent          string           `json:"user_agent"`
	ConnectedAt        int64            `json:"connected_at"`
	DurationSeconds    int64            `json:"duration_seconds"`
	LastStreamSwitchAt int64            `json:"last_stream_switch_at"`
}

type ListenerRegistry struct {
	mu        sync.RWMutex
	listeners map[string]*Listener
	byMount   map[string]map[string]*Listener
}

func NewListenerRegistry() *ListenerRegistry {
	return &ListenerRegistry{
		listeners: make(map[string]*Listener),
		byMount:   make(map[string]map[string]*Listener),
	}
}

func (r *ListenerRegistry) Register(l *Listener) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.listeners[l.ID] = l
	if r.byMount[l.CurrentMount] == nil {
		r.byMount[l.CurrentMount] = make(map[string]*Listener)
	}
	r.byMount[l.CurrentMount][l.ID] = l
}

func (r *ListenerRegistry) Move(id, target string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	l, ok := r.listeners[id]
	if !ok {
		return errors.New("listener not found")
	}
	delete(r.byMount[l.CurrentMount], id)
	if r.byMount[target] == nil {
		r.byMount[target] = make(map[string]*Listener)
	}
	l.CurrentMount = target
	l.LastStreamSwitchAt = now
	r.byMount[target][id] = l
	return nil
}
```

- [ ] **Step 4: Wire the registry onto `Relay` and rerun the relay tests**

```go
type Relay struct {
	Streams     map[string]*Stream
	mu          sync.RWMutex
	LowLatency  bool
	BytesIn     int64
	BytesOut    int64
	History     *HistoryManager
	Diagnostics *DiagnosticsStore
	Listeners   *ListenerRegistry
}

func NewRelay(lowLatency bool, history *HistoryManager) *Relay {
	return &Relay{
		Streams:     make(map[string]*Stream),
		LowLatency:  lowLatency,
		History:     history,
		Diagnostics: NewDiagnosticsStoreWithHistory(10, history),
		Listeners:   NewListenerRegistry(),
	}
}
```

Run: `go test ./relay -run 'TestListenerRegistry(TracksMountMembership|MoveUpdatesMountIndexes)$'`

Expected: PASS

## Task 2: Migrate HTTP Listeners And Enforce Mount-Level Caps

**Files:**
- Modify: `config/config.go`
- Modify: `relay/stream.go`
- Modify: `relay/stats.go`
- Modify: `server/handlers_stream.go`
- Modify: `server/review_regressions_test.go`
- Test: `server/review_regressions_test.go`

- [ ] **Step 1: Write failing server tests for mount caps and HTTP registration**

```go
func TestHandleListenerRejectsWhenMountSpecificCapIsReached(t *testing.T) {
	s := newTestServer(t)
	s.Config.MaxListeners = 10
	s.Config.AdvancedMounts["/live"] = &config.MountSettings{MaxListeners: 1}

	stream := s.Relay.GetOrCreateStream("/live")
	stream.UpdateMetadata("Station", "Desc", "Genre", "", "128", "audio/mpeg", true, true)
	s.Relay.Listeners.Register(&relay.Listener{
		ID:             "existing",
		Protocol:       relay.ListenerProtocolHTTP,
		RequestedMount: "/live",
		CurrentMount:   "/live",
		Connected:      time.Now(),
	})

	req := httptest.NewRequest(http.MethodGet, "/live", nil)
	req.RemoteAddr = "127.0.0.1:9000"
	rr := httptest.NewRecorder()

	s.handleListener(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when mount cap is reached, got %d", rr.Code)
	}
}

func TestHandleListenerRegistersAndUnregistersHTTPPlaybackClients(t *testing.T) {
	s := newTestServer(t)
	stream := s.Relay.GetOrCreateStream("/live")
	stream.UpdateMetadata("Station", "Desc", "Genre", "", "128", "audio/mpeg", true, true)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/live", nil).WithContext(ctx)
	req.RemoteAddr = "127.0.0.1:9001"
	req.Header.Set("User-Agent", "tinyice-test")
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleListener(rr, req)
	}()

	deadline := time.After(time.Second)
	for s.Relay.Listeners.CountForMount("/live") == 0 {
		select {
		case <-deadline:
			t.Fatal("listener never registered")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	cancel()
	<-done

	if got := s.Relay.Listeners.CountForMount("/live"); got != 0 {
		t.Fatalf("expected listener to unregister on exit, got %d", got)
	}
}
```

- [ ] **Step 2: Run the targeted server tests and verify they fail**

Run: `go test ./server -run 'TestHandleListener(RejectsWhenMountSpecificCapIsReached|RegistersAndUnregistersHTTPPlaybackClients)$'`

Expected: FAIL because `MountSettings.MaxListeners` and the relay listener registry integration do not exist in the HTTP path.

- [ ] **Step 3: Extend mount settings and stream membership for real listener objects**

```go
type MountSettings struct {
	Password     string `json:"password"`
	BurstSize    int    `json:"burst_size"`
	MaxListeners int    `json:"max_listeners"`
}

type Stream struct {
	// ...
	listeners map[string]*Listener
}

func (s *Stream) SubscribeListener(l *Listener, burstSize int) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if l.Signal == nil {
		l.Signal = make(chan struct{}, 1)
	}
	s.listeners[l.ID] = l

	start := s.Buffer.Head - int64(burstSize)
	if start < 0 {
		start = 0
	}
	// keep existing Ogg alignment logic here
	if s.Buffer.Head-start > s.Buffer.Size {
		start = s.Buffer.Head - s.Buffer.Size
	}
	return start
}

func (s *Stream) Unsubscribe(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if l, ok := s.listeners[id]; ok {
		close(l.Signal)
		delete(s.listeners, id)
	}
}
```

- [ ] **Step 4: Register HTTP listeners in the serve loop and enforce effective mount caps**

```go
func (s *Server) effectiveMountListenerLimit(mount string) int {
	if ms, ok := s.Config.AdvancedMounts[mount]; ok && ms.MaxListeners > 0 {
		return ms.MaxListeners
	}
	return s.Config.MaxListeners
}

func (s *Server) newHTTPListener(r *http.Request, requestedMount, currentMount string) *relay.Listener {
	now := time.Now()
	return &relay.Listener{
		ID:                 fmt.Sprintf("http-%d", now.UnixNano()),
		Protocol:           relay.ListenerProtocolHTTP,
		RequestedMount:     requestedMount,
		CurrentMount:       currentMount,
		RemoteAddr:         r.RemoteAddr,
		UserAgent:          r.Header.Get("User-Agent"),
		Connected:          now,
		LastStreamSwitchAt: now,
		DisconnectCh:       make(chan struct{}),
		MoveCh:             make(chan relay.ListenerCommand, 1),
	}
}

limit := s.effectiveMountListenerLimit(mount)
if limit > 0 && s.Relay.Listeners.CountForMount(mount) >= limit {
	http.Error(w, "Server Full", http.StatusServiceUnavailable)
	return
}

l := s.newHTTPListener(r, originalMount, mount)
s.Relay.Listeners.Register(l)
defer s.Relay.Listeners.Unregister(l.ID)
offset := stream.SubscribeListener(l, 128*1024)
defer stream.Unsubscribe(l.ID)
```

- [ ] **Step 5: Rerun the server listener tests**

Run: `go test ./server -run 'TestHandleListener(RejectsWhenMountSpecificCapIsReached|RegistersAndUnregistersHTTPPlaybackClients)$'`

Expected: PASS

## Task 3: Register WebRTC Playback Clients And Add Listener APIs

**Files:**
- Modify: `relay/webrtc.go`
- Create: `server/api_listeners.go`
- Modify: `server/routes.go`
- Modify: `server/routes_regressions_test.go`
- Create: `server/listener_api_test.go`
- Test: `server/listener_api_test.go`

- [ ] **Step 1: Write failing API and route tests**

```go
func TestListenerRoutesRequireAuthAndAreRegistered(t *testing.T) {
	s := newTestServer(t)
	mux := http.NewServeMux()
	s.registerAPIRoutes(mux)

	tests := []struct {
		path string
		want int
	}{
		{path: "/api/listeners", want: http.StatusUnauthorized},
		{path: "/api/listeners/disconnect", want: http.StatusUnauthorized},
		{path: "/api/listeners/move", want: http.StatusUnauthorized},
	}

	for _, tc := range tests {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != tc.want {
			t.Fatalf("%s: expected %d, got %d", tc.path, tc.want, rr.Code)
		}
	}
}

func TestAPIGetListenersReturnsPlaybackSnapshots(t *testing.T) {
	s := newTestServer(t)
	s.sessions["sid-1"] = &session{User: s.Config.Users["admin"], CSRFToken: "csrf-ok"}
	s.Relay.Listeners.Register(&relay.Listener{
		ID:             "http-1",
		Protocol:       relay.ListenerProtocolHTTP,
		RequestedMount: "/live",
		CurrentMount:   "/live",
		RemoteAddr:     "127.0.0.1:9000",
		UserAgent:      "tinyice-test",
		Connected:      time.Unix(1_700_000_000, 0),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/listeners", nil)
	req.AddCookie(&http.Cookie{Name: "sid", Value: "sid-1"})
	rr := httptest.NewRecorder()

	s.apiGetListeners(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "\"id\":\"http-1\"") {
		t.Fatalf("expected listener payload, got %s", rr.Body.String())
	}
}
```

- [ ] **Step 2: Run the route and listener API tests to verify they fail**

Run: `go test ./server -run 'Test(ListenerRoutesRequireAuthAndAreRegistered|APIGetListenersReturnsPlaybackSnapshots)$'`

Expected: FAIL because the listener API handlers and routes do not exist yet.

- [ ] **Step 3: Register WebRTC playback listeners and add the listener API handlers**

```go
func (wm *WebRTCManager) registerPlaybackListener(mount string, offer webrtc.SessionDescription) (*Listener, *webrtc.PeerConnection, error) {
	pc, err := wm.api.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{{URLs: []string{"stun:stun.l.google.com:19302"}}},
	})
	if err != nil {
		return nil, nil, err
	}

	now := time.Now()
	l := &Listener{
		ID:                 fmt.Sprintf("webrtc-%d", now.UnixNano()),
		Protocol:           ListenerProtocolWebRTC,
		RequestedMount:     mount,
		CurrentMount:       mount,
		Connected:          now,
		LastStreamSwitchAt: now,
		DisconnectCh:       make(chan struct{}),
		MoveCh:             make(chan ListenerCommand, 1),
	}

	wm.relay.Listeners.Register(l)
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateClosed || state == webrtc.PeerConnectionStateFailed {
			wm.relay.Listeners.Unregister(l.ID)
		}
	})
	return l, pc, nil
}

func (s *Server) apiGetListeners(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.checkAuth(r); !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	jsonResponse(w, s.Relay.Listeners.List())
}

func (s *Server) apiDisconnectListener(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	var body struct{ ID string `json:"id"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if err := s.Relay.Listeners.Disconnect(body.ID); err != nil {
		jsonError(w, "Listener not found", http.StatusNotFound)
		return
	}
	jsonResponse(w, map[string]string{"status": "ok"})
}
```

- [ ] **Step 4: Wire the listener routes and rerun the API tests**

```go
mux.HandleFunc("/api/listeners", s.apiGetListeners)
mux.HandleFunc("/api/listeners/disconnect", s.apiDisconnectListener)
mux.HandleFunc("/api/listeners/move", s.apiMoveListener)
```

Run: `go test ./server -run 'Test(ListenerRoutesRequireAuthAndAreRegistered|APIGetListenersReturnsPlaybackSnapshots)$'`

Expected: PASS

## Task 4: Expose Mount Cap Settings And Build The Admin Listener UI

**Files:**
- Modify: `server/api_streams.go`
- Modify: `server/openapi.yaml`
- Modify: `server/frontend/src/types.ts`
- Create: `server/frontend/src/pages/admin/Listeners.tsx`
- Create: `server/frontend/src/pages/admin/Listeners.test.mjs`
- Modify: `server/frontend/src/pages/admin/AdminLayout.tsx`
- Modify: `server/frontend/src/components/Sidebar.tsx`
- Modify: `server/frontend/src/pages/admin/Streams.tsx`
- Modify: `server/frontend/src/pages/admin/Streams.test.mjs`
- Test: `server/frontend/src/pages/admin/Listeners.test.mjs`

- [ ] **Step 1: Write the failing frontend source tests**

```js
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const listenersPagePath = fileURLToPath(new URL('./Listeners.tsx', import.meta.url))
const typesPath = fileURLToPath(new URL('../../types.ts', import.meta.url))
const streamsPath = fileURLToPath(new URL('./Streams.tsx', import.meta.url))

test('listener admin page wires list, disconnect, and move actions', async () => {
  const source = await readFile(listenersPagePath, 'utf8')
  assert.match(source, /api\.get<ListenerInfo\[]>\\('\/api\/listeners'\\)/)
  assert.match(source, /\/api\/listeners\/disconnect/)
  assert.match(source, /\/api\/listeners\/move/)
})

test('shared types include listener snapshots and per-mount caps', async () => {
  const source = await readFile(typesPath, 'utf8')
  assert.match(source, /export interface ListenerInfo/)
  assert.match(source, /max_listeners:\s*number/)
})

test('streams page exposes per-mount max listener controls', async () => {
  const source = await readFile(streamsPath, 'utf8')
  assert.match(source, /max_listeners/)
})
```

- [ ] **Step 2: Run the frontend source tests and verify they fail**

Run: `node --test server/frontend/src/pages/admin/Listeners.test.mjs`

Expected: FAIL because `Listeners.tsx` does not exist and the shared types do not include listener snapshots.

- [ ] **Step 3: Extend the stream API payloads and frontend types**

```go
type streamInfo struct {
	Mount        string `json:"mount"`
	Bitrate      string `json:"bitrate"`
	Listeners    int    `json:"listeners"`
	MaxListeners int    `json:"max_listeners"`
	BurstSize    int    `json:"burst_size"`
	// existing fields...
}

ms := s.Config.AdvancedMounts[st.MountName]
if ms != nil {
	info.MaxListeners = ms.MaxListeners
	info.BurstSize = ms.BurstSize
}
```

```ts
export interface ListenerInfo {
  id: string
  protocol: 'http' | 'webrtc'
  requested_mount: string
  current_mount: string
  remote_addr: string
  user_agent: string
  connected_at: number
  duration_seconds: number
  last_stream_switch_at: number
}
```

- [ ] **Step 4: Add the dedicated listeners page and nav wiring**

```tsx
export function Listeners() {
  const listeners = signal<ListenerInfo[]>([])
  const filterMount = signal('')

  async function load() {
    listeners.value = await api.get<ListenerInfo[]>('/api/listeners')
  }

  async function disconnect(id: string) {
    await api.post('/api/listeners/disconnect', { id })
    await load()
  }

  async function move(id: string, target_mount: string) {
    await api.post('/api/listeners/move', { id, target_mount })
    await load()
  }

  useEffect(() => { load() }, [])
  // render table with filter + actions
}
```

```tsx
import { Listeners } from './Listeners'

<Route path="/admin/listeners" component={Listeners} />
```

- [ ] **Step 5: Run the frontend tests after the UI wiring is in place**

Run: `node --test server/frontend/src/pages/admin/Listeners.test.mjs server/frontend/src/pages/admin/Streams.test.mjs`

Expected: PASS

## Task 5: Implement HTTP Listener Move And Honest WebRTC Move Responses

**Files:**
- Modify: `relay/listener_registry.go`
- Modify: `server/handlers_stream.go`
- Modify: `server/api_listeners.go`
- Create: `server/listener_move_test.go`
- Test: `server/listener_move_test.go`

- [ ] **Step 1: Write failing listener move tests**

```go
func TestAPIMoveListenerUpdatesHTTPListenerMount(t *testing.T) {
	s := newTestServer(t)
	s.sessions["sid-1"] = &session{User: s.Config.Users["admin"], CSRFToken: "csrf-ok"}

	for _, mount := range []string{"/a", "/b"} {
		stream := s.Relay.GetOrCreateStream(mount)
		stream.UpdateMetadata("Station", "Desc", "Genre", "", "128", "audio/mpeg", true, true)
	}

	now := time.Unix(1_700_000_000, 0)
	l := &relay.Listener{
		ID:                 "http-1",
		Protocol:           relay.ListenerProtocolHTTP,
		RequestedMount:     "/a",
		CurrentMount:       "/a",
		Connected:          now,
		LastStreamSwitchAt: now,
		DisconnectCh:       make(chan struct{}),
		MoveCh:             make(chan relay.ListenerCommand, 1),
	}
	s.Relay.Listeners.Register(l)

	body := strings.NewReader(`{"id":"http-1","target_mount":"/b"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/listeners/move", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", "csrf-ok")
	req.AddCookie(&http.Cookie{Name: "sid", Value: "sid-1"})
	rr := httptest.NewRecorder()

	s.apiMoveListener(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if got := s.Relay.Listeners.CountForMount("/b"); got != 1 {
		t.Fatalf("expected moved listener on /b, got %d", got)
	}
}

func TestAPIMoveListenerRejectsUnsupportedWebRTCMove(t *testing.T) {
	s := newTestServer(t)
	s.sessions["sid-1"] = &session{User: s.Config.Users["admin"], CSRFToken: "csrf-ok"}
	s.Relay.Listeners.Register(&relay.Listener{
		ID:           "webrtc-1",
		Protocol:     relay.ListenerProtocolWebRTC,
		CurrentMount: "/live",
		Connected:    time.Unix(1_700_000_000, 0),
	})

	body := strings.NewReader(`{"id":"webrtc-1","target_mount":"/other"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/listeners/move", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", "csrf-ok")
	req.AddCookie(&http.Cookie{Name: "sid", Value: "sid-1"})
	rr := httptest.NewRecorder()

	s.apiMoveListener(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 for unsupported seamless WebRTC move, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "disconnect_required") {
		t.Fatalf("expected disconnect_required warning, got %s", rr.Body.String())
	}
}
```

- [ ] **Step 2: Run the move tests and verify they fail**

Run: `go test ./server -run 'TestAPIMoveListener(UpdatesHTTPListenerMount|RejectsUnsupportedWebRTCMove)$'`

Expected: FAIL because `/api/listeners/move` does not coordinate with the HTTP serve loop yet.

- [ ] **Step 3: Teach the HTTP serve loop to react to move and disconnect commands**

```go
for {
	select {
	case <-s.done:
		return false
	case <-r.Context().Done():
		return false
	case <-listener.DisconnectCh:
		return false
	case cmd := <-listener.MoveCh:
		stream.Unsubscribe(listener.ID)
		if err := s.Relay.Listeners.Move(listener.ID, cmd.TargetMount, time.Now()); err != nil {
			return false
		}
		currentMount = cmd.TargetMount
		if currentMount == originalMount {
			listener.CurrentMount = originalMount
		}
		return true
	case _, ok := <-listener.Signal:
		if !ok {
			return true
		}
		// existing read/write path
	}
}
```

- [ ] **Step 4: Implement the move API with mount validation and a structured WebRTC limitation**

```go
func (s *Server) apiMoveListener(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var body struct {
		ID         string `json:"id"`
		TargetMount string `json:"target_mount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	l, ok := s.Relay.Listeners.Listener(body.ID)
	if !ok {
		jsonError(w, "Listener not found", http.StatusNotFound)
		return
	}
	if _, ok := s.Relay.GetStream(body.TargetMount); !ok {
		jsonError(w, "Target mount not found", http.StatusNotFound)
		return
	}
	if l.Protocol == relay.ListenerProtocolWebRTC {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "error",
			"warning": "disconnect_required",
		})
		return
	}
	l.MoveCh <- relay.ListenerCommand{TargetMount: body.TargetMount}
	jsonResponse(w, map[string]string{"status": "ok", "current_mount": body.TargetMount})
}
```

- [ ] **Step 5: Run the move tests and the full package tests**

Run: `go test ./server -run 'TestAPIMoveListener(UpdatesHTTPListenerMount|RejectsUnsupportedWebRTCMove)$'`

Expected: PASS

Run: `go test ./...`

Expected: PASS across `relay` and `server`

## Self-Review Checklist

- Spec coverage:
  - listener registry: Task 1
  - mount caps: Task 2 and Task 4
  - list/disconnect APIs: Task 3
  - admin UI: Task 4
  - HTTP move: Task 5
  - honest WebRTC limitation: Task 5
- Placeholder scan:
  - no `TODO` or `TBD`
  - no “write tests later” placeholders
- Type consistency:
  - `ListenerProtocolHTTP` and `ListenerProtocolWebRTC` are used consistently
  - `target_mount` is the API field name everywhere
  - `MaxListeners` in Go maps to `max_listeners` in JSON and TypeScript

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-03-30-listener-registry.md`.

Two execution options:

1. Subagent-Driven (recommended) - I dispatch a fresh subagent per task, review between tasks, fast iteration
2. Inline Execution - Execute tasks in this session using executing-plans, batch execution with checkpoints

Which approach?

