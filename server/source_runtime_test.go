package server

import (
	"bufio"
	"context"
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

func TestHandleSourceRejectsConcurrentSourceAndKeepsCurrentStream(t *testing.T) {
	s := newListenerAPITestServer(t)
	pass, err := config.HashPassword("sourcepass")
	if err != nil {
		t.Fatalf("hash source password: %v", err)
	}
	s.Config.DefaultSourcePassword = pass
	s.Config.Mounts["/live"] = pass
	s.Config.Users["admin"].Mounts["/live"] = pass

	req1 := httptest.NewRequest(http.MethodPut, "/live", nil)
	req1.RemoteAddr = "127.0.0.1:9101"
	req1.SetBasicAuth("source", "sourcepass")
	rec1 := newSourceHijackRecorder(t)
	done1 := make(chan struct{})
	go func() {
		defer close(done1)
		s.handleSource(rec1, req1)
	}()

	assertEventually(t, func() bool {
		st, ok := s.Relay.GetStream("/live")
		return ok && st.Snapshot().SourceIP == "127.0.0.1:9101"
	})

	req2 := httptest.NewRequest(http.MethodPut, "/live", nil)
	req2.RemoteAddr = "127.0.0.1:9102"
	req2.SetBasicAuth("source", "sourcepass")
	rec2 := httptest.NewRecorder()
	s.handleSource(rec2, req2)

	if rec2.Code != http.StatusForbidden {
		t.Fatalf("expected duplicate source to be rejected, got HTTP %d", rec2.Code)
	}

	st, ok := s.Relay.GetStream("/live")
	if !ok {
		t.Fatal("expected current stream to remain after duplicate source rejection")
	}
	if got := st.Snapshot().SourceIP; got != "127.0.0.1:9101" {
		t.Fatalf("expected original source to remain current, got %q", got)
	}
	if _, ok := s.RuntimeRegistry.Get("/live"); !ok {
		t.Fatal("expected runtime to remain while original source is connected")
	}

	if err := rec1.CloseClient(); err != nil {
		t.Fatalf("close source client: %v", err)
	}
	select {
	case <-done1:
	case <-time.After(time.Second):
		t.Fatal("source handler did not exit after client close")
	}
	if _, ok := s.Relay.GetStream("/live"); ok {
		t.Fatal("expected stream to be removed when original source disconnects")
	}
}

func TestHandleSourceAttachesRuntimeToResolvedTenant(t *testing.T) {
	s := newListenerAPITestServer(t)
	pass, err := config.HashPassword("sourcepass")
	if err != nil {
		t.Fatalf("hash source password: %v", err)
	}
	tenant := s.TenantM.CreateTenant("tenant-a", "Tenant A", "free")
	s.Config.DefaultSourcePassword = pass
	s.Config.Mounts["/live"] = pass
	s.Config.Users["admin"].Mounts["/live"] = pass

	req := httptest.NewRequest(http.MethodPut, "/live", nil)
	req = req.WithContext(context.WithValue(req.Context(), tenantContextKey, tenant))
	req.RemoteAddr = "127.0.0.1:9003"
	req.SetBasicAuth("source", "sourcepass")

	rec := newSourceHijackRecorder(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleSource(rec, req)
	}()

	assertEventually(t, func() bool {
		rt, ok := s.RuntimeRegistry.Get("/live")
		return ok && rt.Source == relay.SourceIcecast && rt.TenantID == "tenant-a" && tenant.StreamCount() == 1
	})

	if err := rec.CloseClient(); err != nil {
		t.Fatalf("close source client: %v", err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("source handler did not exit after client close")
	}

	if got := tenant.StreamCount(); got != 0 {
		t.Fatalf("expected tenant stream count to detach after source disconnect, got %d", got)
	}
}
