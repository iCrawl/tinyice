package relay

import (
	"context"
	"testing"
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
	if current.Class != DiagnosticClassSourceDisconnect {
		t.Fatalf("expected source disconnect class, got %q", current.Class)
	}
}
