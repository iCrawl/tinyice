package server

import (
	"context"
	"embed"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/logger"
	"github.com/DatanoiseTV/tinyice/relay"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"go.uber.org/zap"
	"golang.org/x/crypto/acme/autocert"
)

//go:embed all:assets
var assetFS embed.FS

// Server is the main HTTP server and application coordinator for TinyIce.
//
// The Server handles all HTTP requests, manages WebSocket connections, serves
// the web interface, and coordinates between the various subsystems (relay,
// transcoders, streamers, etc.).
//
// Key responsibilities:
//   - HTTP request routing and handling
//   - Web interface rendering (templates, assets)
//   - WebSocket connections for real-time updates
//   - Source client authentication and authorization
//   - Listener connection management
//   - Admin interface and API endpoints
//
// Lifecycle:
//   - Created with NewServer()
//   - Configured with routes and middleware
//   - Started with Start()
//   - Stopped gracefully with Stop()
//
// Thread Safety:
// The Server is designed to be thread-safe. HTTP handlers are called from
// multiple goroutines concurrently, so all handler methods must be safe
// for concurrent access.
type Server struct {
	Config      *config.Config           // Application configuration
	Relay       *relay.Relay             // Core relay/streaming engine
	RelayM      *relay.RelayManager      // Relay stream management
	TranscoderM *relay.TranscoderManager // Transcoding management
	HealthM     *relay.HealthMonitor     // Stream health monitoring
	WebRTCM     *relay.WebRTCManager     // WebRTC connection management
	StreamerM   *relay.StreamerManager   // AutoDJ/streamer management
	RTMP        *relay.RTMPServer        // RTMP ingest server (optional)
	SRT         *relay.SRTServer         // SRT ingest server (optional)
	TenantM     *relay.TenantManager     // Multi-tenant management
	mpdServer   *relay.MPDServer         // MPD protocol server (optional)
	shell       *ShellRenderer           // New Preact frontend renderer
	Version     string                   // TinyIce version
	Commit      string                   // Git commit hash
	httpServers []*http.Server           // Active HTTP servers
	startTime   time.Time                // Server start time
	AuthLog     *zap.SugaredLogger

	sessions   map[string]*session
	sessionsMu sync.RWMutex

	authAttempts   map[string]*authAttempt
	authAttemptsMu sync.Mutex

	certManager *autocert.Manager

	scanAttempts   map[string]*scanAttempt
	scanAttemptsMu sync.Mutex

	hlsOutputs map[string]*relay.HLSOutput
	hlsMu      sync.RWMutex
	hlsCtx     context.Context
	hlsCancel  context.CancelFunc

	done chan struct{}

	configMu   sync.Mutex // protects config writes
	setupToken string     // single-use token for first-run setup (empty = setup complete)

	webAuthn         *webauthn.WebAuthn
	webauthnSessions map[string]*webauthn.SessionData
	webauthnMu       sync.Mutex

	tokenSaveTimer *time.Timer
	tokenSaveMu    sync.Mutex
	tokenSaveDelay time.Duration

	deadStreamRecovery func(string)
}

func NewServer(cfg *config.Config, authLog *zap.SugaredLogger, version, commit, setupToken string) *Server {
	hm, err := relay.NewHistoryManager("history.db")
	if err != nil {
		logger.L.Fatalf("Failed to initialize history manager: %v", err)
	}

	r := relay.NewRelay(cfg.LowLatencyMode, hm)

	// Initialize WebAuthn
	var wa *webauthn.WebAuthn
	rpID := "localhost"
	rpName := "TinyIce"
	rpOrigins := []string{"http://localhost:8000"}
	if cfg.WebAuthn != nil {
		if cfg.WebAuthn.RPID != "" {
			rpID = cfg.WebAuthn.RPID
		}
		if cfg.WebAuthn.RPName != "" {
			rpName = cfg.WebAuthn.RPName
		}
		if len(cfg.WebAuthn.RPOrigins) > 0 {
			rpOrigins = cfg.WebAuthn.RPOrigins
		}
	} else if cfg.BaseURL != "" {
		if u, err := url.Parse(cfg.BaseURL); err == nil {
			rpID = u.Hostname()
			rpOrigins = []string{cfg.BaseURL}
		}
	}
	wa, _ = webauthn.New(&webauthn.Config{
		RPID:                  rpID,
		RPDisplayName:         rpName,
		RPOrigins:             rpOrigins,
		AttestationPreference: protocol.PreferNoAttestation,
	})

	healthM := relay.NewHealthMonitor(r)
	hlsCtx, hlsCancel := context.WithCancel(context.Background())
	srv := &Server{
		Config:           cfg,
		Relay:            r,
		HealthM:          healthM,
		RelayM:           relay.NewRelayManager(r),
		TranscoderM:      relay.NewTranscoderManager(r),
		WebRTCM:          relay.NewWebRTCManager(r),
		StreamerM:        relay.NewStreamerManager(r, cfg),
		RTMP:             relay.NewRTMPServer(r, cfg),
		SRT:              relay.NewSRTServer(r, cfg),
		TenantM:          relay.NewTenantManager(),
		shell:            NewShellRenderer(),
		Version:          version,
		Commit:           commit,
		startTime:        time.Now(),
		AuthLog:          authLog,
		sessions:         make(map[string]*session),
		authAttempts:     make(map[string]*authAttempt),
		scanAttempts:     make(map[string]*scanAttempt),
		hlsOutputs:       make(map[string]*relay.HLSOutput),
		hlsCtx:           hlsCtx,
		hlsCancel:        hlsCancel,
		done:             make(chan struct{}),
		setupToken:       setupToken,
		webAuthn:         wa,
		webauthnSessions: make(map[string]*webauthn.SessionData),
	}
	srv.deadStreamRecovery = srv.StreamerM.RecoverDeadSongCommandMount
	healthM.OnEvent(srv.handleStreamHealthEvent)

	// Ensure default tenant exists for backward compatibility
	srv.TenantM.GetOrCreateDefaultTenant()

	return srv
}

func (s *Server) handleStreamHealthEvent(e relay.StreamHealthEvent) {
	logger.L.Infow("Stream health event",
		"mount", e.Mount,
		"old_status", e.OldStatus.String(),
		"new_status", e.NewStatus.String(),
	)
	if e.NewStatus == relay.StatusDead && s.deadStreamRecovery != nil {
		s.deadStreamRecovery(e.Mount)
	}
}

// withSetupGuard wraps a handler to enforce setup mode.
func (s *Server) withSetupGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.Config.SetupComplete {
			path := r.URL.Path
			if path == "/setup" || strings.HasPrefix(path, "/setup/") || strings.HasPrefix(path, "/assets/") {
				logger.L.Debugw("setupGuard: allowing through", "path", path)
				next.ServeHTTP(w, r)
				return
			}
			http.Redirect(w, r, "/setup", http.StatusTemporaryRedirect)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) Shutdown(ctx context.Context) error {
	logger.L.Info("Server shutting down gracefully...")

	if s.hlsCancel != nil {
		s.hlsCancel()
	}

	close(s.done)

	s.Relay.DisconnectAllListeners()

	s.RelayM.StopAll()
	s.TranscoderM.StopAll()

	for _, st := range s.StreamerM.GetStreamers() {
		s.StreamerM.StopStreamer(st.OutputMount)
	}

	if s.RTMP != nil {
		s.RTMP.Stop()
	}

	if s.SRT != nil {
		s.SRT.Stop()
	}

	var wg sync.WaitGroup
	for _, srv := range s.httpServers {
		wg.Add(1)
		go func(srv *http.Server) {
			defer wg.Done()
			shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := srv.Shutdown(shCtx); err != nil {
				logger.L.Errorf("Error during HTTP server shutdown: %v", err)
			}
		}(srv)
	}

	waitDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
		logger.L.Info("All servers shut down successfully")
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) HotSwap() error {
	logger.L.Info("Initiating zero-downtime hot swap...")

	exe, err := os.Executable()
	if err != nil {
		return err
	}

	process, err := os.StartProcess(exe, os.Args, &os.ProcAttr{
		Dir:   ".",
		Env:   os.Environ(),
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	})
	if err != nil {
		return fmt.Errorf("failed to start new process: %v", err)
	}

	logger.L.Infof("New process started with PID %d. Waiting for health check...", process.Pid)

	time.Sleep(5 * time.Second)

	logger.L.Info("Handoff period complete. Shutting down old process...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	go func() {
		s.Shutdown(ctx)
		os.Exit(0)
	}()

	return nil
}

func (s *Server) ReloadConfig(cfg *config.Config) {
	s.Config = cfg
	s.RelayM.StopAll()
	for _, rc := range s.Config.Relays {
		if rc.Enabled {
			s.RelayM.StartRelay(rc.URL, rc.Mount, rc.Password, rc.BurstSize, s.Config.VisibleMounts[rc.Mount])
		}
	}
	s.TranscoderM.StopAll()
	for _, tc := range s.Config.Transcoders {
		if tc.Enabled {
			s.TranscoderM.StartTranscoder(tc)
		}
	}
	logger.L.Info("Configuration reloaded successfully")
}

func (s *Server) Start() error {
	mux := s.setupRoutes()
	// Setup guard is handled inside the "/" catch-all handler since it intercepts all routes.
	// The withSetupGuard middleware is not used because the "/" handler in Go's ServeMux
	// catches routes before they reach more specific handlers when registered as a catch-all.
	var handler http.Handler = mux
	if s.Config.MultiTenant != nil && s.Config.MultiTenant.Enabled {
		handler = s.withTenant(handler)
	}

	if s.Config.DirectoryListing {
		go s.directoryReportingTask()
	}
	go s.statsRecordingTask()
	go s.HealthM.Start(context.Background())

	for _, rc := range s.Config.Relays {
		if rc.Enabled {
			s.RelayM.StartRelay(rc.URL, rc.Mount, rc.Password, rc.BurstSize, s.Config.VisibleMounts[rc.Mount])
		}
	}
	for _, tc := range s.Config.Transcoders {
		if tc.Enabled {
			s.TranscoderM.StartTranscoder(tc)
		}
	}
	for _, adj := range s.Config.AutoDJs {
		absMusicDir, _ := filepath.Abs(adj.MusicDir)
		streamer, err := s.StreamerM.StartStreamer(adj.Name, adj.Mount, absMusicDir, adj.Loop, adj.Format, adj.Bitrate, adj.InjectMetadata, adj.Playlist, adj.MPDEnabled, adj.MPDPort, adj.MPDPassword, adj.Visible, adj.LastPlaylist, adj.SongCommand, adj.SongCommandTimeout)
		if err == nil {
			if adj.LastPlaylist != "" {
				streamer.LoadPlaylist(adj.LastPlaylist)
			}
			if adj.Enabled {
				if adj.LastPlaylist != "" {
					streamer.LoadPlaylist(adj.LastPlaylist)
				}
				if adj.InjectMetadata {
					if st, ok := s.Relay.GetStream(adj.Mount); ok {
						st.SetVisible(adj.Visible)
					}
				}
				if len(adj.Playlist) == 0 {
					streamer.ScanMusicDir()
				}
				streamer.Play()
			}
		} else {
			logger.L.Errorf("Failed to initialize AutoDJ %s: %v", adj.Name, err)
		}
	}

	// Start RTMP server if enabled
	if s.Config.Ingest != nil && s.Config.Ingest.RTMPEnabled {
		if err := s.RTMP.Start(); err != nil {
			logger.L.Errorf("Failed to start RTMP server: %v", err)
		}
	}

	// Start SRT server if enabled
	if s.Config.Ingest != nil && s.Config.Ingest.SRTEnabled {
		if err := s.SRT.Start(); err != nil {
			logger.L.Errorf("Failed to start SRT server: %v", err)
		}
	}

	port := s.Config.Port

	if s.Config.UseHTTPS {
		addr := net.JoinHostPort(s.Config.BindHost, port)
		return s.startHTTPS(handler, addr)
	}

	srv := &http.Server{
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}
	s.httpServers = append(s.httpServers, srv)

	listeners, err := s.buildListeners(port)
	if err != nil {
		return err
	}

	if len(listeners) == 1 {
		logger.L.Infof("Starting TinyIce on %s (HTTP)", listeners[0].Addr())
		return srv.Serve(listeners[0])
	}

	logger.L.Infof("Starting TinyIce on [::]:%s and 0.0.0.0:%s (HTTP)", port, port)
	combined := newMultiListener(listeners)
	return srv.Serve(combined)
}
