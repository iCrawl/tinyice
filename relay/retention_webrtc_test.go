package relay

import (
	"errors"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

func TestRealWebRTCPlaybackSharesAdmissionAndDisconnectAccounting(t *testing.T) {
	r := NewRelay(false, nil)
	out := r.GetOrCreateStream("/opus")
	out.ContentType = "audio/ogg"
	wm := NewWebRTCManager(r)
	wm.SetListenerLimit(func(string) int { return 1 })
	httpListener := &Listener{ID: "http", Protocol: ListenerProtocolHTTP, CurrentMount: "/opus", Connected: time.Now()}
	if err := r.Listeners.Admit(httpListener, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := wm.HandleOffer("/opus", webrtc.SessionDescription{}); !errors.Is(err, ErrListenerCapacity) {
		t.Fatalf("WebRTC ignored HTTP capacity reservation: %v", err)
	}
	r.Listeners.Unregister("http")
	viewer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer viewer.Close()
	if _, err = viewer.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		t.Fatal(err)
	}
	offer, err := viewer.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	gathered := webrtc.GatheringCompletePromise(viewer)
	if err = viewer.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	<-gathered
	answer, err := wm.HandleOffer("/opus", *viewer.LocalDescription(), ListenerSnapshot{RemoteAddr: "192.0.2.10:1234", UserAgent: "test-player"})
	if err != nil {
		t.Fatal(err)
	}
	if err = viewer.SetRemoteDescription(*answer); err != nil {
		t.Fatal(err)
	}
	snapshots := r.Listeners.List()
	if len(snapshots) != 1 || snapshots[0].Protocol != ListenerProtocolWebRTC || snapshots[0].RemoteAddr != "192.0.2.10:1234" {
		t.Fatalf("real WebRTC playback was not registered: %#v", snapshots)
	}
	if _, err = wm.HandleOffer("/opus", webrtc.SessionDescription{}); !errors.Is(err, ErrListenerCapacity) {
		t.Fatal("second WebRTC connection bypassed capacity")
	}
	if _, err = r.Listeners.RequestMoveWithLimit(snapshots[0].ID, "/other", 1); err == nil {
		t.Fatal("unsupported WebRTC move accepted")
	}
	if err = r.Listeners.Disconnect(snapshots[0].ID); err != nil {
		t.Fatal(err)
	}
	waitRetention(t, func() bool { return r.Listeners.CountForMount("/opus") == 0 && out.ListenersCount() == 0 })
}
