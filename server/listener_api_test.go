package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DatanoiseTV/tinyice/relay"
)

func TestListenerRoutesRequireAuthAndAreRegistered(t *testing.T) {
	s := newTestServer(t)
	mux := http.NewServeMux()
	s.registerAPIRoutes(mux)

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
			t.Fatalf("%s %s: expected %d, got %d", tc.method, tc.path, tc.want, rr.Code)
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
	if !strings.Contains(rr.Body.String(), `"id":"http-1"`) {
		t.Fatalf("expected listener payload, got %s", rr.Body.String())
	}
}

func TestAPIMoveListenerQueuesHTTPMoveCommand(t *testing.T) {
	s := newTestServer(t)
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

func TestAPIMoveListenerRejectsWebRTCMoves(t *testing.T) {
	s := newTestServer(t)
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
