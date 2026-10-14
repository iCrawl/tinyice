package server

import (
	"net/http/httptest"
	"testing"
)

func TestClientIPIgnoresForwardedHeadersFromUntrustedPeer(t *testing.T) {
	s := newListenerAPITestServer(t)
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "203.0.113.10:12345"
	req.Header.Set("X-Forwarded-For", "198.51.100.77")
	req.Header.Set("X-Real-IP", "198.51.100.88")

	if got := s.clientIP(req); got != "203.0.113.10" {
		t.Fatalf("expected direct peer IP, got %q", got)
	}
}

func TestClientIPHonorsForwardedHeadersFromTrustedPeer(t *testing.T) {
	s := newListenerAPITestServer(t)
	s.Config.TrustedProxies = []string{"203.0.113.10"}
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "203.0.113.10:12345"
	req.Header.Set("X-Forwarded-For", "198.51.100.77, 203.0.113.10")

	if got := s.clientIP(req); got != "198.51.100.77" {
		t.Fatalf("expected forwarded client IP, got %q", got)
	}
}

func TestValidateOutboundURLRejectsPrivateTargets(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1:8000/stream",
		"http://localhost:8000/stream",
		"http://10.0.0.5/stream",
		"ftp://example.com/stream",
	} {
		if err := validateOutboundURL(raw); err == nil {
			t.Fatalf("expected %q to be rejected", raw)
		}
	}
}

func TestValidateOutboundURLAllowsPublicHTTPSTarget(t *testing.T) {
	if err := validateOutboundURL("https://example.com/stream"); err != nil {
		t.Fatalf("expected public HTTPS URL to be allowed: %v", err)
	}
}
