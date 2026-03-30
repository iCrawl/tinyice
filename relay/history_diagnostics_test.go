package relay

import (
	"path/filepath"
	"testing"
	"time"
)

func TestHistoryManagerRecordsAndPrunesDiagnostics(t *testing.T) {
	hm, err := NewHistoryManager(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatalf("NewHistoryManager: %v", err)
	}

	oldTimestamp := time.Now().Add(-91 * 24 * time.Hour)
	hm.RecordDiagnostic(DiagnosticUpdate{
		Mount:     "/live",
		Status:    DiagnosticStatusDead,
		Class:     DiagnosticClassHealthDead,
		Reason:    "old dead event",
		Actor:     DiagnosticActorHealthMonitor,
		Timestamp: oldTimestamp,
	})
	hm.RecordDiagnostic(DiagnosticUpdate{
		Mount:     "/live",
		Status:    DiagnosticStatusRecovering,
		Class:     DiagnosticClassRecoveryStarted,
		Reason:    "retrying after dead event",
		Actor:     DiagnosticActorAutoDJ,
		Timestamp: time.Now(),
	})

	entries := hm.GetDiagnostics("/live", 10)
	if len(entries) != 1 {
		t.Fatalf("expected old diagnostics to be pruned, got %d entries", len(entries))
	}
	if entries[0].Reason != "retrying after dead event" {
		t.Fatalf("expected newest diagnostic entry to remain, got %q", entries[0].Reason)
	}
}
