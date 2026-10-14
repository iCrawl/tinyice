package relay

import (
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
	s.LastDataReceived = time.Now().Add(-10 * time.Second)

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

func TestHealthMonitorRecordsDiagnosticsOnStateChange(t *testing.T) {
	r := NewRelay(false, nil)
	s := r.GetOrCreateStream("/diagnostic-health")
	s.LastDataReceived = time.Now().Add(-45 * time.Second)

	hm := NewHealthMonitor(r)
	hm.check()

	diag, ok := r.Diagnostics.Current("/diagnostic-health")
	if !ok {
		t.Fatal("expected health diagnostic")
	}
	if diag.Status != DiagnosticStatusDead {
		t.Fatalf("expected dead diagnostic, got %q", diag.Status)
	}
	if diag.Class != DiagnosticClassHealthDead {
		t.Fatalf("expected health_dead class, got %q", diag.Class)
	}
	if diag.Actor != DiagnosticActorHealthMonitor {
		t.Fatalf("expected health monitor actor, got %q", diag.Actor)
	}
	if diag.Reason == "" {
		t.Fatal("expected diagnostic reason")
	}
}

func TestHealthMonitorClearsStaleHealthDiagnosticWhenStreamIsFresh(t *testing.T) {
	r := NewRelay(false, nil)
	s := r.GetOrCreateStream("/fresh-again")
	s.LastDataReceived = time.Now()

	r.Diagnostics.Record(DiagnosticUpdate{
		Mount:  "/fresh-again",
		Status: DiagnosticStatusDegraded,
		Class:  DiagnosticClassHealthDegraded,
		Reason: "no data for 10s",
		Actor:  DiagnosticActorHealthMonitor,
	})

	hm := NewHealthMonitor(r)
	hm.check()

	diag, ok := r.Diagnostics.Current("/fresh-again")
	if !ok {
		t.Fatal("expected health diagnostic")
	}
	if diag.Status != DiagnosticStatusRunning {
		t.Fatalf("expected running diagnostic, got %q", diag.Status)
	}
	if diag.Class != DiagnosticClassHealthRecovered {
		t.Fatalf("expected health_recovered class, got %q", diag.Class)
	}
	if diag.Reason != "stream healthy again" {
		t.Fatalf("expected stream healthy again reason, got %q", diag.Reason)
	}
}
