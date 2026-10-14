package server

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
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
	appPrefixes := []string{"/login", "/logout", "/setup", "/auth/", "/api/", "/explore", "/developers", "/assets/", "/events", "/player", "/embed/", "/webrtc", "/kiosk"}
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

// sourceHasFramedBody reports whether the request carries a body that
// net/http can frame and decode on its own. Classic Icecast source
// clients send neither Content-Length nor Transfer-Encoding (ContentLength
// == 0, empty TransferEncoding); anything else — a length, or chunked,
// which is what a reverse proxy produces — can and must be read via
// r.Body so the framing is stripped.
func sourceHasFramedBody(r *http.Request) bool {
	if len(r.TransferEncoding) > 0 {
		return true
	}
	return r.ContentLength != 0
}

func (s *Server) handleSource(w http.ResponseWriter, r *http.Request) {
	clientIP := s.clientIP(r)
	if s.isBanned(clientIP) {
		logger.L.Warnw("Banned IP source connection", "ip", clientIP)
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

	// Same lockout the login form and the WebRTC source endpoint apply.
	// This path had none, so the source password — which grants the
	// ability to broadcast on the station — could be brute-forced at
	// line rate with no consequence.
	if err := s.checkAuthLimit(clientIP); err != nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="Icecast"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	_, p, ok := r.BasicAuth()
	if !ok || !config.CheckPasswordHash(p, requiredPass) {
		u, _, _ := r.BasicAuth()
		if u == "" {
			u = "unknown"
		}
		s.recordAuthFailure(clientIP)
		s.logAuthFailed(u, clientIP, "source password mismatch")
		w.Header().Set("WWW-Authenticate", `Basic realm="Icecast"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	s.recordAuthSuccess(clientIP)

	s.logAuth().Infow("Source auth successful", "mount", mount, "ip", clientIP)

	if s.Relay.History != nil {
		s.Relay.History.RecordUA(r.Header.Get("User-Agent"), "source")
	}

	// Single-source enforcement. Without this, robodj-class clients
	// that retry on connect (we've observed 3 SOURCE attempts within
	// 25 ms after a restart) all get accepted concurrently. Two write
	// loops then interleave bytes into the same ring buffer and every
	// downstream MP3 / Ogg decoder errors out on missing frame
	// headers. Claim BEFORE the hijack so we can return a clean 403
	// for the loser; release in defer so the next reconnect (after
	// the current source drops) succeeds.
	stream := s.Relay.GetOrCreateStream(mount)
	sourceToken, claimed := stream.ClaimSource(clientIP)
	if !claimed {
		existing := stream.GetSourceIP()
		logger.L.Warnw("Source rejected: mount already has an active source",
			"mount", mount, "new_ip", clientIP, "existing_ip", existing)
		http.Error(w, "Mount already has an active source", http.StatusForbidden)
		return
	}
	defer stream.ReleaseSourceIfCurrent(sourceToken)

	// Per-read deadline on the source. Without it, a source that
	// silently drops (network blip, encoder crash without FIN)
	// holds the handler goroutine in Read forever; the stream
	// stays mounted, transcoders keep their input subscription, and
	// the next reconnect from the same encoder leaves zombies. The
	// deadline is refreshed after every successful read so a
	// continuously-streaming source is never penalised — only
	// genuinely silent connections die.
	const sourceReadTimeout = 60 * time.Second

	// How the body is read depends on how the client framed it.
	//
	// A classic Icecast source (BUTT, darkice, ices) sends SOURCE/PUT with
	// neither Content-Length nor Transfer-Encoding and then streams audio
	// forever. That is not valid HTTP/1.1 framing, so net/http hands us an
	// empty r.Body — hence the hijack, which reads the raw socket.
	//
	// But when the client is framing correctly (chunked, or a length),
	// hijacking is wrong: the raw socket then still carries the chunk
	// headers, and they get broadcast to listeners as if they were audio
	// (we measured "d08\r\n" landing in the middle of an MP3 stream).
	// That is exactly what a reverse proxy in front of us produces, since
	// it re-frames the request before forwarding. In that case let
	// net/http do the de-chunking and read r.Body.
	var src io.Reader
	var setReadDeadline func(time.Time) error

	// Let an admin kick (or a mount being disabled) terminate this
	// connection: without it the encoder keeps streaming and recreates
	// the mount on its next write.
	var closeOnce sync.Once
	if sourceHasFramedBody(r) {
		// NOTE: deliberately no Flush here. Flushing the response
		// before the request body has been read blocks in net/http
		// until the client goes away (measured: ~7 s, i.e. the client's
		// whole lifetime), which starved the ingest completely. The
		// header goes out with the first write / on return; a client
		// that framed its body is already streaming and is not waiting
		// on our 200.
		w.Header().Set("Server", "Icecast 2.4.4")
		w.WriteHeader(http.StatusOK)
		rc := http.NewResponseController(w)
		src = r.Body
		setReadDeadline = rc.SetReadDeadline
		defer r.Body.Close()
		stream.SetSourceCloserIfCurrent(sourceToken, func() { closeOnce.Do(func() { _ = r.Body.Close() }) })
	} else {
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
		src = bufrw
		setReadDeadline = conn.SetReadDeadline
		stream.SetSourceCloserIfCurrent(sourceToken, func() { closeOnce.Do(func() { _ = conn.Close() }) })
	}
	defer stream.SetSourceCloserIfCurrent(sourceToken, nil)

	logger.L.Infow("Source connected", "mount", mount, "ip", clientIP, "ua", r.Header.Get("User-Agent"))
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
		"ip":    clientIP,
		"ua":    r.Header.Get("User-Agent"),
		"name":  r.Header.Get("Ice-Name"),
	})

	tenantID := "default"
	if tenant := TenantFromContext(r.Context()); tenant != nil {
		tenantID = tenant.ID
	}
	if s.RuntimeRegistry != nil {
		s.RuntimeRegistry.AttachClaim(stream, sourceToken, relay.SourceIcecast, tenantID)
	}
	s.updateSourceMetadata(stream, mount, r)
	logger.L.Infow("Source bitrate headers", "mount", mount,
		"ice_bitrate", r.Header.Get("Ice-Bitrate"),
		"ice_audio_info", r.Header.Get("Ice-Audio-Info"),
		"content_type", r.Header.Get("Content-Type"))

	// Late-joining listeners on Ogg-based mounts (Vorbis / Opus / FLAC-in-Ogg)
	// need the BOS + comment/setup pages prepended so they can initialise the
	// codec. Capture those pages while forwarding the byte stream.
	captureHeaders := isOggContentType(r.Header.Get("Content-Type")) || stream.IsOgg()
	var headerBuf []byte
	captureStartOffset := stream.Buffer.HeadOffset()

	// Vorbis/Opus sources put TITLE/ARTIST in the Ogg comment header, not
	// in an ICY metadata request; feed history from there.
	var sniffer *relay.OggCommentSniffer
	if captureHeaders {
		sniffer = relay.NewOggCommentSniffer(func(song string) { stream.SetCurrentSong(song, s.Relay) })
	}

	buf := make([]byte, 8192)
	var totalIn atomic.Int64
	ingestStart := time.Now()
	bitrateMeasured := false

	// Zero-data watchdog. A source that authenticates and then delivers
	// nothing is nearly always an HTTP reverse proxy in front of us: the
	// classic Icecast source protocol sends neither Content-Length nor
	// Transfer-Encoding, so the proxy sees a bodiless request, forwards
	// no body, and the audio never leaves the proxy. Waiting for the
	// read deadline to surface that takes a minute, by which time most
	// encoders have already reconnected — so say it while the operator
	// is still looking.
	watchdogDone := make(chan struct{})
	defer close(watchdogDone)
	go func() {
		select {
		case <-watchdogDone:
			return
		case <-time.After(10 * time.Second):
		}
		if totalIn.Load() > 0 {
			return
		}
		logger.L.Warnw("Source connected but has sent no audio after 10s",
			"mount", mount,
			"ip", clientIP,
			"ua", r.Header.Get("User-Agent"),
			"framed_body", sourceHasFramedBody(r),
			"hint", "if this encoder reaches TinyIce through an HTTP reverse proxy (Caddy/nginx), the proxy drops the body of a classic Icecast SOURCE request — point the encoder straight at TinyIce (it can terminate TLS itself) or proxy the port at TCP/stream level instead of HTTP",
		)
	}()

	// Report a ResponseWriter that can't carry a read deadline once,
	// rather than silently running the ingest with no idle timeout: a
	// wrapper that doesn't implement Unwrap makes every call here fail
	// with ErrNotSupported and there is no other symptom until a
	// half-open source parks the goroutine forever.
	deadlineWarned := false
	for {
		if setReadDeadline != nil {
			if err := setReadDeadline(time.Now().Add(sourceReadTimeout)); err != nil && !deadlineWarned {
				deadlineWarned = true
				logger.L.Warnw("Source: read deadline unsupported on this connection; a silent source will not time out",
					"mount", mount, "error", err)
			}
		}
		n, err := src.Read(buf)
		if n > 0 {
			totalIn.Add(int64(n))
			// Sources that declare no bitrate (Traktor over Ogg Vorbis) would
			// show 0k; after a few seconds, publish the measured rate instead.
			if elapsed := time.Since(ingestStart); !bitrateMeasured && elapsed >= 10*time.Second {
				bitrateMeasured = true
				kbps := int(float64(totalIn.Load()) * 8 / 1000 / elapsed.Seconds())
				if stream.SetBitrateIfUnknown(kbps) {
					logger.L.Infow("Source declared no bitrate; using measured rate", "mount", mount, "kbps", kbps)
				}
			}
			if !s.Relay.PublishSource(stream, sourceToken, buf[:n]) {
				break
			}
			if sniffer != nil {
				sniffer.Feed(buf[:n])
			}
			if captureHeaders {
				headerBuf = append(headerBuf, buf[:n]...)
				endPos, needMore, abort := relay.FindOggHeaderEnd(headerBuf)
				if abort {
					captureHeaders = false
					headerBuf = nil
				} else if !needMore {
					headers := make([]byte, endPos)
					copy(headers, headerBuf[:endPos])
					stream.StoreOggHead(headers, captureStartOffset+int64(endPos))
					logger.L.Infow("Source: Captured Ogg headers",
						"mount", mount,
						"bytes", len(headers),
						"offset", captureStartOffset+int64(endPos),
					)
					captureHeaders = false
					headerBuf = nil
				}
			}
		}
		if err != nil {
			break
		}
	}
	if stream.IsCurrentSource(sourceToken) {
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
		if s.RuntimeRegistry != nil {
			s.RuntimeRegistry.DetachClaim(stream, sourceToken)
		}
		s.Relay.RemoveStreamIfCurrent(stream, sourceToken)
	} else {
		logger.L.Infow("Stale source disconnected; keeping successor stream", "mount", mount)
	}
}

// looksLikeUnknownMount reports whether a 404 for this path should count
// toward the vuln-scanner lockout. Anything that is configured as a stream
// (even if no source is currently connected) is a legitimate listener URL;
// browser prefetch paths with static file extensions are also ignored.
func (s *Server) looksLikeUnknownMount(mount string) bool {
	// Known configured mount — always legitimate.
	if _, ok := s.Config.Mounts[mount]; ok {
		return false
	}
	if _, ok := s.Config.AdvancedMounts[mount]; ok {
		return false
	}
	if _, ok := s.Config.VisibleMounts[mount]; ok {
		return false
	}
	if _, ok := s.Config.FallbackMounts[mount]; ok {
		return false
	}
	for _, u := range s.Config.Users {
		if _, ok := u.Mounts[mount]; ok {
			return false
		}
	}
	// Browser prefetch paths — not a stream attempt.
	lower := strings.ToLower(mount)
	if strings.HasPrefix(lower, "/.well-known/") {
		return false
	}
	staticSuffixes := []string{
		".ico", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp",
		".css", ".js", ".map", ".json", ".xml", ".txt", ".webmanifest",
		".woff", ".woff2", ".ttf",
	}
	for _, ext := range staticSuffixes {
		if strings.HasSuffix(lower, ext) {
			return false
		}
	}
	return true
}

// isOggContentType returns true if the given HTTP Content-Type indicates an
// Ogg-container stream (Vorbis, Opus, FLAC-in-Ogg, etc.).
func isOggContentType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if ct == "" {
		return false
	}
	// Strip any "; charset=..." suffix.
	if idx := strings.Index(ct, ";"); idx >= 0 {
		ct = strings.TrimSpace(ct[:idx])
	}
	switch ct {
	case "application/ogg", "audio/ogg", "audio/opus", "audio/vorbis":
		return true
	}
	// Also recognise vendor-specific shapes like audio/ogg; codecs=opus.
	if strings.HasPrefix(ct, "audio/ogg") || strings.HasPrefix(ct, "application/ogg") {
		return true
	}
	return false
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

	if !isAlreadyMP3(r.Header.Get("Content-Type")) {
		s.TranscoderM.EnsureAutoMP3Transcoders(mount, s.Config.AutoTranscodeMP3Bitrates, s.Config.Transcoders)
	}

	// Per-mount NameOverride from AdvancedMounts: rebrand a stream
	// without poking the encoder. Empty / unset = use the source's
	// Ice-Name as before.
	iceName := r.Header.Get("Ice-Name")
	if adv, ok := s.Config.AdvancedMounts[mount]; ok && adv != nil && adv.NameOverride != "" {
		iceName = adv.NameOverride
	}

	if stream.UpdateMetadata(iceName, r.Header.Get("Ice-Description"), r.Header.Get("Ice-Genre"), r.Header.Get("Ice-Url"), bitrate, r.Header.Get("Content-Type"), isPublic, isVisible) {
		s.dispatchWebhook("metadata_update", map[string]interface{}{
			"mount":        mount,
			"name":         stream.Name,
			"description":  stream.Description,
			"genre":        stream.Genre,
			"current_song": stream.CurrentSong,
		})
	}
}

// visibleStreamList builds the StreamInfo array the landing and explore
// pages are bootstrapped with. Both pages used to build it separately and
// had drifted: explore's copy omitted has_video, so a video mount rendered
// there as audio-only and its player opened without picture.
func (s *Server) visibleStreamList() []map[string]interface{} {
	allStreams := s.Relay.Snapshot()
	videoMounts := make(map[string]bool)
	for _, st := range allStreams {
		if strings.HasSuffix(st.MountName, "/video") {
			videoMounts[strings.TrimSuffix(st.MountName, "/video")] = true
		}
	}
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
				"has_video": videoMounts[st.MountName],
			})
		}
	}
	return streamList
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	pageData := s.BasePageData("")
	pageData["streams"] = s.visibleStreamList()
	s.shell.Render(w, "landing", s.Config.PageTitle, pageData)
}

// isAlreadyMP3 returns true if the Content-Type header advertises an MP3
// stream — an auto MP3 transcoder of an mp3 source is wasted CPU.
func isAlreadyMP3(contentType string) bool {
	ct := strings.ToLower(contentType)
	return ct == "audio/mpeg" || ct == "audio/mp3" || strings.HasPrefix(ct, "audio/mpeg;")
}
