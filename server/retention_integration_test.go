package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/relay"
)

func TestRetentionFallbackUpdatesRegistry(t *testing.T) {
	s := newListenerAPITestServer(t)
	s.Config.FallbackMounts = map[string]string{"/live": "/backup"}
	live := s.Relay.GetOrCreateStream("/live")
	backup := s.Relay.GetOrCreateStream("/backup")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest(http.MethodGet, "/live", nil).WithContext(ctx)
	r.RemoteAddr = "127.0.0.1:8181"
	done := make(chan struct{})
	go func() { defer close(done); s.handleListener(httptest.NewRecorder(), r) }()
	wait := func(condition func() bool) {
		deadline := time.After(time.Second)
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for !condition() {
			select {
			case <-deadline:
				t.Fatal("listener did not reach expected stream subscription")
			case <-tick.C:
			}
		}
	}
	wait(func() bool { return live.ListenersCount() == 1 })
	s.Relay.RemoveStream("/live")
	wait(func() bool { return backup.ListenersCount() == 1 })
	listeners := s.Relay.Listeners.List()
	if len(listeners) != 1 || listeners[0].CurrentMount != "/backup" {
		t.Errorf("listener receiving backup still registered as %#v", listeners)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("listener did not close")
	}
}

func TestRetentionPerMountCapCreateAndUpdate(t *testing.T) {
	s, sid := hardeningServer(t, config.RoleSuperAdmin)
	s.Config.AdvancedMounts = map[string]*config.MountSettings{}
	create := withSession(httptest.NewRequest(http.MethodPost, "/api/streams", strings.NewReader(`{"mount":"/mine","password":"secret","max_listeners":7}`)), sid)
	create.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.apiCreateStream(w, create)
	if w.Code != http.StatusOK {
		t.Fatalf("create failed: %d %s", w.Code, w.Body.String())
	}
	if got := mountMaxListeners(s.Config, "/mine"); got != 7 {
		t.Errorf("create max_listeners = %d, want 7", got)
	}
	update := withSession(httptest.NewRequest(http.MethodPatch, "/api/streams", strings.NewReader(`{"mount":"/mine","max_listeners":9}`)), sid)
	update.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	s.apiUpdateStream(w, update)
	if w.Code != http.StatusOK {
		t.Fatalf("update failed: %d %s", w.Code, w.Body.String())
	}
	if got := mountMaxListeners(s.Config, "/mine"); got != 9 {
		t.Errorf("update max_listeners = %d, want 9", got)
	}
}

func TestRetentionMoveToFullMount(t *testing.T) {
	s, sid := hardeningServer(t, config.RoleSuperAdmin)
	s.Config.AdvancedMounts = map[string]*config.MountSettings{"/full": {MaxListeners: 1}}
	s.Relay.GetOrCreateStream("/full")
	s.Relay.Listeners.Register(&relay.Listener{ID: "full", RequestedMount: "/full", Connected: time.Now()})
	from := &relay.Listener{ID: "from", RequestedMount: "/other", Connected: time.Now(), MoveCh: make(chan relay.ListenerCommand, 1)}
	s.Relay.Listeners.Register(from)
	r := withSession(httptest.NewRequest(http.MethodPost, "/api/listeners/move", strings.NewReader(`{"id":"from","target_mount":"/full"}`)), sid)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.apiMoveListener(w, r)
	if w.Code != http.StatusConflict {
		t.Errorf("move to full mount returned %d, want 409", w.Code)
	}
	select {
	case <-from.MoveCh:
		t.Error("move to full mount queued")
	default:
	}
}
