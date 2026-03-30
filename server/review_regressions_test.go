package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/logger"
	"github.com/DatanoiseTV/tinyice/relay"
	"go.uber.org/zap"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()

	logger.L = zap.NewNop().Sugar()

	cfgPath := filepath.Join(t.TempDir(), "tinyice.json")
	cfg := &config.Config{
		ConfigPath:    cfgPath,
		SetupComplete: true,
		Mounts:        map[string]string{},
		Users: map[string]*config.User{
			"admin": {
				Username: "admin",
				Role:     config.RoleSuperAdmin,
				Mounts:   map[string]string{},
			},
		},
		VisibleMounts: map[string]bool{},
	}
	if err := os.WriteFile(cfgPath, []byte("{}"), 0600); err != nil {
		t.Fatalf("seed config file: %v", err)
	}

	r := relay.NewRelay(false, nil)

	return &Server{
		Config:       cfg,
		Relay:        r,
		StreamerM:    relay.NewStreamerManager(r, cfg),
		sessions:     make(map[string]*session),
		authAttempts: make(map[string]*authAttempt),
		scanAttempts: make(map[string]*scanAttempt),
		startTime:    time.Now(),
		done:         make(chan struct{}),
	}
}

func makeMultipartLogoRequest(t *testing.T, csrfToken string) *http.Request {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("logo", "logo.png")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := io.Copy(part, strings.NewReader("pngdata")); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/branding/logo", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if csrfToken != "" {
		req.Header.Set("X-CSRF-Token", csrfToken)
	}
	req.AddCookie(&http.Cookie{Name: "sid", Value: "sid-1"})
	return req
}

func TestAPIUploadLogoRejectsMultipartWithoutCSRF(t *testing.T) {
	s := newTestServer(t)
	s.sessions["sid-1"] = &session{
		User:      s.Config.Users["admin"],
		CSRFToken: "csrf-ok",
	}

	rr := httptest.NewRecorder()
	s.apiUploadLogo(rr, makeMultipartLogoRequest(t, ""))

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without CSRF token, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestAPIUploadLogoAcceptsMultipartWithCSRF(t *testing.T) {
	s := newTestServer(t)
	s.sessions["sid-1"] = &session{
		User:      s.Config.Users["admin"],
		CSRFToken: "csrf-ok",
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir temp dir: %v", err)
	}
	defer func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()

	rr := httptest.NewRecorder()
	s.apiUploadLogo(rr, makeMultipartLogoRequest(t, "csrf-ok"))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 with CSRF token, got %d: %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(tempDir, "branding", "logo.png")); err != nil {
		t.Fatalf("expected uploaded logo file to exist: %v", err)
	}
}

func TestHandlePublicEventsEmitsNamedEvents(t *testing.T) {
	s := newTestServer(t)
	stream := s.Relay.GetOrCreateStream("/live")
	stream.UpdateMetadata("Station", "Desc", "Genre", "", "128", "audio/mpeg", true, true)
	stream.SetCurrentSong("Track Title", s.Relay)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/events", nil).WithContext(ctx)
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handlePublicEvents(rr, req)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handlePublicEvents did not return after cancellation")
	}

	body := rr.Body.String()
	if !strings.Contains(body, "event: streams\n") {
		t.Fatalf("expected streams event in response, got: %s", body)
	}
	if !strings.Contains(body, "event: stream\n") {
		t.Fatalf("expected stream event in response, got: %s", body)
	}
	if !strings.Contains(body, "event: metadata\n") {
		t.Fatalf("expected metadata event in response, got: %s", body)
	}
	if !strings.Contains(body, "\"mount\":\"/live\"") {
		t.Fatalf("expected /live mount in response, got: %s", body)
	}
}

func TestHandlePublicEventsEmitsEmptyArraysWhenNoVisibleStreams(t *testing.T) {
	s := newTestServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/events", nil).WithContext(ctx)
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handlePublicEvents(rr, req)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handlePublicEvents did not return after cancellation")
	}

	body := rr.Body.String()
	if strings.Contains(body, "data: null\n\n") {
		t.Fatalf("expected empty arrays instead of null payloads, got: %s", body)
	}
	if !strings.Contains(body, "data: []\n\n") {
		t.Fatalf("expected empty array payload in response, got: %s", body)
	}
	if !strings.Contains(body, "event: streams\ndata: []\n\n") {
		t.Fatalf("expected empty streams event in response, got: %s", body)
	}
}

func TestRegisterHLSRejectsOpusStreams(t *testing.T) {
	s := newTestServer(t)
	s.hlsOutputs = make(map[string]*relay.HLSOutput)
	s.hlsCtx, s.hlsCancel = context.WithCancel(context.Background())
	defer s.hlsCancel()

	stream := s.Relay.GetOrCreateStream("/opus")
	stream.ContentType = "audio/ogg"
	stream.IsOggStream = true

	hls := s.RegisterHLS("/opus")
	if hls != nil {
		t.Fatal("expected RegisterHLS to reject opus streams")
	}
	if got := s.getHLSOutput("/opus"); got != nil {
		t.Fatal("expected no stored HLS output for opus stream")
	}
}

func TestAPIDeleteAutoDJAllowsRecreateOnSameMount(t *testing.T) {
	s := newTestServer(t)
	s.sessions["sid-1"] = &session{
		User:      s.Config.Users["admin"],
		CSRFToken: "csrf-ok",
	}

	musicDir := t.TempDir()
	createBody, err := json.Marshal(map[string]any{
		"name":            "DJ One",
		"mount":           "/stream",
		"music_dir":       musicDir,
		"format":          "mp3",
		"bitrate":         128,
		"loop":            true,
		"inject_metadata": true,
	})
	if err != nil {
		t.Fatalf("marshal create body: %v", err)
	}

	createReq := httptest.NewRequest(http.MethodPost, "/api/autodj", bytes.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createReq.AddCookie(&http.Cookie{Name: "sid", Value: "sid-1"})
	createRR := httptest.NewRecorder()
	s.apiCreateAutoDJ(createRR, createReq)
	if createRR.Code != http.StatusOK {
		t.Fatalf("expected initial create to succeed, got %d: %s", createRR.Code, createRR.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/autodj?mount=%2Fstream", nil)
	deleteReq.Header.Set("X-CSRF-Token", "csrf-ok")
	deleteReq.AddCookie(&http.Cookie{Name: "sid", Value: "sid-1"})
	deleteRR := httptest.NewRecorder()
	s.apiDeleteAutoDJ(deleteRR, deleteReq)
	if deleteRR.Code != http.StatusOK {
		t.Fatalf("expected delete to succeed, got %d: %s", deleteRR.Code, deleteRR.Body.String())
	}

	recreateReq := httptest.NewRequest(http.MethodPost, "/api/autodj", bytes.NewReader(createBody))
	recreateReq.Header.Set("Content-Type", "application/json")
	recreateReq.AddCookie(&http.Cookie{Name: "sid", Value: "sid-1"})
	recreateRR := httptest.NewRecorder()
	s.apiCreateAutoDJ(recreateRR, recreateReq)
	if recreateRR.Code != http.StatusOK {
		t.Fatalf("expected recreate on same mount to succeed, got %d: %s", recreateRR.Code, recreateRR.Body.String())
	}
}
