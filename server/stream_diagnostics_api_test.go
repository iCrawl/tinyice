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

func TestAPIGetStreamsLoadsPersistedDiagnosticsWithoutCurrentSnapshot(t *testing.T) {
	s := newTestServer(t)
	s.Config.Mounts["/offline"] = "hashed"
	s.sessions["sid-1"] = &session{User: s.Config.Users["admin"], CSRFToken: "csrf-ok"}
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

	var payload []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(payload) != 1 {
		t.Fatalf("expected one stream entry, got %d", len(payload))
	}
	if payload[0]["status"] != "dead" {
		t.Fatalf("expected dead status from persisted history, got %#v", payload[0]["status"])
	}
	if payload[0]["status_reason"] != "no data for 91s" {
		t.Fatalf("expected persisted reason field, got %#v", payload[0]["status_reason"])
	}
	history, ok := payload[0]["history"].([]any)
	if !ok {
		t.Fatalf("expected history array, got %#v", payload[0]["history"])
	}
	if len(history) != 1 {
		t.Fatalf("expected one persisted history entry, got %d", len(history))
	}
}

func TestAPIGetStreamsMarksRuntimeOwnedAutoDJMountAsRunningWithoutRemoteSourceIP(t *testing.T) {
	s := newTestServer(t)
	s.sessions["sid-1"] = &session{User: s.Config.Users["admin"], CSRFToken: "csrf-ok"}

	stream := s.Relay.GetOrCreateStream("/auto")
	s.RuntimeRegistry.GetOrCreate("/auto").Stream = stream
	s.RuntimeRegistry.AttachSource("/auto", relay.SourceAutoDJ, "default")

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
	if payload[0]["status"] != "running" {
		t.Fatalf("expected running status for autodj-owned stream, got %#v", payload[0]["status"])
	}
	if payload[0]["source_kind"] != "autodj" {
		t.Fatalf("expected autodj source kind, got %#v", payload[0]["source_kind"])
	}
	if payload[0]["source_label"] != "AutoDJ" {
		t.Fatalf("expected AutoDJ source label, got %#v", payload[0]["source_label"])
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

func TestAPIGetStreamDiagnosticsReturnsPersistedEntries(t *testing.T) {
	s := newTestServer(t)
	s.sessions["sid-1"] = &session{User: s.Config.Users["admin"], CSRFToken: "csrf-ok"}
	s.Relay.History.RecordDiagnostic(relay.DiagnosticUpdate{
		Mount:     "/persisted",
		Status:    relay.DiagnosticStatusDead,
		Class:     relay.DiagnosticClassHealthDead,
		Reason:    "no data for 91s",
		Actor:     relay.DiagnosticActorHealthMonitor,
		Timestamp: time.Now(),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/streams/diagnostics?mount=%2Fpersisted", nil)
	req.AddCookie(&http.Cookie{Name: "sid", Value: "sid-1"})
	rr := httptest.NewRecorder()

	s.apiGetStreamDiagnostics(rr, req)

	var payload []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(payload) != 1 {
		t.Fatalf("expected one persisted diagnostic entry, got %d", len(payload))
	}
	if payload[0]["reason"] != "no data for 91s" {
		t.Fatalf("expected persisted reason, got %#v", payload[0]["reason"])
	}
}

func TestAPIGetStreamDiagnosticsRejectsUnauthorizedMountAccess(t *testing.T) {
	s := newTestServer(t)
	s.Config.Users["dj"] = &config.User{
		Username: "dj",
		Role:     config.RoleAdmin,
		Mounts: map[string]string{
			"/owned": "hashed",
		},
	}
	s.sessions["sid-dj"] = &session{User: s.Config.Users["dj"], CSRFToken: "csrf-ok"}
	s.Relay.History.RecordDiagnostic(relay.DiagnosticUpdate{
		Mount:     "/other",
		Status:    relay.DiagnosticStatusDead,
		Class:     relay.DiagnosticClassHealthDead,
		Reason:    "no data for 91s",
		Actor:     relay.DiagnosticActorHealthMonitor,
		Timestamp: time.Now(),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/streams/diagnostics?mount=%2Fother", nil)
	req.AddCookie(&http.Cookie{Name: "sid", Value: "sid-dj"})
	rr := httptest.NewRecorder()

	s.apiGetStreamDiagnostics(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden for unauthorized mount, got %d: %s", rr.Code, rr.Body.String())
	}
}
