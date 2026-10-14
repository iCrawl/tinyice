package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DatanoiseTV/tinyice/logger"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type fallbackReviewHook struct{ count atomic.Int64 }

func (h *fallbackReviewHook) Write(entry zapcore.Entry) error {
	if entry.Message == "Primary stream down, falling back" {
		h.count.Add(1)
	}
	return nil
}
func TestListenerOutageWaitsWithoutRepeatedFallbackAttempts(t *testing.T) {
	s := newListenerAPITestServer(t)
	originalLogger := logger.L
	t.Cleanup(func() { logger.L = originalLogger })
	hook := &fallbackReviewHook{}
	logger.L = zap.NewNop().WithOptions(zap.WrapCore(func(c zapcore.Core) zapcore.Core {
		return zapcore.RegisterHooks(zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.AddSync(&discardReviewWriter{}), zap.InfoLevel), hook.Write)
	})).Sugar()
	s.Config.FallbackMounts = map[string]string{"/live": "/backup"}
	live := s.Relay.GetOrCreateStream("/live")
	live.UpdateMetadata("Station", "", "", "", "128", "audio/mpeg", true, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleListener(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/live", nil).WithContext(ctx))
	}()
	deadline := time.Now().Add(time.Second)
	for s.Relay.Listeners.CountForMount("/live") == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.Relay.Listeners.CountForMount("/live") == 0 {
		t.Fatal("listener not admitted")
	}
	s.Relay.RemoveStream("/live")
	time.Sleep(250 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("did not exit")
	}
	if count := hook.count.Load(); count > 3 {
		t.Fatalf("both primary and fallback down caused %d fallback attempts in 250ms", count)
	}
}

type discardReviewWriter struct{}

func (*discardReviewWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestAPIDisconnectListenerDuringBothMountsOutage(t *testing.T) {
	s := newListenerAPITestServer(t)
	s.sessions["sid-1"] = &session{User: s.Config.Users["admin"], CSRFToken: "csrf-ok"}
	originalLogger := logger.L
	t.Cleanup(func() { logger.L = originalLogger })
	hook := &fallbackReviewHook{}
	logger.L = zap.NewNop().WithOptions(zap.WrapCore(func(c zapcore.Core) zapcore.Core {
		return zapcore.RegisterHooks(zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.AddSync(&discardReviewWriter{}), zap.InfoLevel), hook.Write)
	})).Sugar()
	s.Config.FallbackMounts = map[string]string{"/live": "/backup"}
	live := s.Relay.GetOrCreateStream("/live")
	live.UpdateMetadata("Station", "", "", "", "128", "audio/mpeg", true, true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleListener(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/live", nil).WithContext(ctx))
	}()
	defer func() {
		cancel()
		<-done
	}()
	deadline := time.Now().Add(time.Second)
	for live.ListenersCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	listeners := s.Relay.Listeners.List()
	if len(listeners) != 1 || live.ListenersCount() != 1 {
		t.Fatalf("listener did not subscribe: %#v", listeners)
	}
	s.Relay.RemoveStream("/live")
	deadline = time.Now().Add(time.Second)
	for hook.count.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if hook.count.Load() == 0 {
		t.Fatal("listener did not enter fallback outage")
	}
	request := httptest.NewRequest(http.MethodPost, "/api/listeners/disconnect", strings.NewReader(`{"id":"`+listeners[0].ID+`"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", "csrf-ok")
	request.AddCookie(&http.Cookie{Name: "sid", Value: "sid-1"})
	response := httptest.NewRecorder()
	s.apiDisconnectListener(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("disconnect failed: %d %s", response.Code, response.Body.String())
	}
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("operator disconnect left listener waiting for unavailable primary and fallback")
	}
	if got := s.Relay.Listeners.List(); len(got) != 0 {
		t.Fatalf("disconnected listener retained registry slot: %#v", got)
	}
}
