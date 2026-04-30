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
	s := newListenerAPITestServer(t)
	s.Config.Mounts["/offline"] = "hashed"
	s.sessions["sid-1"] = &session{User: s.Config.Users["admin"]}
	s.Relay.Diagnostics.Record(relay.DiagnosticUpdate{
		Mount:     "/offline",
		Status:    relay.DiagnosticStatusDead,
		Class:     relay.DiagnosticClassHealthDead,
		Reason:    "no data for 91s",
		Actor:     relay.DiagnosticActorHealthMonitor,
		Timestamp: time.Now(),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/streams", nil)
	req.AddCookie(&http.Cookie{Name: "sid", Value: "sid-1"})
	rr := httptest.NewRecorder()

	s.apiGetStreams(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var payload []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	var offline map[string]any
	for _, stream := range payload {
		if stream["mount"] == "/offline" {
			offline = stream
			break
		}
	}
	if offline == nil {
		t.Fatalf("expected /offline stream entry, got %#v", payload)
	}
	if offline["status"] != "dead" {
		t.Fatalf("expected dead status, got %#v", offline["status"])
	}
	if offline["status_class"] != "health_dead" {
		t.Fatalf("expected health_dead class, got %#v", offline["status_class"])
	}
	if offline["status_reason"] != "no data for 91s" {
		t.Fatalf("expected reason field, got %#v", offline["status_reason"])
	}
	history, ok := offline["history"].([]any)
	if !ok || len(history) != 1 {
		t.Fatalf("expected one history entry, got %#v", offline["history"])
	}
}

func TestAPIGetStreamsLoadsPersistedDiagnosticsWithoutCurrentSnapshot(t *testing.T) {
	s := newListenerAPITestServer(t)
	hm, err := relay.NewHistoryManager(t.TempDir() + "/history.db")
	if err != nil {
		t.Fatalf("NewHistoryManager: %v", err)
	}
	s.Relay.History = hm
	s.Relay.Diagnostics = relay.NewDiagnosticsStoreWithHistory(10, hm)
	s.Config.Mounts["/offline"] = "hashed"
	s.sessions["sid-1"] = &session{User: s.Config.Users["admin"]}
	s.Relay.History.RecordDiagnostic(relay.DiagnosticUpdate{
		Mount:     "/offline",
		Status:    relay.DiagnosticStatusDead,
		Class:     relay.DiagnosticClassHealthDead,
		Reason:    "no data for 91s",
		Actor:     relay.DiagnosticActorHealthMonitor,
		Timestamp: time.Now(),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/streams", nil)
	req.AddCookie(&http.Cookie{Name: "sid", Value: "sid-1"})
	rr := httptest.NewRecorder()

	s.apiGetStreams(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var payload []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	var offline map[string]any
	for _, stream := range payload {
		if stream["mount"] == "/offline" {
			offline = stream
			break
		}
	}
	if offline == nil {
		t.Fatalf("expected /offline stream entry, got %#v", payload)
	}
	if offline["status"] != "dead" {
		t.Fatalf("expected dead status from persisted history, got %#v", offline["status"])
	}
	if offline["status_reason"] != "no data for 91s" {
		t.Fatalf("expected persisted reason field, got %#v", offline["status_reason"])
	}
	historyEntries, ok := offline["history"].([]any)
	if !ok || len(historyEntries) != 1 {
		t.Fatalf("expected one persisted history entry, got %#v", offline["history"])
	}
}

func TestAPIGetStreamDiagnosticsReturnsPersistedHistory(t *testing.T) {
	s := newListenerAPITestServer(t)
	hm, err := relay.NewHistoryManager(t.TempDir() + "/history.db")
	if err != nil {
		t.Fatalf("NewHistoryManager: %v", err)
	}
	s.Relay.History = hm
	s.Relay.Diagnostics = relay.NewDiagnosticsStoreWithHistory(10, hm)
	s.sessions["sid-1"] = &session{User: s.Config.Users["admin"]}
	s.Relay.Diagnostics.Record(relay.DiagnosticUpdate{
		Mount:     "/live",
		Status:    relay.DiagnosticStatusError,
		Class:     relay.DiagnosticClassSongCommandEmpty,
		Reason:    "song_command returned empty output",
		Actor:     relay.DiagnosticActorAutoDJ,
		Timestamp: time.Now(),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/streams/diagnostics?mount=/live", nil)
	req.AddCookie(&http.Cookie{Name: "sid", Value: "sid-1"})
	rr := httptest.NewRecorder()

	s.apiGetStreamDiagnostics(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var payload []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(payload) != 1 || payload[0]["class"] != "song_command_empty_output" {
		t.Fatalf("expected persisted diagnostic class, got %#v", payload)
	}
}

func TestAPIGetStreamsMarksRuntimeOwnedAutoDJMountAsRunningWithoutRemoteSourceIP(t *testing.T) {
	s := newListenerAPITestServer(t)
	s.sessions["sid-1"] = &session{User: s.Config.Users["admin"]}

	stream := s.Relay.GetOrCreateStream("/auto")
	s.RuntimeRegistry.GetOrCreate("/auto").Stream = stream
	s.RuntimeRegistry.AttachSource("/auto", relay.SourceAutoDJ, "default")

	req := httptest.NewRequest(http.MethodGet, "/api/streams", nil)
	req.AddCookie(&http.Cookie{Name: "sid", Value: "sid-1"})
	rr := httptest.NewRecorder()

	s.apiGetStreams(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var payload []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var auto map[string]any
	for _, stream := range payload {
		if stream["mount"] == "/auto" {
			auto = stream
			break
		}
	}
	if auto == nil {
		t.Fatalf("expected /auto stream entry, got %#v", payload)
	}
	if auto["status"] != "running" {
		t.Fatalf("expected running status for autodj-owned stream, got %#v", auto["status"])
	}
	if auto["source_kind"] != "autodj" {
		t.Fatalf("expected autodj source kind, got %#v", auto["source_kind"])
	}
	if auto["source_label"] != "AutoDJ" {
		t.Fatalf("expected AutoDJ source label, got %#v", auto["source_label"])
	}
}

func TestAPIGetStreamsIncludesConfiguredMountLimits(t *testing.T) {
	s := newListenerAPITestServer(t)
	s.Config.Mounts["/limited"] = "hashed"
	s.Config.AdvancedMounts = map[string]*config.MountSettings{
		"/limited": {
			BurstSize:    262144,
			MaxListeners: 12,
		},
	}
	s.sessions["sid-1"] = &session{User: s.Config.Users["admin"]}

	req := httptest.NewRequest(http.MethodGet, "/api/streams", nil)
	req.AddCookie(&http.Cookie{Name: "sid", Value: "sid-1"})
	rr := httptest.NewRecorder()

	s.apiGetStreams(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var payload []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	for _, stream := range payload {
		if stream["mount"] != "/limited" {
			continue
		}
		if stream["burst_size"] != float64(262144) {
			t.Fatalf("expected burst size, got %#v", stream["burst_size"])
		}
		if stream["max_listeners"] != float64(12) {
			t.Fatalf("expected max listeners, got %#v", stream["max_listeners"])
		}
		return
	}
	t.Fatalf("expected /limited stream entry, got %#v", payload)
}
