package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/logger"
	"github.com/DatanoiseTV/tinyice/relay"
	"go.uber.org/zap"
)

func newListenerAPITestServer(t *testing.T) *Server {
	t.Helper()
	logger.Init("error", false, "")
	cfg := &config.Config{
		SetupComplete: true,
		Mounts:        map[string]string{"/live": "secret"},
		Users: map[string]*config.User{
			"admin": {
				Username: "admin",
				Role:     config.RoleSuperAdmin,
				Mounts:   map[string]string{"/live": "secret"},
			},
		},
	}
	return NewServer(cfg, zap.NewNop().Sugar(), "test", "test", "")
}

func TestHandleListenerRegistersAndUnregistersHTTPPlaybackClients(t *testing.T) {
	s := newListenerAPITestServer(t)
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

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("listener handler did not exit after cancellation")
	}

	if got := s.Relay.Listeners.CountForMount("/live"); got != 0 {
		t.Fatalf("expected listener to unregister on exit, got %d", got)
	}
}

func TestAPIDisconnectListenerStopsHTTPPlaybackClient(t *testing.T) {
	s := newListenerAPITestServer(t)
	s.sessions["sid-1"] = &session{User: s.Config.Users["admin"], CSRFToken: "csrf-ok"}
	stream := s.Relay.GetOrCreateStream("/live")
	stream.UpdateMetadata("Station", "Desc", "Genre", "", "128", "audio/mpeg", true, true)

	req := httptest.NewRequest(http.MethodGet, "/live", nil)
	req.RemoteAddr = "127.0.0.1:9002"
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleListener(rr, req)
	}()

	var listenerID string
	deadline := time.After(time.Second)
	for listenerID == "" {
		select {
		case <-deadline:
			t.Fatal("listener never registered")
		default:
			listeners := s.Relay.Listeners.List()
			if len(listeners) > 0 {
				listenerID = listeners[0].ID
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	disconnectReq := httptest.NewRequest(http.MethodPost, "/api/listeners/disconnect", strings.NewReader(`{"id":"`+listenerID+`"}`))
	disconnectReq.Header.Set("Content-Type", "application/json")
	disconnectReq.Header.Set("X-CSRF-Token", "csrf-ok")
	disconnectReq.AddCookie(&http.Cookie{Name: "sid", Value: "sid-1"})
	disconnectRR := httptest.NewRecorder()

	s.apiDisconnectListener(disconnectRR, disconnectReq)

	if disconnectRR.Code != http.StatusOK {
		t.Fatalf("expected disconnect 200, got %d: %s", disconnectRR.Code, disconnectRR.Body.String())
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("listener handler did not exit after disconnect")
	}
}

func TestAPIMoveListenerQueuesHTTPMoveCommand(t *testing.T) {
	s := newListenerAPITestServer(t)
	s.sessions["sid-1"] = &session{User: s.Config.Users["admin"], CSRFToken: "csrf-ok"}
	s.Relay.GetOrCreateStream("/backup")

	listener := &relay.Listener{
		ID:             "http-1",
		Protocol:       relay.ListenerProtocolHTTP,
		RequestedMount: "/live",
		CurrentMount:   "/live",
		Connected:      time.Unix(1_700_000_000, 0),
		DisconnectCh:   make(chan struct{}),
		MoveCh:         make(chan relay.ListenerCommand, 1),
	}
	s.Relay.Listeners.Register(listener)

	req := httptest.NewRequest(http.MethodPost, "/api/listeners/move", strings.NewReader(`{"id":"http-1","target_mount":"/backup"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", "csrf-ok")
	req.AddCookie(&http.Cookie{Name: "sid", Value: "sid-1"})
	rr := httptest.NewRecorder()

	s.apiMoveListener(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	select {
	case cmd := <-listener.MoveCh:
		if cmd.TargetMount != "/backup" {
			t.Fatalf("expected target /backup, got %q", cmd.TargetMount)
		}
	default:
		t.Fatal("expected move command to be queued")
	}
}

func TestAPIMoveListenerSwitchesHTTPPlaybackMount(t *testing.T) {
	s := newListenerAPITestServer(t)
	s.sessions["sid-1"] = &session{User: s.Config.Users["admin"], CSRFToken: "csrf-ok"}
	live := s.Relay.GetOrCreateStream("/live")
	live.UpdateMetadata("Live", "Desc", "Genre", "", "128", "audio/mpeg", true, true)
	backup := s.Relay.GetOrCreateStream("/backup")
	backup.UpdateMetadata("Backup", "Desc", "Genre", "", "128", "audio/mpeg", true, true)

	req := httptest.NewRequest(http.MethodGet, "/live", nil)
	req.RemoteAddr = "127.0.0.1:9010"
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleListener(rr, req)
	}()

	var listenerID string
	deadline := time.After(time.Second)
	for listenerID == "" {
		select {
		case <-deadline:
			t.Fatal("listener never registered")
		default:
			listeners := s.Relay.Listeners.List()
			if len(listeners) > 0 {
				listenerID = listeners[0].ID
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	moveReq := httptest.NewRequest(http.MethodPost, "/api/listeners/move", strings.NewReader(`{"id":"`+listenerID+`","target_mount":"/backup"}`))
	moveReq.Header.Set("Content-Type", "application/json")
	moveReq.Header.Set("X-CSRF-Token", "csrf-ok")
	moveReq.AddCookie(&http.Cookie{Name: "sid", Value: "sid-1"})
	moveRR := httptest.NewRecorder()

	s.apiMoveListener(moveRR, moveReq)
	if moveRR.Code != http.StatusOK {
		t.Fatalf("expected move 200, got %d: %s", moveRR.Code, moveRR.Body.String())
	}

	deadline = time.After(time.Second)
	for {
		snap, ok := s.Relay.Listeners.Get(listenerID)
		if ok && snap.CurrentMount == "/backup" && snap.RequestedMount == "/backup" {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("listener did not move to backup, got %#v", s.Relay.Listeners.List())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	_ = s.Relay.Listeners.Disconnect(listenerID)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("listener handler did not exit after cleanup")
	}
}

func TestAPIMoveListenerRejectsWebRTCMoves(t *testing.T) {
	s := newListenerAPITestServer(t)
	s.sessions["sid-1"] = &session{User: s.Config.Users["admin"], CSRFToken: "csrf-ok"}
	s.Relay.GetOrCreateStream("/backup")

	s.Relay.Listeners.Register(&relay.Listener{
		ID:             "webrtc-1",
		Protocol:       relay.ListenerProtocolWebRTC,
		RequestedMount: "/live",
		CurrentMount:   "/live",
		Connected:      time.Unix(1_700_000_000, 0),
		DisconnectCh:   make(chan struct{}),
		MoveCh:         make(chan relay.ListenerCommand, 1),
	})

	req := httptest.NewRequest(http.MethodPost, "/api/listeners/move", strings.NewReader(`{"id":"webrtc-1","target_mount":"/backup"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", "csrf-ok")
	req.AddCookie(&http.Cookie{Name: "sid", Value: "sid-1"})
	rr := httptest.NewRecorder()

	s.apiMoveListener(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rr.Code, rr.Body.String())
	}

	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["warning"] != "disconnect_required" {
		t.Fatalf("expected disconnect_required warning, got %#v", body)
	}
}

func TestHandleListenerEnforcesPerMountListenerLimit(t *testing.T) {
	s := newListenerAPITestServer(t)
	s.Config.AdvancedMounts = map[string]*config.MountSettings{
		"/live": {MaxListeners: 1},
	}
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
	req.RemoteAddr = "127.0.0.1:9003"
	rr := httptest.NewRecorder()

	s.handleListener(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when mount cap is reached, got %d", rr.Code)
	}
}

func TestListenerRoutesRequireAuthAndAreRegistered(t *testing.T) {
	s := newListenerAPITestServer(t)
	mux := s.setupRoutes()

	tests := []struct {
		method string
		path   string
		want   int
	}{
		{method: http.MethodGet, path: "/api/listeners", want: http.StatusUnauthorized},
		{method: http.MethodPost, path: "/api/listeners/disconnect", want: http.StatusUnauthorized},
		{method: http.MethodPost, path: "/api/listeners/move", want: http.StatusUnauthorized},
	}

	for _, tc := range tests {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
		if tc.method == http.MethodPost {
			req.Header.Set("Content-Type", "application/json")
		}
		rr := httptest.NewRecorder()

		mux.ServeHTTP(rr, req)

		if rr.Code != tc.want {
			t.Fatalf("%s %s: expected %d, got %d; body=%s", tc.method, tc.path, tc.want, rr.Code, rr.Body.String())
		}
	}
}

func TestAPIGetListenersReturnsPlaybackSnapshots(t *testing.T) {
	s := newListenerAPITestServer(t)
	s.sessions["sid-1"] = &session{User: s.Config.Users["admin"]}
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

	var body []relay.ListenerSnapshot
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode listener response: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("expected one listener, got %#v", body)
	}
	if body[0].ID != "http-1" || body[0].CurrentMount != "/live" || body[0].UserAgent != "tinyice-test" {
		t.Fatalf("unexpected listener payload: %#v", body[0])
	}
}
