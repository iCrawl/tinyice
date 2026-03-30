package relay

import (
	"testing"
	"time"
)

func TestListenerRegistryTracksMountMembership(t *testing.T) {
	reg := NewListenerRegistry()
	now := time.Unix(1_700_000_000, 0)

	l1 := &Listener{
		ID:           "http-1",
		Protocol:     ListenerProtocolHTTP,
		CurrentMount: "/live",
		Connected:    now,
	}
	l2 := &Listener{
		ID:           "webrtc-1",
		Protocol:     ListenerProtocolWebRTC,
		CurrentMount: "/live",
		Connected:    now.Add(time.Second),
	}

	reg.Register(l1)
	reg.Register(l2)

	if got := reg.CountForMount("/live"); got != 2 {
		t.Fatalf("expected 2 listeners on /live, got %d", got)
	}

	snapshots := reg.List()
	if len(snapshots) != 2 {
		t.Fatalf("expected 2 snapshots, got %d", len(snapshots))
	}
	if snapshots[0].ID != "webrtc-1" {
		t.Fatalf("expected newest listener first, got %#v", snapshots[0])
	}
}

func TestListenerRegistryMoveUpdatesMountIndexes(t *testing.T) {
	reg := NewListenerRegistry()
	l := &Listener{
		ID:           "http-1",
		Protocol:     ListenerProtocolHTTP,
		CurrentMount: "/a",
		Connected:    time.Unix(1_700_000_000, 0),
	}

	reg.Register(l)
	if err := reg.Move("http-1", "/b", time.Unix(1_700_000_010, 0)); err != nil {
		t.Fatalf("move listener: %v", err)
	}

	if got := reg.CountForMount("/a"); got != 0 {
		t.Fatalf("expected /a to be empty, got %d", got)
	}
	if got := reg.CountForMount("/b"); got != 1 {
		t.Fatalf("expected /b to have 1 listener, got %d", got)
	}

	snapshot, ok := reg.Get("http-1")
	if !ok {
		t.Fatal("expected moved listener snapshot")
	}
	if snapshot.CurrentMount != "/b" {
		t.Fatalf("expected current mount /b, got %q", snapshot.CurrentMount)
	}
}
