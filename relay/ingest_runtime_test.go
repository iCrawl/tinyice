package relay

import (
	"net"
	"testing"

	"github.com/DatanoiseTV/tinyice/config"
	rtmp "github.com/yutopp/go-rtmp"
	rtmpmsg "github.com/yutopp/go-rtmp/message"
)

func TestRTMPPublishRegistersAndRemovesRuntime(t *testing.T) {
	r := NewRelay(false, nil)
	rr := NewRuntimeRegistry(r)
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	h := &rtmpHandler{
		relay:           r,
		config:          &config.Config{},
		conn:            serverConn,
		runtimeRegistry: rr,
	}

	err := h.OnPublish(&rtmp.StreamContext{}, 0, &rtmpmsg.NetStreamPublish{PublishingName: "/live"})
	if err != nil {
		t.Fatalf("OnPublish: %v", err)
	}

	rt, ok := rr.Get("/live")
	if !ok {
		t.Fatal("expected RTMP runtime to be registered")
	}
	if rt.Source != SourceRTMP {
		t.Fatalf("expected RTMP source, got %q", rt.Source)
	}
	if rt.Stream != r.GetOrCreateStream("/live") {
		t.Fatal("expected runtime to reference RTMP audio stream")
	}

	h.OnClose()

	if _, ok := rr.Get("/live"); ok {
		t.Fatal("expected RTMP runtime to be removed on close")
	}
}
