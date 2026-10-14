package server

import (
	"context"
	"github.com/DatanoiseTV/tinyice/logger"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
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
