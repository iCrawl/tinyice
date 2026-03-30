package server

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/logger"
	"github.com/DatanoiseTV/tinyice/relay"
)

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	logger.L.Debugw("Root handler request", "method", r.Method, "path", r.URL.Path)
	if r.Method == "PUT" || r.Method == "SOURCE" {
		s.handleSource(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, ".m3u8") || strings.HasSuffix(r.URL.Path, ".m3u") {
		s.handlePlaylist(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, ".pls") {
		s.handlePLS(w, r)
		return
	}
	// Known app paths — serve the appropriate page, not a stream listener
	path := r.URL.Path
	if strings.HasPrefix(path, "/admin") {
		// All /admin/* paths serve the admin SPA shell
		s.handleAdmin(w, r)
		return
	}

	// Only treat as stream listener if it's not a known app route
	appPrefixes := []string{"/login", "/logout", "/setup", "/auth/", "/api/", "/explore", "/developers", "/assets/", "/events", "/player", "/embed/", "/webrtc"}
	isAppRoute := path == "/" || path == "/favicon.ico"
	for _, prefix := range appPrefixes {
		if strings.HasPrefix(path, prefix) || path == prefix {
			isAppRoute = true
			break
		}
	}

	if r.Method == "GET" && !isAppRoute {
		s.handleListener(w, r)
		return
	}
	s.handleStatus(w, r)
}

func (s *Server) handlePLS(w http.ResponseWriter, r *http.Request) {
	mount := strings.TrimSuffix(r.URL.Path, ".pls")
	st, ok := s.Relay.GetStream(mount)
	if !ok {
		http.NotFound(w, r)
		return
	}

	baseURL := s.Config.BaseURL
	if baseURL == "" {
		proto := "http://"
		if s.Config.UseHTTPS || r.Header.Get("X-Forwarded-Proto") == "https" {
			proto = "https://"
		}
		baseURL = proto + r.Host
	}
	baseURL = strings.TrimSuffix(baseURL, "/")

	w.Header().Set("Content-Type", "audio/x-scpls")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.pls\"", st.Name))
	fmt.Fprintf(w, "[playlist]\nNumberOfEntries=1\nFile1=%s%s\nTitle1=%s\nLength1=-1\nVersion=2\n", baseURL, mount, st.Name)
}

func (s *Server) handlePlaylist(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	ext := ".m3u8"
	if strings.HasSuffix(path, ".m3u") {
		ext = ".m3u"
	}
	mount := strings.TrimSuffix(path, ext)

	st, ok := s.Relay.GetStream(mount)
	if !ok {
		http.NotFound(w, r)
		return
	}

	baseURL := s.Config.BaseURL
	if baseURL == "" {
		proto := "http://"
		if s.Config.UseHTTPS || r.Header.Get("X-Forwarded-Proto") == "https" {
			proto = "https://"
		}
		baseURL = proto + r.Host
	}
	baseURL = strings.TrimSuffix(baseURL, "/")

	w.Header().Set("Content-Type", "audio/x-mpegurl")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s%s\"", st.Name, ext))
	fmt.Fprintf(w, "#EXTM3U\n#EXTINF:-1,%s\n%s%s\n", st.Name, baseURL, mount)
}

func (s *Server) handleSource(w http.ResponseWriter, r *http.Request) {
	if s.isBanned(r.RemoteAddr) {
		logger.L.Warnw("Banned IP source connection", "ip", r.RemoteAddr)
		return
	}
	mount := r.URL.Path
	requiredPass, found := s.getSourcePassword(mount)
	if !found {
		requiredPass = s.Config.DefaultSourcePassword
	}

	if s.Config.DisabledMounts[mount] {
		logger.L.Warnw("Disabled mount connection", "mount", mount)
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	_, p, ok := r.BasicAuth()
	if !ok || !config.CheckPasswordHash(p, requiredPass) {
		u, _, _ := r.BasicAuth()
		if u == "" {
			u = "unknown"
		}
		s.logAuthFailed(u, r.RemoteAddr, "source password mismatch")
		w.Header().Set("WWW-Authenticate", `Basic realm="Icecast"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	s.logAuth().Infow("Source auth successful", "mount", mount, "ip", host)

	if s.Relay.History != nil {
		s.Relay.History.RecordUA(r.Header.Get("User-Agent"), "source")
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Hijacking unsupported", http.StatusInternalServerError)
		return
	}
	conn, bufrw, err := hj.Hijack()
	if err != nil {
		logger.L.Errorf("Hijack failed: %v", err)
		return
	}
	defer conn.Close()

	bufrw.WriteString("HTTP/1.0 200 OK\r\nServer: Icecast 2.4.4\r\nConnection: Keep-Alive\r\n\r\n")
	bufrw.Flush()

	logger.L.Infow("Source connected", "mount", mount, "ip", r.RemoteAddr, "ua", r.Header.Get("User-Agent"))
	s.Relay.Diagnostics.Record(relay.DiagnosticUpdate{
		Mount:     mount,
		Status:    relay.DiagnosticStatusRunning,
		Class:     relay.DiagnosticClassRecoverySucceeded,
		Reason:    "source connected",
		Actor:     relay.DiagnosticActorIcecastSource,
		Timestamp: time.Now(),
	})
	s.dispatchWebhook("source_connect", map[string]interface{}{
		"mount": mount,
		"ip":    r.RemoteAddr,
		"ua":    r.Header.Get("User-Agent"),
		"name":  r.Header.Get("Ice-Name"),
	})

	tenantID := "default"
	if tenant := TenantFromContext(r.Context()); tenant != nil {
		tenantID = tenant.ID
	}

	stream := s.Relay.GetOrCreateStream(mount)
	rt := s.RuntimeRegistry.GetOrCreate(mount)
	rt.Stream = stream
	s.RuntimeRegistry.AttachSource(mount, relay.SourceIcecast, tenantID)
	defer s.RuntimeRegistry.Remove(mount)
	stream.SourceIP = r.RemoteAddr

	s.updateSourceMetadata(stream, mount, r)

	buf := make([]byte, 8192)
	for {
		n, err := bufrw.Read(buf)
		if n > 0 {
			stream.Broadcast(buf[:n], s.Relay)
		}
		if err != nil {
			break
		}
	}
	logger.L.Infow("Source disconnected", "mount", mount)
	s.Relay.Diagnostics.Record(relay.DiagnosticUpdate{
		Mount:     mount,
		Status:    relay.DiagnosticStatusStopped,
		Class:     relay.DiagnosticClassSourceDisconnect,
		Reason:    "source disconnected",
		Actor:     relay.DiagnosticActorIcecastSource,
		Timestamp: time.Now(),
	})
	s.dispatchWebhook("source_disconnect", map[string]interface{}{
		"mount": mount,
	})
	s.Relay.RemoveStream(mount)
}

func (s *Server) updateSourceMetadata(stream *relay.Stream, mount string, r *http.Request) {
	bitrate := r.Header.Get("Ice-Bitrate")
	if bitrate == "" || bitrate == "N/A" {
		audioInfo := r.Header.Get("Ice-Audio-Info")
		if audioInfo != "" {
			parts := strings.Split(audioInfo, ";")
			for _, part := range parts {
				if strings.HasPrefix(strings.TrimSpace(part), "bitrate=") {
					bitrate = strings.TrimPrefix(strings.TrimSpace(part), "bitrate=")
					break
				}
			}
		}
	}
	isPublic := r.Header.Get("Ice-Public") == "1"
	isVisible := s.Config.VisibleMounts[mount]
	if stream.UpdateMetadata(r.Header.Get("Ice-Name"), r.Header.Get("Ice-Description"), r.Header.Get("Ice-Genre"), r.Header.Get("Ice-Url"), bitrate, r.Header.Get("Content-Type"), isPublic, isVisible) {
		s.dispatchWebhook("metadata_update", map[string]interface{}{
			"mount":        mount,
			"name":         stream.Name,
			"description":  stream.Description,
			"genre":        stream.Genre,
			"current_song": stream.CurrentSong,
		})
	}
}

func (s *Server) effectiveMountListenerLimit(mount string) int {
	if ms, ok := s.Config.AdvancedMounts[mount]; ok && ms.MaxListeners > 0 {
		return ms.MaxListeners
	}
	return s.Config.MaxListeners
}

func (s *Server) newHTTPListener(r *http.Request, requestedMount, currentMount string) *relay.Listener {
	now := time.Now()
	return &relay.Listener{
		ID:                 fmt.Sprintf("http-%d", now.UnixNano()),
		Protocol:           relay.ListenerProtocolHTTP,
		RequestedMount:     requestedMount,
		CurrentMount:       currentMount,
		RemoteAddr:         r.RemoteAddr,
		UserAgent:          r.Header.Get("User-Agent"),
		Connected:          now,
		LastStreamSwitchAt: now,
		DisconnectCh:       make(chan struct{}),
		MoveCh:             make(chan relay.ListenerCommand, 1),
	}
}

type listenerLoopResult struct {
	NextMount      string
	RequestedMount string
	KeepGoing      bool
}

func (s *Server) handleListener(w http.ResponseWriter, r *http.Request) {
	if s.isBanned(r.RemoteAddr) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	requestedMount := r.URL.Path
	mount := requestedMount

	if s.Relay.History != nil {
		s.Relay.History.RecordUA(r.Header.Get("User-Agent"), "listener")
	}

	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if s.Config.LowLatencyMode {
		w.Header().Set("X-Accel-Buffering", "no")
	}

	flusher, _ := w.(http.Flusher)
	logger.L.Infow("Listener connected", "mount", mount, "ip", r.RemoteAddr, "ua", r.Header.Get("User-Agent"))
	defer logger.L.Infow("Listener disconnected", "mount", mount, "ip", r.RemoteAddr)
	listener := s.newHTTPListener(r, requestedMount, mount)
	registered := false
	defer func() {
		if registered {
			s.Relay.Listeners.Unregister(listener.ID)
		}
	}()

	recoveryTicker := time.NewTicker(10 * time.Second)
	defer recoveryTicker.Stop()

	var primaryFirstSeen time.Time
	const fallbackHysteresis = 30 * time.Second

	for {
		select {
		case <-s.done:
			return
		default:
		}

		if mount != requestedMount {
			if _, ok := s.Relay.GetStream(requestedMount); ok {
				if primaryFirstSeen.IsZero() {
					primaryFirstSeen = time.Now()
				}
				if time.Since(primaryFirstSeen) >= fallbackHysteresis {
					logger.L.Infow("Primary stream stable, recovering from fallback",
						"mount", requestedMount,
						"stable_for", time.Since(primaryFirstSeen),
					)
					mount = requestedMount
					primaryFirstSeen = time.Time{}
				}
			} else {
				primaryFirstSeen = time.Time{}
			}
		}

		stream, ok := s.Relay.GetStream(mount)
		if !ok {
			fallback, hasFallback := s.Config.FallbackMounts[mount]
			if hasFallback && fallback != mount {
				logger.L.Infow("Primary stream down, falling back", "from", mount, "to", fallback)
				mount = fallback
				continue
			}
			if mount != requestedMount {
				mount = requestedMount
				time.Sleep(1 * time.Second)
				continue
			}
			host, _, _ := net.SplitHostPort(r.RemoteAddr)
			s.recordScanAttempt(host, requestedMount)
			http.NotFound(w, r)
			return
		}

		metaint := 0
		if r.Header.Get("Icy-MetaData") == "1" && !stream.IsOgg() {
			metaint = 16000
			w.Header().Set("icy-metaint", "16000")
			w.Header().Set("icy-name", s.Config.PageTitle)
		}

		limit := s.effectiveMountListenerLimit(mount)
		if !registered {
			if limit > 0 && s.Relay.Listeners.CountForMount(mount) >= limit {
				http.Error(w, "Server Full", http.StatusServiceUnavailable)
				return
			}
			listener.SetRequestedMount(requestedMount)
			listener.SetCurrentMount(mount)
			s.Relay.Listeners.Register(listener)
			registered = true
		} else if listener.CurrentMountName() != mount {
			currentMount := listener.CurrentMountName()
			if limit > 0 && s.Relay.Listeners.CountForMount(mount) >= limit {
				logger.L.Warnw("Listener move rejected by mount cap", "id", listener.ID, "from", currentMount, "to", mount)
				mount = currentMount
				continue
			}
			listener.SetRequestedMount(requestedMount)
			if err := s.Relay.Listeners.Move(listener.ID, mount, time.Now()); err != nil {
				logger.L.Warnw("Listener move failed", "id", listener.ID, "from", currentMount, "to", mount, "error", err)
				mount = currentMount
				continue
			}
		}

		w.Header().Set("Content-Type", stream.ContentType)
		if mount != requestedMount {
			w.Header().Set("X-Stream-Status", "fallback")
		} else {
			w.Header().Set("X-Stream-Status", "primary")
		}
		if flusher != nil {
			flusher.Flush()
		}

		result := s.serveStreamData(w, r, stream, listener, requestedMount, mount, recoveryTicker, metaint)
		if !result.KeepGoing {
			return
		}
		requestedMount = result.RequestedMount
		mount = result.NextMount
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *Server) serveStreamData(w http.ResponseWriter, r *http.Request, stream *relay.Stream, listener *relay.Listener, requestedMount, currentMount string, recoveryTicker *time.Ticker, metaint int) listenerLoopResult {
	listener.SetCurrentMount(currentMount)
	offset := stream.SubscribeListener(listener, 128*1024)
	signal := listener.Signal
	defer stream.Unsubscribe(listener.ID)

	if stream.OggHead != nil {
		if _, err := w.Write(stream.OggHead); err != nil {
			return listenerLoopResult{}
		}
		logger.L.Debugf("Ogg Listener %s: Sending stored headers (%d bytes), then starting burst at %d", listener.ID, len(stream.OggHead), offset)
	}

	// was 16384
	buf := make([]byte, 4096)
	flusher, _ := w.(http.Flusher)

	bytesSentSinceMeta := 0
	lastSong := ""

	consecutiveSkips := 0
	maxConsecutiveSkips := 5

	for {
		select {
		case <-s.done:
			return listenerLoopResult{}
		case <-r.Context().Done():
			return listenerLoopResult{}
		case <-listener.DisconnectCh:
			return listenerLoopResult{}
		case cmd := <-listener.MoveCh:
			target := cmd.TargetMount
			if target == "" || target == currentMount {
				continue
			}
			listener.SetRequestedMount(target)
			return listenerLoopResult{
				NextMount:      target,
				RequestedMount: target,
				KeepGoing:      true,
			}
		case <-recoveryTicker.C:
			if currentMount != requestedMount {
				if _, ok := s.Relay.GetStream(requestedMount); ok {
					return listenerLoopResult{
						NextMount:      requestedMount,
						RequestedMount: requestedMount,
						KeepGoing:      true,
					}
				}
			}
		case _, ok := <-signal:
			if !ok {
				return listenerLoopResult{
					NextMount:      currentMount,
					RequestedMount: requestedMount,
					KeepGoing:      true,
				}
			}
			for {
				readLimit := len(buf)
				if metaint > 0 {
					remaining := metaint - bytesSentSinceMeta
					if remaining < readLimit {
						readLimit = remaining
					}
				}

				n, next, skipped := stream.Buffer.ReadAt(offset, buf[:readLimit])
				if skipped && stream.IsOggStream {
					consecutiveSkips++
					if consecutiveSkips >= maxConsecutiveSkips {
						logger.L.Warnw("Slow listener disconnected (ogg sync skip)",
							"id", listener.ID, "mount", currentMount,
							"consecutive_skips", consecutiveSkips,
						)
						return listenerLoopResult{}
					}
					offset = relay.FindNextPageBoundary(stream.Buffer.Data, stream.Buffer.Size, stream.Buffer.Head, next)
					continue
				}
				if n == 0 {
					break
				}
				if skipped {
					stream.RecordDroppedBytes(next - offset)
					consecutiveSkips++
					if consecutiveSkips >= maxConsecutiveSkips {
						logger.L.Warnw("Slow listener disconnected",
							"id", listener.ID, "mount", currentMount,
							"consecutive_skips", consecutiveSkips,
						)
						return listenerLoopResult{}
					}
				} else {
					consecutiveSkips = 0
				}
				offset = next

				if _, err := w.Write(buf[:n]); err != nil {
					return listenerLoopResult{}
				}

				if metaint > 0 {
					bytesSentSinceMeta += n
					if bytesSentSinceMeta >= metaint {
						currentSong := stream.GetCurrentSong()
						meta := ""
						if currentSong != lastSong {
							meta = fmt.Sprintf("StreamTitle='%s';", currentSong)
							lastSong = currentSong
						}

						l := (len(meta) + 15) / 16
						res := make([]byte, 1+l*16)
						res[0] = byte(l)
						copy(res[1:], meta)

						if _, err := w.Write(res); err != nil {
							return listenerLoopResult{}
						}
						bytesSentSinceMeta = 0
					}
				}

				atomic.AddInt64(&s.Relay.BytesOut, int64(n))
				atomic.AddInt64(&stream.BytesOut, int64(n))
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	// Build stream list for the landing page
	allStreams := s.Relay.Snapshot()
	var streamList []map[string]interface{}
	for _, st := range allStreams {
		if st.Visible {
			streamList = append(streamList, map[string]interface{}{
				"mount":     st.MountName,
				"title":     st.CurrentSong,
				"artist":    st.Name,
				"format":    st.ContentType,
				"bitrate":   st.Bitrate,
				"listeners": st.ListenersCount,
				"live":      st.SourceIP != "",
			})
		}
	}

	pageData := s.BasePageData("")
	pageData["streams"] = streamList
	s.shell.Render(w, "landing", s.Config.PageTitle, pageData)
}
