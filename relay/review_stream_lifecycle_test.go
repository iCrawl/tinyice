package relay

import (
	"sync/atomic"
	"testing"

	"github.com/pion/webrtc/v4"
)

func TestSubscribeSafeRejectsConcurrentCloseWindow(t *testing.T) {
	r := NewRelay(false, nil)
	s := r.GetOrCreateStream("/race")

	s.mu.Lock()
	done := make(chan struct{})

	go func() {
		defer close(done)
		_, _, _ = s.SubscribeSafe("listener-race", 1024)
	}()

	atomic.StoreInt32(&s.closed, 1)
	s.mu.Unlock()
	<-done

	if got := s.ListenersCount(); got != 0 {
		t.Fatalf("expected no listeners after close race, got %d", got)
	}
}

func TestCleanupSourceRemovesRelayStream(t *testing.T) {
	r := NewRelay(false, nil)
	r.GetOrCreateStream("/webrtc")

	wm := NewWebRTCManager(r)
	pc, err := wm.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("new peer connection: %v", err)
	}
	defer pc.Close()

	wm.sources["/webrtc"] = pc
	wm.cleanupSource("/webrtc", pc)

	if _, ok := wm.sources["/webrtc"]; ok {
		t.Fatal("expected cleanupSource to remove source entry")
	}
	if _, ok := r.GetStream("/webrtc"); ok {
		t.Fatal("expected cleanupSource to remove relay stream")
	}
}

func TestWebRTCIngestStopRemovesRelayStream(t *testing.T) {
	r := NewRelay(false, nil)
	stream := r.GetOrCreateStream("/mount")
	wm := NewWebRTCManager(r)

	pc, err := wm.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("new peer connection: %v", err)
	}
	defer pc.Close()
	wm.sources["/mount"] = pc

	source := NewWebRTCIngestSource("/mount", stream, wm)
	source.Stop()

	if _, ok := r.GetStream("/mount"); ok {
		t.Fatal("expected Stop to remove relay stream")
	}
}
