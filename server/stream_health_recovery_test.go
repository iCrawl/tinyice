package server

import (
	"testing"

	"github.com/DatanoiseTV/tinyice/relay"
)

func TestServerDeadHealthEventTriggersDeadStreamRecovery(t *testing.T) {
	s := newListenerAPITestServer(t)

	var gotMount string
	s.deadStreamRecovery = func(mount string) {
		gotMount = mount
	}

	s.handleStreamHealthEvent(relay.StreamHealthEvent{
		Mount:     "/auto",
		NewStatus: relay.StatusDead,
	})

	if gotMount != "/auto" {
		t.Fatalf("expected dead stream recovery for /auto, got %q", gotMount)
	}
}

func TestServerNonDeadHealthEventDoesNotTriggerDeadStreamRecovery(t *testing.T) {
	s := newListenerAPITestServer(t)

	called := false
	s.deadStreamRecovery = func(string) {
		called = true
	}

	s.handleStreamHealthEvent(relay.StreamHealthEvent{
		Mount:     "/auto",
		NewStatus: relay.StatusDegraded,
	})

	if called {
		t.Fatal("expected non-dead health event not to trigger recovery")
	}
}
