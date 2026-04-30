package server

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/relay"
)

type sourceHijackRecorder struct {
	serverConn net.Conn
	clientConn net.Conn
	header     http.Header
}

func newSourceHijackRecorder(t *testing.T) *sourceHijackRecorder {
	t.Helper()

	serverConn, clientConn := net.Pipe()
	go func() {
		_, _ = io.Copy(io.Discard, clientConn)
	}()
	return &sourceHijackRecorder{
		serverConn: serverConn,
		clientConn: clientConn,
		header:     make(http.Header),
	}
}

func (r *sourceHijackRecorder) Header() http.Header         { return r.header }
func (r *sourceHijackRecorder) WriteHeader(statusCode int)  {}
func (r *sourceHijackRecorder) Write(p []byte) (int, error) { return len(p), nil }

func (r *sourceHijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return r.serverConn, bufio.NewReadWriter(bufio.NewReader(r.serverConn), bufio.NewWriter(r.serverConn)), nil
}

func (r *sourceHijackRecorder) CloseClient() error {
	return r.clientConn.Close()
}

func assertEventually(t *testing.T, fn func() bool) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not satisfied before timeout")
}

func TestHandleSourceRegistersAndRemovesIcecastRuntime(t *testing.T) {
	s := newListenerAPITestServer(t)
	pass, err := config.HashPassword("sourcepass")
	if err != nil {
		t.Fatalf("hash source password: %v", err)
	}
	s.Config.DefaultSourcePassword = pass
	s.Config.Mounts["/live"] = pass
	s.Config.Users["admin"].Mounts["/live"] = pass

	req := httptest.NewRequest(http.MethodPut, "/live", nil)
	req.RemoteAddr = "127.0.0.1:9002"
	req.SetBasicAuth("source", "sourcepass")

	rec := newSourceHijackRecorder(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleSource(rec, req)
	}()

	assertEventually(t, func() bool {
		rt, ok := s.RuntimeRegistry.Get("/live")
		return ok && rt.Source == relay.SourceIcecast && rt.Stream == s.Relay.GetOrCreateStream("/live")
	})

	if err := rec.CloseClient(); err != nil {
		t.Fatalf("close source client: %v", err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("source handler did not exit after client close")
	}

	if _, ok := s.RuntimeRegistry.Get("/live"); ok {
		t.Fatal("expected runtime to be removed when source disconnects")
	}
}
