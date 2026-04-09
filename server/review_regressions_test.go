package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/logger"
	"github.com/DatanoiseTV/tinyice/relay"
	"go.uber.org/zap"
)

var testLoggerOnce sync.Once

func newTestServer(t *testing.T) *Server {
	t.Helper()

	testLoggerOnce.Do(func() {
		logger.L = zap.NewNop().Sugar()
	})

	cfgPath := filepath.Join(t.TempDir(), "tinyice.json")
	cfg := &config.Config{
		ConfigPath:     cfgPath,
		SetupComplete:  true,
		Mounts:         map[string]string{},
		AdvancedMounts: map[string]*config.MountSettings{},
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

	hm, err := relay.NewHistoryManager(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatalf("history manager: %v", err)
	}

	r := relay.NewRelay(false, hm)
	rr := relay.NewRuntimeRegistry(r)
	tm := relay.NewTenantManager()
	tm.GetOrCreateDefaultTenant()
	rr.SetTenantManager(tm)
	sm := relay.NewStreamerManager(r, cfg)
	sm.SetRuntimeRegistry(rr)

	return &Server{
		Config:          cfg,
		Relay:           r,
		RuntimeRegistry: rr,
		StreamerM:       sm,
		TenantM:         tm,
		shell:           NewShellRenderer(),
		sessions:        make(map[string]*session),
		authAttempts:    make(map[string]*authAttempt),
		scanAttempts:    make(map[string]*scanAttempt),
		startTime:       time.Now(),
		done:            make(chan struct{}),
	}
}

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

func TestNewServerInitializesRuntimeRegistry(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "tinyice.json")
	cfg := &config.Config{
		ConfigPath:     cfgPath,
		SetupComplete:  true,
		Mounts:         map[string]string{},
		AdvancedMounts: map[string]*config.MountSettings{},
		VisibleMounts:  map[string]bool{},
	}
	if err := os.WriteFile(cfgPath, []byte("{}"), 0600); err != nil {
		t.Fatalf("seed config file: %v", err)
	}

	s := NewServer(cfg, zap.NewNop().Sugar(), "test", "test", "")
	if s.RuntimeRegistry == nil {
		t.Fatal("expected runtime registry to be initialized")
	}
	if s.RuntimeRegistry.GetOrCreate("/live").Stream != s.Relay.GetOrCreateStream("/live") {
		t.Fatal("expected runtime registry to wrap the live relay streams")
	}
}

func TestHandleSourceRegistersIcecastMountRuntime(t *testing.T) {
	s := newTestServer(t)
	pass, err := config.HashPassword("sourcepass")
	if err != nil {
		t.Fatalf("hash source password: %v", err)
	}
	s.Config.DefaultSourcePassword = pass

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
		return ok && rt.Source == relay.SourceIcecast
	})

	if err := rec.CloseClient(); err != nil {
		t.Fatalf("close source client: %v", err)
	}
	<-done
}

func TestHandleSourceRemovesRuntimeOnDisconnect(t *testing.T) {
	s := newTestServer(t)
	pass, err := config.HashPassword("sourcepass")
	if err != nil {
		t.Fatalf("hash source password: %v", err)
	}
	s.Config.DefaultSourcePassword = pass

	req := httptest.NewRequest(http.MethodPut, "/live", nil)
	req.RemoteAddr = "127.0.0.1:9003"
	req.SetBasicAuth("source", "sourcepass")

	rec := newSourceHijackRecorder(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleSource(rec, req)
	}()

	assertEventually(t, func() bool {
		_, ok := s.Relay.GetStream("/live")
		return ok
	})

	if err := rec.CloseClient(); err != nil {
		t.Fatalf("close source client: %v", err)
	}
	<-done

	if _, ok := s.RuntimeRegistry.Get("/live"); ok {
		t.Fatal("expected runtime to be removed when source disconnects")
	}
}

func TestHandleListenerRejectsWhenMountSpecificCapIsReached(t *testing.T) {
	s := newTestServer(t)
	s.Config.MaxListeners = 10
	s.Config.AdvancedMounts["/live"] = &config.MountSettings{MaxListeners: 1}

	stream := s.Relay.GetOrCreateStream("/live")
	stream.UpdateMetadata("Station", "Desc", "Genre", "", "128", "audio/mpeg", true, true)
	s.Relay.Listeners.Register(&relay.Listener{
		ID:             "existing",
		Protocol:       relay.ListenerProtocolHTTP,
		RequestedMount: "/live",
		CurrentMount:   "/live",
		Connected:      time.Now(),
	})

	req := httptest.NewRequest(http.MethodGet, "/live", nil)
	req.RemoteAddr = "127.0.0.1:9000"
	rr := httptest.NewRecorder()

	s.handleListener(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when mount cap is reached, got %d", rr.Code)
	}
}

func TestHandleListenerRegistersAndUnregistersHTTPPlaybackClients(t *testing.T) {
	s := newTestServer(t)
	stream := s.Relay.GetOrCreateStream("/live")
	stream.UpdateMetadata("Station", "Desc", "Genre", "", "128", "audio/mpeg", true, true)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/live", nil).WithContext(ctx)
	req.RemoteAddr = "127.0.0.1:9001"
	req.Header.Set("User-Agent", "tinyice-test")
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleListener(rr, req)
	}()

	deadline := time.After(time.Second)
	for s.Relay.Listeners.CountForMount("/live") == 0 {
		select {
		case <-deadline:
			t.Fatal("listener never registered")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("listener handler did not exit after cancellation")
	}

	if got := s.Relay.Listeners.CountForMount("/live"); got != 0 {
		t.Fatalf("expected listener to unregister on exit, got %d", got)
	}
}

func TestHandleListenerEmitsICYMetadataForMP3ClientsThatRequestIt(t *testing.T) {
	s := newTestServer(t)
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
	s := newTestServer(t)
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

func TestAPIUpdateStreamPersistsAdvancedMountSettings(t *testing.T) {
	s := newTestServer(t)
	s.Config.Mounts["/live"] = "hashed"
	s.sessions["sid-1"] = &session{
		User:      s.Config.Users["admin"],
		CSRFToken: "csrf-ok",
	}

	req := httptest.NewRequest(http.MethodPut, "/api/streams", strings.NewReader(`{"mount":"/live","burst_size":131072,"max_listeners":42}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", "csrf-ok")
	req.AddCookie(&http.Cookie{Name: "sid", Value: "sid-1"})
	rr := httptest.NewRecorder()

	s.apiUpdateStream(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	ms := s.Config.AdvancedMounts["/live"]
	if ms == nil {
		t.Fatal("expected advanced mount settings to be created")
	}
	if ms.BurstSize != 131072 {
		t.Fatalf("expected burst size 131072, got %d", ms.BurstSize)
	}
	if ms.MaxListeners != 42 {
		t.Fatalf("expected max listeners 42, got %d", ms.MaxListeners)
	}
}

func TestHandleListenerProcessesMoveCommand(t *testing.T) {
	s := newTestServer(t)
	live := s.Relay.GetOrCreateStream("/live")
	live.UpdateMetadata("Live", "Desc", "Genre", "", "128", "audio/mpeg", true, true)
	backup := s.Relay.GetOrCreateStream("/backup")
	backup.UpdateMetadata("Backup", "Desc", "Genre", "", "128", "audio/mpeg", true, true)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/live", nil).WithContext(ctx)
	req.RemoteAddr = "127.0.0.1:9002"
	req.Header.Set("User-Agent", "tinyice-move-test")
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleListener(rr, req)
	}()

	var listenerID string
	deadline := time.After(time.Second)
	for listenerID == "" {
		select {
		case <-deadline:
			t.Fatal("listener never registered")
		default:
			listeners := s.Relay.Listeners.List()
			if len(listeners) > 0 {
				listenerID = listeners[0].ID
				if listeners[0].CurrentMount != "/live" {
					t.Fatalf("expected initial mount /live, got %q", listeners[0].CurrentMount)
				}
			} else {
				time.Sleep(10 * time.Millisecond)
			}
		}
	}

	if err := s.Relay.Listeners.RequestMove(listenerID, "/backup"); err != nil {
		t.Fatalf("queue move: %v", err)
	}

	moveDeadline := time.After(time.Second)
	for {
		select {
		case <-moveDeadline:
			t.Fatal("listener never moved to /backup")
		default:
			snapshot, ok := s.Relay.Listeners.Get(listenerID)
			if ok && snapshot.CurrentMount == "/backup" {
				cancel()
				<-done
				if got := s.Relay.Listeners.CountForMount("/live"); got != 0 {
					t.Fatalf("expected /live to be empty after move and disconnect, got %d", got)
				}
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
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

func TestHandleEventsEmitsAdminStreamAndAutoDJContracts(t *testing.T) {
	s := newTestServer(t)
	s.sessions["sid-1"] = &session{
		User:      s.Config.Users["admin"],
		CSRFToken: "csrf-ok",
	}

	stream := s.Relay.GetOrCreateStream("/live")
	stream.UpdateMetadata("Station", "Desc", "Genre", "", "128", "audio/mpeg", true, true)
	stream.SetCurrentSong("Track Title", s.Relay)

	musicDir := t.TempDir()
	streamer, err := s.StreamerM.StartStreamer("Admin AutoDJ", "/live", musicDir, false, "mp3", 128, true, nil, false, "", "", true, "", "", 0)
	if err != nil {
		t.Fatalf("start streamer: %v", err)
	}
	defer s.StreamerM.RemoveStreamer("/live")
	streamer.PushToQueue(filepath.Join(musicDir, "next-track.mp3"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/admin/events", nil).WithContext(ctx)
	req.AddCookie(&http.Cookie{Name: "sid", Value: "sid-1"})
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleEvents(rr, req)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handleEvents did not return after cancellation")
	}

	body := rr.Body.String()
	if !strings.Contains(body, "event: stats\n") {
		t.Fatalf("expected stats event in response, got: %s", body)
	}
	if !strings.Contains(body, "event: stream\n") {
		t.Fatalf("expected stream event in response, got: %s", body)
	}
	if !strings.Contains(body, `"format":"audio/mpeg"`) {
		t.Fatalf("expected stream event to include format, got: %s", body)
	}
	if !strings.Contains(body, "event: autodj\n") {
		t.Fatalf("expected autodj event in response, got: %s", body)
	}
	if !strings.Contains(body, `"currentTrack":`) {
		t.Fatalf("expected autodj event to include currentTrack payload, got: %s", body)
	}
	if !strings.Contains(body, `"queue":["next-track.mp3"]`) {
		t.Fatalf("expected autodj event to include queue titles, got: %s", body)
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

func TestRegisterHLSRegistersRuntimeOutput(t *testing.T) {
	s := newTestServer(t)
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
	if _, ok := rt.Outputs[relay.OutputHLS]; ok {
		t.Fatal("expected hls output registration to be removed")
	}
}

func TestRuntimeRegistryDoesNotChangeRelaySnapshotShape(t *testing.T) {
	s := newTestServer(t)
	stream := s.Relay.GetOrCreateStream("/live")
	stream.SetCurrentSong("test song", s.Relay)
	s.RuntimeRegistry.AttachSource("/live", relay.SourceIcecast, "default")

	stats := s.Relay.Snapshot()
	if len(stats) != 1 {
		t.Fatalf("expected 1 stream snapshot, got %d", len(stats))
	}
	if stats[0].MountName != "/live" {
		t.Fatalf("expected /live snapshot, got %q", stats[0].MountName)
	}
}

func TestRuntimeForMountReturnsAdminMetadataWithoutChangingEvents(t *testing.T) {
	s := newTestServer(t)
	stream := s.Relay.GetOrCreateStream("/live")
	s.RuntimeRegistry.GetOrCreate("/live").Stream = stream
	s.RuntimeRegistry.AttachSource("/live", relay.SourceAutoDJ, "default")

	rt, ok := s.runtimeForMount("/live")
	if !ok {
		t.Fatal("expected runtime metadata for /live")
	}
	if rt.Source != relay.SourceAutoDJ {
		t.Fatalf("expected autodj source, got %q", rt.Source)
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

func TestMountScopedUserCannotManageOtherUsersAutoDJResources(t *testing.T) {
	s := newTestServer(t)
	s.Config.Users["dj"] = &config.User{
		Username: "dj",
		Role:     config.RoleAdmin,
		Mounts: map[string]string{
			"/owned": "hashed",
		},
	}
	s.sessions["sid-dj"] = &session{
		User:      s.Config.Users["dj"],
		CSRFToken: "csrf-ok",
	}

	musicDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(musicDir, "track.mp3"), []byte("ID3test-data"), 0600); err != nil {
		t.Fatalf("seed music file: %v", err)
	}
	if _, err := s.StreamerM.StartStreamer("Other", "/other", musicDir, false, "mp3", 128, false, nil, false, "", "", true, "", "", 0); err != nil {
		t.Fatalf("start streamer: %v", err)
	}
	s.Config.AutoDJs = []*config.AutoDJConfig{{
		Name:     "Other",
		Mount:    "/other",
		MusicDir: musicDir,
		Format:   "mp3",
		Bitrate:  128,
		Enabled:  true,
	}}

	tests := []struct {
		name string
		run  func(*httptest.ResponseRecorder)
	}{
		{
			name: "create autodj on unauthorized mount",
			run: func(rr *httptest.ResponseRecorder) {
				body := strings.NewReader(`{"name":"Nope","mount":"/other","music_dir":"` + musicDir + `"}`)
				req := httptest.NewRequest(http.MethodPost, "/api/autodj", body)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-CSRF-Token", "csrf-ok")
				req.AddCookie(&http.Cookie{Name: "sid", Value: "sid-dj"})
				s.apiCreateAutoDJ(rr, req)
			},
		},
		{
			name: "play autodj on unauthorized mount",
			run: func(rr *httptest.ResponseRecorder) {
				req := httptest.NewRequest(http.MethodPost, "/api/autodj/play?mount=%2Fother", nil)
				req.Header.Set("X-CSRF-Token", "csrf-ok")
				req.AddCookie(&http.Cookie{Name: "sid", Value: "sid-dj"})
				s.apiAutoDJPlay(rr, req)
			},
		},
		{
			name: "read playlist on unauthorized mount",
			run: func(rr *httptest.ResponseRecorder) {
				req := httptest.NewRequest(http.MethodGet, "/api/playlist?mount=%2Fother", nil)
				req.AddCookie(&http.Cookie{Name: "sid", Value: "sid-dj"})
				s.apiGetPlaylist(rr, req)
			},
		},
		{
			name: "queue file on unauthorized mount",
			run: func(rr *httptest.ResponseRecorder) {
				body := strings.NewReader(`{"mount":"/other","path":"track.mp3"}`)
				req := httptest.NewRequest(http.MethodPost, "/api/queue", body)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-CSRF-Token", "csrf-ok")
				req.AddCookie(&http.Cookie{Name: "sid", Value: "sid-dj"})
				s.apiAddToQueue(rr, req)
			},
		},
		{
			name: "browse files on unauthorized mount",
			run: func(rr *httptest.ResponseRecorder) {
				req := httptest.NewRequest(http.MethodGet, "/api/files?mount=%2Fother", nil)
				req.AddCookie(&http.Cookie{Name: "sid", Value: "sid-dj"})
				s.apiGetFiles(rr, req)
			},
		},
		{
			name: "delete autodj on unauthorized mount",
			run: func(rr *httptest.ResponseRecorder) {
				req := httptest.NewRequest(http.MethodDelete, "/api/autodj?mount=%2Fother", nil)
				req.Header.Set("X-CSRF-Token", "csrf-ok")
				req.AddCookie(&http.Cookie{Name: "sid", Value: "sid-dj"})
				s.apiDeleteAutoDJ(rr, req)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			tc.run(rr)
			if rr.Code != http.StatusForbidden {
				t.Fatalf("expected forbidden, got %d: %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestNonSuperAdminCannotCreateTranscoder(t *testing.T) {
	s := newTestServer(t)
	s.Config.Users["dj"] = &config.User{
		Username: "dj",
		Role:     config.RoleAdmin,
		Mounts: map[string]string{
			"/owned": "hashed",
		},
	}
	s.sessions["sid-dj"] = &session{
		User:      s.Config.Users["dj"],
		CSRFToken: "csrf-ok",
	}

	req := httptest.NewRequest(http.MethodPost, "/api/transcoders", strings.NewReader(`{"name":"x","input_mount":"/owned","output_mount":"/x","format":"mp3","bitrate":128}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", "csrf-ok")
	req.AddCookie(&http.Cookie{Name: "sid", Value: "sid-dj"})
	rr := httptest.NewRecorder()

	s.apiCreateTranscoder(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestAPICreateAutoDJRollsBackConfigWhenStartFails(t *testing.T) {
	s := newTestServer(t)
	s.sessions["sid-1"] = &session{
		User:      s.Config.Users["admin"],
		CSRFToken: "csrf-ok",
	}

	musicDir := t.TempDir()
	if _, err := s.StreamerM.StartStreamer("Existing", "/dup", musicDir, false, "mp3", 128, false, nil, false, "", "", true, "", "", 0); err != nil {
		t.Fatalf("start existing streamer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/autodj", strings.NewReader(`{"name":"Broken","mount":"/dup","music_dir":"`+musicDir+`","format":"mp3","bitrate":128}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", "csrf-ok")
	req.AddCookie(&http.Cookie{Name: "sid", Value: "sid-1"})
	rr := httptest.NewRecorder()

	s.apiCreateAutoDJ(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected startup failure, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(s.Config.AutoDJs) != 0 {
		t.Fatalf("expected failed create to leave config unchanged, got %d entries", len(s.Config.AutoDJs))
	}
}

func TestMutateConfigPersistsChanges(t *testing.T) {
	s := newTestServer(t)

	if err := s.mutateConfig(func(cfg *config.Config) error {
		cfg.PageTitle = "Updated Title"
		cfg.VisibleMounts["/live"] = true
		return nil
	}); err != nil {
		t.Fatalf("mutate config: %v", err)
	}

	data, err := os.ReadFile(s.Config.ConfigPath)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}

	var saved config.Config
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("unmarshal saved config: %v", err)
	}
	if saved.PageTitle != "Updated Title" {
		t.Fatalf("expected saved page title to persist, got %q", saved.PageTitle)
	}
	if !saved.VisibleMounts["/live"] {
		t.Fatalf("expected saved visible mount flag to persist")
	}
}

func TestTouchTokenPersistsConfigThroughMutationPath(t *testing.T) {
	s := newTestServer(t)
	s.tokenSaveDelay = 10 * time.Millisecond
	s.Config.APITokens = []*config.APIToken{{
		ID:       "tok-1",
		Name:     "test",
		Username: "admin",
		Role:     config.RoleSuperAdmin,
	}}

	s.touchToken(s.Config.APITokens[0], "127.0.0.1:9000")

	deadline := time.Now().Add(time.Second)
	for {
		data, err := os.ReadFile(s.Config.ConfigPath)
		if err != nil {
			t.Fatalf("read saved config: %v", err)
		}

		var saved config.Config
		if err := json.Unmarshal(data, &saved); err != nil {
			t.Fatalf("unmarshal saved config: %v", err)
		}
		if len(saved.APITokens) == 1 && saved.APITokens[0].LastUsedIP == "127.0.0.1" && saved.APITokens[0].LastUsedAt != "" {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("expected token usage to persist, got %#v", saved.APITokens)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
