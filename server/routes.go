package server

import (
	"net/http"
	"net/url"
	"strings"
)

func methodHandler(handlers map[string]http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if handler, ok := handlers[r.Method]; ok {
			handler(w, r)
			return
		}
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) setupRoutes() *http.ServeMux {
	mux := http.NewServeMux()
	s.registerAdminRoutes(mux)
	s.registerSetupRoutes(mux)
	s.registerAuthRoutes(mux)
	s.registerPublicRoutes(mux)
	s.registerAPIRoutes(mux)
	return mux
}

func (s *Server) registerAdminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/admin", s.handleAdmin)
	mux.HandleFunc("/admin/golive", s.handleGoLive)
	mux.HandleFunc("/admin/golive/chunk", s.handleGoLiveChunk)
	mux.HandleFunc("/admin/add-mount", s.handleAddMount)
	mux.HandleFunc("/admin/toggle-latency", s.handleToggleLatency)
	mux.HandleFunc("/admin/stats", s.handleStats)
	mux.HandleFunc("/admin/events", s.handleEvents)
	mux.HandleFunc("/admin/metadata", s.handleMetadata)
	mux.HandleFunc("/admin/kick", s.handleKick)
	mux.HandleFunc("/admin/remove-mount", s.handleRemoveMount)
	mux.HandleFunc("/admin/hotswap", s.handleHotSwap)
	mux.HandleFunc("/admin/kick-all-listeners", s.handleKickAllListeners)
	mux.HandleFunc("/admin/toggle-mount", s.handleToggleMount)
	mux.HandleFunc("/admin/toggle-visible", s.handleToggleVisible)
	mux.HandleFunc("/admin/update-fallback", s.handleUpdateFallback)
	mux.HandleFunc("/admin/add-user", s.handleAddUser)
	mux.HandleFunc("/admin/remove-user", s.handleRemoveUser)
	mux.HandleFunc("/admin/add-banned-ip", s.handleAddBannedIP)
	mux.HandleFunc("/admin/remove-banned-ip", s.handleRemoveBannedIP)
	mux.HandleFunc("/admin/add-whitelisted-ip", s.handleAddWhitelistedIP)
	mux.HandleFunc("/admin/remove-whitelisted-ip", s.handleRemoveWhitelistedIP)
	mux.HandleFunc("/admin/clear-auth-lockout", s.handleClearAuthLockout)
	mux.HandleFunc("/admin/clear-scan-lockout", s.handleClearScanLockout)
	mux.HandleFunc("/admin/add-webhook", s.handleAddWebhook)
	mux.HandleFunc("/admin/delete-webhook", s.handleDeleteWebhook)
	mux.HandleFunc("/admin/player/toggle", s.handlePlayerToggle)
	mux.HandleFunc("/admin/player/restart", s.handlePlayerRestart)
	mux.HandleFunc("/admin/player/scan", s.handlePlayerScan)
	mux.HandleFunc("/admin/player/clear-playlist", s.handlePlayerClearPlaylist)
	mux.HandleFunc("/admin/player/clear-queue", s.handlePlayerClearQueue)
	mux.HandleFunc("/admin/player/save-playlist", s.handlePlayerSavePlaylist)
	mux.HandleFunc("/admin/player/playlist-info", s.handlePlayerPlaylistInfo)
	mux.HandleFunc("/admin/player/load-playlist", s.handlePlayerLoadPlaylist)
	mux.HandleFunc("/admin/player/reorder", s.handlePlayerReorder)
	mux.HandleFunc("/admin/player/queue", s.handlePlayerQueue)
	mux.HandleFunc("/admin/player/shuffle", s.handlePlayerShuffle)
	mux.HandleFunc("/admin/player/loop", s.handlePlayerLoop)
	mux.HandleFunc("/admin/player/metadata", s.handlePlayerMetadata)
	mux.HandleFunc("/admin/player/next", s.handlePlayerNext)
	mux.HandleFunc("/admin/player/files", s.handlePlayerFiles)
	mux.HandleFunc("/admin/player/playlist-action", s.handlePlayerPlaylistAction)
	mux.HandleFunc("/admin/autodj/add", s.handleAddAutoDJ)
	mux.HandleFunc("/admin/autodj/delete", s.handleDeleteAutoDJ)
	mux.HandleFunc("/admin/autodj/toggle", s.handleToggleAutoDJ)
	mux.HandleFunc("/admin/autodj/studio", s.handleAutoDJStudio)
	mux.HandleFunc("/admin/autodj/update", s.handleUpdateAutoDJ)
	mux.HandleFunc("/admin/add-relay", s.handleAddRelay)
	mux.HandleFunc("/admin/toggle-relay", s.handleToggleRelay)
	mux.HandleFunc("/admin/restart-relay", s.handleRestartRelay)
	mux.HandleFunc("/admin/delete-relay", s.handleDeleteRelay)
	mux.HandleFunc("/admin/add-transcoder", s.handleAddTranscoder)
	mux.HandleFunc("/admin/toggle-transcoder", s.handleToggleTranscoder)
	mux.HandleFunc("/admin/delete-transcoder", s.handleDeleteTranscoder)
	mux.HandleFunc("/admin/transcoder-stats", s.handleTranscoderStats)
	mux.HandleFunc("/admin/security-stats", s.handleGetSecurityStats)
	mux.HandleFunc("/admin/history", s.handleHistory)
	mux.HandleFunc("/admin/statistics", s.handleGetStats)
	mux.HandleFunc("/admin/insights", s.handleInsights)
}

func (s *Server) registerSetupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/login", s.handleLogin)
	mux.HandleFunc("/logout", s.handleLogout)
	mux.HandleFunc("/setup", s.handleSetup)
	mux.HandleFunc("/setup/verify-token", s.handleSetupVerifyToken)
	mux.HandleFunc("/setup/complete", s.handleSetupComplete)
}

func (s *Server) registerAuthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/passkey/register/begin", s.handlePasskeyRegisterBegin)
	mux.HandleFunc("/api/passkey/register/finish", s.handlePasskeyRegisterFinish)
	mux.HandleFunc("/api/passkey/login/begin", s.handlePasskeyLoginBegin)
	mux.HandleFunc("/api/passkey/login/finish", s.handlePasskeyLoginFinish)
	mux.HandleFunc("/api/passkey", s.handlePasskeyDelete)
	mux.HandleFunc("/auth/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/auth/")
		if strings.HasSuffix(path, "/callback") {
			s.handleOIDCCallback(w, r)
			return
		}
		s.handleOIDCRedirect(w, r)
	})
	mux.HandleFunc("/api/oidc/providers", s.handleOIDCProvidersList)
	mux.HandleFunc("/api/pending-users", s.handleGetPendingUsers)
	mux.HandleFunc("/api/pending-users/approve", s.handleApprovePendingUser)
	mux.HandleFunc("/api/pending-users/deny", s.handleDenyPendingUser)
}

func (s *Server) registerPublicRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/explore", s.handleExplore)
	mux.HandleFunc("/webrtc/offer", s.handleWebRTCOffer)
	mux.HandleFunc("/webrtc/source-offer", s.handleWebRTCSourceOffer)
	mux.HandleFunc("/player/", s.handlePlayer)
	mux.HandleFunc("/player-webrtc/", s.handleWebRTCPlayer)
	mux.HandleFunc("/embed/", s.handleEmbed)
	mux.HandleFunc("/api/tenants", s.handleListTenants)
	mux.HandleFunc("/api/tenants/usage", s.handleTenantUsage)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !s.Config.SetupComplete {
			path := r.URL.Path
			if path == "/setup" || strings.HasPrefix(path, "/setup/") {
				switch path {
				case "/setup":
					s.handleSetup(w, r)
				case "/setup/verify-token":
					s.handleSetupVerifyToken(w, r)
				case "/setup/complete":
					s.handleSetupComplete(w, r)
				default:
					http.NotFound(w, r)
				}
				return
			}
			if !strings.HasPrefix(path, "/assets/") && !strings.HasPrefix(path, "/api/passkey/") {
				http.Redirect(w, r, "/setup", http.StatusTemporaryRedirect)
				return
			}
		}

		path := r.URL.Path
		if strings.HasSuffix(path, "/playlist.m3u8") {
			s.handleHLSPlaylist(w, r)
			return
		}
		if strings.HasSuffix(path, ".ts") && strings.Contains(path, "/segment-") {
			s.handleHLSSegment(w, r)
			return
		}
		s.handleRoot(w, r)
	})
	mux.HandleFunc("/events", s.handlePublicEvents)
	mux.HandleFunc("/events/metadata", s.handleMetadataEvents)
	mux.HandleFunc("/status-json.xsl", s.handleLegacyStats)
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.Handle("/assets/", http.StripPrefix("/assets/", s.shell.AssetHandler()))
	mux.HandleFunc("/developers", s.handleDevelopers)
}

func (s *Server) registerAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/streams", methodHandler(map[string]http.HandlerFunc{
		http.MethodGet:    s.apiGetStreams,
		http.MethodPost:   s.apiCreateStream,
		http.MethodDelete: s.apiDeleteStream,
	}))
	mux.HandleFunc("/api/streams/kick", s.apiKickStream)
	mux.HandleFunc("/api/streams/diagnostics", s.apiGetStreamDiagnostics)

	mux.HandleFunc("/api/autodj", methodHandler(map[string]http.HandlerFunc{
		http.MethodGet:    s.apiGetAutoDJ,
		http.MethodPost:   s.apiCreateAutoDJ,
		http.MethodDelete: s.apiDeleteAutoDJ,
	}))
	mux.HandleFunc("/api/autodj/play", s.apiAutoDJPlay)
	mux.HandleFunc("/api/autodj/pause", s.apiAutoDJPause)
	mux.HandleFunc("/api/autodj/next", s.apiAutoDJNext)
	mux.HandleFunc("/api/autodj/shuffle", s.apiAutoDJShuffle)
	mux.HandleFunc("/api/autodj/loop", s.apiAutoDJLoop)
	mux.HandleFunc("/api/autodj/playlist", methodHandler(map[string]http.HandlerFunc{
		http.MethodGet:    s.apiGetPlaylist,
		http.MethodPost:   s.apiAddToPlaylist,
		http.MethodDelete: s.apiRemoveFromPlaylist,
	}))
	mux.HandleFunc("/api/autodj/playlist/clear", s.apiClearPlaylist)
	mux.HandleFunc("/api/autodj/playlist/reorder", s.apiReorderPlaylist)
	mux.HandleFunc("/api/autodj/queue", methodHandler(map[string]http.HandlerFunc{
		http.MethodGet:  s.apiGetQueue,
		http.MethodPost: s.apiAddToQueue,
	}))
	mux.HandleFunc("/api/autodj/files", s.apiGetFiles)
	mux.HandleFunc("/api/autodj/", s.handleAutoDJMountAPI)

	mux.HandleFunc("/api/relays", methodHandler(map[string]http.HandlerFunc{
		http.MethodGet:    s.apiGetRelays,
		http.MethodPost:   s.apiCreateRelay,
		http.MethodDelete: s.apiDeleteRelay,
	}))
	mux.HandleFunc("/api/relays/toggle", s.apiToggleRelay)

	mux.HandleFunc("/api/transcoders", methodHandler(map[string]http.HandlerFunc{
		http.MethodGet:    s.apiGetTranscoders,
		http.MethodPost:   s.apiCreateTranscoder,
		http.MethodDelete: s.apiDeleteTranscoder,
	}))

	mux.HandleFunc("/api/users", methodHandler(map[string]http.HandlerFunc{
		http.MethodGet:    s.apiGetUsers,
		http.MethodPost:   s.apiCreateUser,
		http.MethodPut:    s.apiUpdateUser,
		http.MethodDelete: s.apiDeleteUser,
	}))

	mux.HandleFunc("/api/security/bans", methodHandler(map[string]http.HandlerFunc{
		http.MethodGet:    s.apiGetBans,
		http.MethodPost:   s.apiAddBan,
		http.MethodDelete: s.apiRemoveBan,
	}))
	mux.HandleFunc("/api/security/whitelist", methodHandler(map[string]http.HandlerFunc{
		http.MethodGet:    s.apiGetWhitelist,
		http.MethodPost:   s.apiAddWhitelist,
		http.MethodDelete: s.apiRemoveWhitelist,
	}))
	mux.HandleFunc("/api/security/audit", s.apiGetAuditLog)

	mux.HandleFunc("/api/branding", methodHandler(map[string]http.HandlerFunc{
		http.MethodGet: s.apiGetBranding,
		http.MethodPut: s.apiUpdateBranding,
	}))
	mux.HandleFunc("/api/settings", methodHandler(map[string]http.HandlerFunc{
		http.MethodGet: s.apiGetSettings,
		http.MethodPut: s.apiUpdateSettings,
	}))
	mux.HandleFunc("/api/stats", s.apiGetStats)
	mux.HandleFunc("/api/tokens", methodHandler(map[string]http.HandlerFunc{
		http.MethodGet:    s.apiGetTokens,
		http.MethodPost:   s.apiCreateToken,
		http.MethodDelete: s.apiDeleteToken,
	}))
	mux.HandleFunc("/api/branding/logo", s.apiUploadLogo)
	mux.HandleFunc("/branding/logo", s.handleServeLogo)
	mux.HandleFunc("/api/openapi.yaml", s.handleOpenAPISpec)
	mux.HandleFunc("/api/docs", s.handleSwaggerUI)
}

func (s *Server) handleAutoDJMountAPI(w http.ResponseWriter, r *http.Request) {
	rawPath := r.URL.RawPath
	if rawPath == "" {
		rawPath = r.URL.Path
	}
	path := strings.TrimPrefix(rawPath, "/api/autodj/")

	var mount, action string
	actionSuffixes := []string{
		"/playlist/add", "/playlist/remove", "/playlist/clear",
		"/playlist/reorder", "/playlist/playnext",
		"/playlist/save", "/playlist/load",
		"/files", "/playlist", "/queue",
		"/play", "/pause", "/next", "/shuffle", "/loop",
		"/metadata", "/volume",
	}
	for _, suffix := range actionSuffixes {
		if strings.HasSuffix(path, suffix) {
			encodedMount := strings.TrimSuffix(path, suffix)
			decoded, err := url.PathUnescape(encodedMount)
			if err != nil {
				decoded = encodedMount
			}
			if !strings.HasPrefix(decoded, "/") {
				decoded = "/" + decoded
			}
			mount = decoded
			action = strings.TrimPrefix(suffix, "/")
			break
		}
	}
	if mount == "" {
		http.NotFound(w, r)
		return
	}

	q := r.URL.Query()
	q.Set("mount", mount)
	r.URL.RawQuery = q.Encode()

	switch action {
	case "files":
		s.apiGetFiles(w, r)
	case "playlist":
		methodHandler(map[string]http.HandlerFunc{
			http.MethodGet:    s.apiGetPlaylist,
			http.MethodPost:   s.apiAddToPlaylist,
			http.MethodDelete: s.apiRemoveFromPlaylist,
		})(w, r)
	case "playlist/add":
		s.apiAddToPlaylist(w, r)
	case "playlist/remove":
		s.apiRemoveFromPlaylist(w, r)
	case "playlist/clear":
		s.apiClearPlaylist(w, r)
	case "playlist/reorder":
		s.apiReorderPlaylist(w, r)
	case "playlist/playnext":
		s.apiAddToQueue(w, r)
	case "playlist/save":
		s.handlePlayerSavePlaylist(w, r)
	case "playlist/load":
		s.handlePlayerLoadPlaylist(w, r)
	case "queue":
		methodHandler(map[string]http.HandlerFunc{
			http.MethodGet:  s.apiGetQueue,
			http.MethodPost: s.apiAddToQueue,
		})(w, r)
	case "play":
		s.apiAutoDJPlay(w, r)
	case "pause":
		s.apiAutoDJPause(w, r)
	case "next":
		s.apiAutoDJNext(w, r)
	case "shuffle":
		s.apiAutoDJShuffle(w, r)
	case "loop":
		s.apiAutoDJLoop(w, r)
	case "metadata", "volume":
		s.handlePlayerMetadata(w, r)
	default:
		http.NotFound(w, r)
	}
}
