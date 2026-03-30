package relay

import (
	"testing"
	"time"
)

func TestDiagnosticsStoreKeepsLatestCurrentState(t *testing.T) {
	store := NewDiagnosticsStore(10)
	now := time.Unix(1_700_000_000, 0)

	store.Record(DiagnosticUpdate{
		Mount:     "/live",
		Status:    DiagnosticStatusDegraded,
		Class:     DiagnosticClassHealthDegraded,
		Reason:    "no data for 8s",
		Actor:     DiagnosticActorHealthMonitor,
		Timestamp: now,
	})
	store.Record(DiagnosticUpdate{
		Mount:     "/live",
		Status:    DiagnosticStatusDead,
		Class:     DiagnosticClassHealthDead,
		Reason:    "no data for 34s",
		Actor:     DiagnosticActorHealthMonitor,
		Timestamp: now.Add(time.Second),
	})

	current, ok := store.Current("/live")
	if !ok {
		t.Fatal("expected current diagnostic snapshot")
	}
	if current.Status != DiagnosticStatusDead {
		t.Fatalf("expected dead status, got %q", current.Status)
	}
	if current.Reason != "no data for 34s" {
		t.Fatalf("expected latest reason, got %q", current.Reason)
	}
}

func TestDiagnosticsStoreBoundsHistoryPerMount(t *testing.T) {
	store := NewDiagnosticsStore(3)
	base := time.Unix(1_700_000_000, 0)

	for i := 0; i < 5; i++ {
		store.Record(DiagnosticUpdate{
			Mount:     "/live",
			Status:    DiagnosticStatusError,
			Class:     DiagnosticClassRecoveryFailed,
			Reason:    "retry failed",
			Actor:     DiagnosticActorAutoDJ,
			Timestamp: base.Add(time.Duration(i) * time.Second),
		})
	}

	current, ok := store.Current("/live")
	if !ok {
		t.Fatal("expected current diagnostic snapshot")
	}
	if len(current.History) != 3 {
		t.Fatalf("expected bounded history of 3, got %d", len(current.History))
	}
	if current.History[0].Timestamp != base.Add(2 * time.Second) {
		t.Fatalf("expected oldest retained entry to be the third update, got %v", current.History[0].Timestamp)
	}
}
