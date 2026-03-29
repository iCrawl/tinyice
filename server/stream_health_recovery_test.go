package server

import (
	"testing"

	"github.com/DatanoiseTV/tinyice/logger"
	"github.com/DatanoiseTV/tinyice/relay"
)

func init() {
	logger.Init("error", false, "")
}

func TestHandleStreamHealthEventTriggersDeadAutoDJRecovery(t *testing.T) {
	called := false
	s := &Server{}
	s.deadStreamRecovery = func(mount string) {
		called = true
		if mount != "/dead" {
			t.Fatalf("unexpected mount %q", mount)
		}
	}

	s.handleStreamHealthEvent(relay.StreamHealthEvent{
		Mount:     "/dead",
		NewStatus: relay.StatusDead,
	})

	if !called {
		t.Fatal("expected dead stream recovery hook to run")
	}
}

func TestHandleStreamHealthEventIgnoresNonDeadStates(t *testing.T) {
	called := false
	s := &Server{}
	s.deadStreamRecovery = func(string) { called = true }

	s.handleStreamHealthEvent(relay.StreamHealthEvent{
		Mount:     "/degraded",
		NewStatus: relay.StatusDegraded,
	})

	if called {
		t.Fatal("expected non-dead status to skip recovery")
	}
}
