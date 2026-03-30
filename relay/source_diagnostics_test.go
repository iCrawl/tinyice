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
