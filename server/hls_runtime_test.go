package server

import (
	"context"
	"testing"

	"github.com/DatanoiseTV/tinyice/relay"
)

func TestRegisterHLSRegistersRuntimeOutput(t *testing.T) {
	s := newListenerAPITestServer(t)
	s.hlsOutputs = make(map[string]*relay.HLSOutput)
	s.hlsCtx, s.hlsCancel = context.WithCancel(context.Background())
	defer s.hlsCancel()

	stream := s.Relay.GetOrCreateStream("/live")
	stream.ContentType = "audio/mpeg"
	s.RuntimeRegistry.GetOrCreate("/live").Stream = stream

	hls := s.RegisterHLS("/live")
	if hls == nil {
		t.Fatal("expected hls output")
	}

	rt, ok := s.RuntimeRegistry.Get("/live")
	if !ok {
		t.Fatal("expected runtime for /live")
	}
	if _, ok := rt.Outputs[relay.OutputHLS]; !ok {
		t.Fatal("expected hls output registration")
	}

	s.UnregisterHLS("/live")
	rt, ok = s.RuntimeRegistry.Get("/live")
	if !ok {
		t.Fatal("expected runtime for /live after unregister")
	}
	if _, ok := rt.Outputs[relay.OutputHLS]; ok {
		t.Fatal("expected hls output registration to be removed")
	}
}
