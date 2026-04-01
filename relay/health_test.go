package relay

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/DatanoiseTV/tinyice/logger"
)

func init() {
	logger.Init("error", false, "")
}

func TestHealthStatus(t *testing.T) {
	tests := []struct {
		name     string
		lastData time.Duration
		want     HealthStatus
	}{
		{"healthy - recent data", 1 * time.Second, StatusHealthy},
		{"degraded - 10s ago", 10 * time.Second, StatusDegraded},
		{"dead - 60s ago", 60 * time.Second, StatusDead},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateHealthStatus(time.Now().Add(-tt.lastData))
			if got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestHealthMonitorDetectsStateChange(t *testing.T) {
	r := NewRelay(false, nil)
	s := r.GetOrCreateStream("/test-health")
	s.SetLastDataAt(time.Now().Add(-10 * time.Second))

	hm := NewHealthMonitor(r)
	var gotEvent *StreamHealthEvent
	hm.OnEvent(func(e StreamHealthEvent) {
		gotEvent = &e
	})

	hm.check()

	if gotEvent == nil {
		t.Fatal("expected health event, got none")
	}
	if gotEvent.NewStatus != StatusDegraded {
		t.Fatalf("expected Degraded, got %v", gotEvent.NewStatus)
	}
}

func TestHealthMonitorEmitsDeadEvent(t *testing.T) {
	r := NewRelay(false, nil)
	s := r.GetOrCreateStream("/dead")
	s.SetLastDataAt(time.Now().Add(-time.Minute))

	hm := NewHealthMonitor(r)
	var gotEvent *StreamHealthEvent
	hm.OnEvent(func(e StreamHealthEvent) {
		if e.NewStatus == StatusDead {
			gotEvent = &e
		}
	})

	hm.check()

	if gotEvent == nil {
		t.Fatal("expected dead health event")
	}
}

func TestHealthMonitorWritesDeadDiagnosticReason(t *testing.T) {
	r := NewRelay(false, nil)
	s := r.GetOrCreateStream("/dead")
	s.SetLastDataAt(time.Now().Add(-time.Minute))

	hm := NewHealthMonitor(r)
	hm.check()

	current, ok := r.Diagnostics.Current("/dead")
	if !ok {
		t.Fatal("expected diagnostic for dead mount")
	}
	if current.Status != DiagnosticStatusDead {
		t.Fatalf("expected dead diagnostic, got %q", current.Status)
	}
	if current.Class != DiagnosticClassHealthDead {
		t.Fatalf("expected health_dead class, got %q", current.Class)
	}
	if current.Reason == "" {
		t.Fatal("expected dead diagnostic reason to be populated")
	}
}

func TestHealthMonitorMarksRecoveredMountRunningAgain(t *testing.T) {
	r := NewRelay(false, nil)
	s := r.GetOrCreateStream("/recover")
	s.SetLastDataAt(time.Now().Add(-time.Minute))

	hm := NewHealthMonitor(r)
	hm.check()

	s.SetLastDataAt(time.Now())
	hm.check()

	current, ok := r.Diagnostics.Current("/recover")
	if !ok {
		t.Fatal("expected recovered mount diagnostic")
	}
	if current.Status != DiagnosticStatusRunning {
		t.Fatalf("expected running status after recovery, got %q", current.Status)
	}
	if current.Class != DiagnosticClassHealthRecovered {
		t.Fatalf("expected health_recovered class, got %q", current.Class)
	}
}

func TestHealthMonitorSeedsRunningDiagnosticForInitiallyHealthyMount(t *testing.T) {
	history, err := NewHistoryManager(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatalf("history manager: %v", err)
	}

	r := NewRelay(false, history)
	s := r.GetOrCreateStream("/healthy")
	s.SetLastDataAt(time.Now())

	r.History.RecordDiagnostic(DiagnosticUpdate{
		Mount:     "/healthy",
		Status:    DiagnosticStatusDegraded,
		Class:     DiagnosticClassHealthDegraded,
		Reason:    "no data for 9s",
		Actor:     DiagnosticActorHealthMonitor,
		Timestamp: time.Now().Add(-time.Minute),
	})

	hm := NewHealthMonitor(r)
	hm.check()

	current, ok := r.Diagnostics.Current("/healthy")
	if !ok {
		t.Fatal("expected current diagnostic for healthy mount after initial check")
	}
	if current.Status != DiagnosticStatusRunning {
		t.Fatalf("expected running status after initial healthy check, got %q", current.Status)
	}
	if current.Class != DiagnosticClassHealthRecovered {
		t.Fatalf("expected health_recovered class after initial healthy check, got %q", current.Class)
	}
}

func TestSnapshotHealthUsesRollingWindowAndCanRecover(t *testing.T) {
	r := NewRelay(false, nil)
	s := r.GetOrCreateStream("/health-window")

	base := time.Unix(1_000, 0)
	s.Started = base
	s.SetLastDataAt(base)

	s.recordHealthInputAt(1000, base)
	s.recordHealthDropAt(200, base)

	degraded := s.snapshotAt(base)
	if degraded.Health >= 100 {
		t.Fatalf("expected degraded health after recent drop, got %.2f", degraded.Health)
	}

	recoveredAt := base.Add(healthWindow + time.Second)
	s.SetLastDataAt(recoveredAt)
	s.recordHealthInputAt(1000, recoveredAt)

	recovered := s.snapshotAt(recoveredAt)
	if recovered.Health != 100 {
		t.Fatalf("expected health to recover to 100 after stale drops age out, got %.2f", recovered.Health)
	}
	if recovered.BytesDropped != 200 {
		t.Fatalf("expected lifetime dropped bytes to remain separate from rolling health, got %d", recovered.BytesDropped)
	}
}
