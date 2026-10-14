package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandleListenerEmitsICYMetadataForMP3ClientsThatRequestIt(t *testing.T) {
	s := newListenerAPITestServer(t)
	stream := s.Relay.GetOrCreateStream("/fallback")
	stream.UpdateMetadata("Fallback", "Desc", "Genre", "", "128", "audio/mpeg", true, true)
	stream.SetCurrentSong("Artist - Title", s.Relay)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/fallback", nil).WithContext(ctx)
	req.RemoteAddr = "127.0.0.1:9010"
	req.Header.Set("Icy-MetaData", "1")
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleListener(rr, req)
	}()

	assertEventually(t, func() bool {
		return s.Relay.Listeners.CountForMount("/fallback") > 0
	})

	stream.Broadcast(bytes.Repeat([]byte{0x55}, 20000), s.Relay)
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	if got := rr.Header().Get("icy-metaint"); got != "16000" {
		t.Fatalf("expected icy-metaint header, got %q", got)
	}
	if !strings.Contains(rr.Body.String(), "StreamTitle='Artist - Title';") {
		t.Fatalf("expected ICY metadata block in response, got %q", rr.Body.String())
	}
}

func TestHandleListenerDoesNotEmitICYMetadataWithoutClientNegotiation(t *testing.T) {
	s := newListenerAPITestServer(t)
	stream := s.Relay.GetOrCreateStream("/fallback")
	stream.UpdateMetadata("Fallback", "Desc", "Genre", "", "128", "audio/mpeg", true, true)
	stream.SetCurrentSong("Artist - Title", s.Relay)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/fallback", nil).WithContext(ctx)
	req.RemoteAddr = "127.0.0.1:9011"
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleListener(rr, req)
	}()

	assertEventually(t, func() bool {
		return s.Relay.Listeners.CountForMount("/fallback") > 0
	})

	stream.Broadcast(bytes.Repeat([]byte{0x55}, 20000), s.Relay)
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	if got := rr.Header().Get("icy-metaint"); got != "" {
		t.Fatalf("expected no icy-metaint header, got %q", got)
	}
	if strings.Contains(rr.Body.String(), "StreamTitle='Artist - Title';") {
		t.Fatalf("did not expect ICY metadata block in response, got %q", rr.Body.String())
	}
}
