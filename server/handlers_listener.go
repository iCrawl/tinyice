package server

import (
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/logger"
	"github.com/DatanoiseTV/tinyice/relay"
)

func (s *Server) handleListener(w http.ResponseWriter, r *http.Request) {
	clientIP := s.clientIP(r)
	if s.isBanned(clientIP) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	// A disabled mount refuses sources (handleSource checks the same
	// map); it must refuse listeners too, or "disabled" only means
	// "nobody new may broadcast".
	if s.Config.DisabledMounts[r.URL.Path] {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	originalMount := r.URL.Path
	mount := originalMount

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
	// The subscriber id must stay unique per CONNECTION, so it keeps the
	// raw RemoteAddr (which carries the peer port); only the logged/geo
	// identity is resolved through the trusted-proxy chain.
	id := r.RemoteAddr + "-" + fmt.Sprintf("%d", time.Now().UnixNano())
	listener := &relay.Listener{
		ID:             id,
		Protocol:       relay.ListenerProtocolHTTP,
		RequestedMount: originalMount,
		CurrentMount:   mount,
		RemoteAddr:     r.RemoteAddr,
		UserAgent:      r.Header.Get("User-Agent"),
		Connected:      time.Now(),
		DisconnectCh:   make(chan struct{}),
		MoveCh:         make(chan relay.ListenerCommand, 1),
	}
	registered := false
	logger.L.Infow("Listener connected", "mount", mount, "ip", r.RemoteAddr, "ua", r.Header.Get("User-Agent"))
	defer func() {
		if registered {
			s.Relay.Listeners.Unregister(listener.ID)
		}
		logger.L.Infow("Listener disconnected", "mount", mount, "ip", r.RemoteAddr)
	}()

	recoveryTicker := time.NewTicker(10 * time.Second)
	defer recoveryTicker.Stop()

	var primaryFirstSeen time.Time
	icy := &icyState{}

	// served flips once headers + data have gone to this client. It
	// decides what "both primary and fallback are down" means: for a
	// listener mid-stream we wait for either to return (they already
	// have a response; players don't reconnect on their own), but for a
	// fresh connection we answer 404 like a fallback-less mount would.
	// Previously every connection took the wait path, so a station with
	// a fallback configured and both mounts down held every listener
	// request open forever with no headers, never noticed the client
	// hanging up (the loop checked s.done but not r.Context()), and
	// parked one goroutine plus a ticker per player reconnect.
	served := false
	var outageSince time.Time
	const maxOutageWait = 2 * time.Minute

	for {
		select {
		case <-s.done:
			return
		case <-r.Context().Done():
			return
		case <-listener.DisconnectCh:
			return
		default:
		}

		if served {
			current, exists := s.Relay.GetStream(listener.CurrentMountName())
			if !exists || !current.Snapshot().Enabled || s.Config.DisabledMounts[listener.CurrentMountName()] {
				if outageSince.IsZero() {
					outageSince = time.Now()
				}
				if time.Since(outageSince) > maxOutageWait {
					return
				}
			}
		}
		if mount != originalMount {
			if _, ok := s.Relay.GetStream(originalMount); ok {
				if primaryFirstSeen.IsZero() {
					primaryFirstSeen = time.Now()
				}
				if time.Since(primaryFirstSeen) >= fallbackHysteresis {
					logger.L.Infow("Primary stream stable, recovering from fallback",
						"mount", originalMount,
						"stable_for", time.Since(primaryFirstSeen),
					)
					mount = originalMount
					primaryFirstSeen = time.Time{}
				}
			} else {
				primaryFirstSeen = time.Time{}
			}
		}

		stream, ok := s.Relay.GetStream(mount)
		if registered && listener.CurrentMountName() != mount {
			snapshot, _ := s.Relay.Listeners.Get(id)
			if !ok && snapshot.MoveStatus == "pending" && snapshot.MoveDestination == mount {
				s.Relay.Listeners.FailMove(id, "destination unavailable")
				mount = listener.CurrentMountName()
				originalMount = listener.RequestedMountName()
				continue
			}
		}
		if registered && listener.CurrentMountName() != mount && ok {
			if s.Config.DisabledMounts[mount] {
				s.Relay.Listeners.FailMove(id, "destination unavailable")
				mount = listener.CurrentMountName()
				originalMount = listener.RequestedMountName()
				continue
			}
			if _, err := s.Relay.Listeners.ReserveMove(id, mount, s.effectiveMountListenerLimit(mount)); err != nil {
				s.Relay.Listeners.FailMove(id, err.Error())
				mount = listener.CurrentMountName()
				select {
				case <-r.Context().Done():
					return
				case <-time.After(time.Second):
				}
				continue
			}
		}
		if !ok {
			// Follow the fallback only once per pass. FallbackMounts is
			// operator-edited and nothing rejects a cycle (/a -> /b and
			// /b -> /a); with both down the old code hopped between them
			// with no sleep and no response, at 100% of a core, logging
			// "falling back" thousands of times a second.
			fallback, hasFallback := s.Config.FallbackMounts[mount]
			if hasFallback && fallback != mount && mount == originalMount {
				logger.L.Infow("Primary stream down, falling back", "from", mount, "to", fallback)
				mount = fallback
				continue
			}
			if mount != originalMount {
				mount = originalMount
				if served {
					if outageSince.IsZero() {
						outageSince = time.Now()
					}
					if time.Since(outageSince) > maxOutageWait {
						logger.L.Infow("Listener: primary and fallback down too long, dropping", "mount", originalMount)
						return
					}
					select {
					case <-r.Context().Done():
						return
					case <-s.done:
						return
					case <-listener.DisconnectCh:
						return
					case <-time.After(1 * time.Second):
					}
					continue
				}
				// Fresh connection with nothing to fall back to: report it.
			}
			// Only count as a scan attempt when the mount is something
			// we've never known — a configured mount that's currently
			// offline is a legitimate listener, not an attacker probing
			// paths. Similarly, obvious non-stream prefetch paths
			// (favicon.ico, manifest.json, *.png …) don't deserve a ban.
			if s.looksLikeUnknownMount(mount) {
				s.recordScanAttempt(clientIP, mount)
			}
			http.NotFound(w, r)
			return
		}

		metaint := 0
		if r.Header.Get("Icy-MetaData") == "1" && !stream.IsOgg() {
			metaint = 16000
			w.Header().Set("icy-metaint", "16000")
		}
		// ICY metadata headers — populated for all listeners regardless of
		// metaint negotiation so non-shoutcast clients (browsers, players)
		// can still surface the station name in their UI. Field reads are
		// single-string copies — same race profile the rest of this file
		// already accepts.
		icyName := stream.Name
		if icyName == "" {
			icyName = s.Config.PageTitle
		}
		icyGenre := stream.Genre
		icyURL := stream.URL
		icyDesc := stream.Description
		icyBR := stream.Bitrate
		icyPub := stream.Public
		w.Header().Set("icy-name", icyName)
		if icyGenre != "" {
			w.Header().Set("icy-genre", icyGenre)
		}
		if icyURL != "" {
			w.Header().Set("icy-url", icyURL)
		}
		if icyDesc != "" {
			w.Header().Set("icy-description", icyDesc)
		}
		if icyBR != "" {
			w.Header().Set("icy-br", icyBR)
		}
		if icyPub {
			w.Header().Set("icy-pub", "1")
		} else {
			w.Header().Set("icy-pub", "0")
		}

		listenerLimit := s.effectiveMountListenerLimit(mount)

		if !registered {
			listener.CurrentMount = mount
			if err := s.Relay.Listeners.Admit(listener, listenerLimit); err != nil {
				http.Error(w, "Server Full", http.StatusServiceUnavailable)
				return
			}
			registered = true
		}

		w.Header().Set("Content-Type", stream.ContentType)
		if mount != originalMount {
			w.Header().Set("X-Stream-Status", "fallback")
		} else {
			w.Header().Set("X-Stream-Status", "primary")
		}
		if flusher != nil {
			flusher.Flush()
		}

		served = true
		outageSince = time.Time{}
		result := s.serveStreamData(w, r, stream, id, originalMount, mount, recoveryTicker, metaint, icy)
		if !result.KeepGoing {
			return
		}
		if result.NextMount != "" {
			mount = result.NextMount
			originalMount = result.RequestedMount

		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *Server) effectiveMountListenerLimit(mount string) int {
	if s.Config == nil {
		return 0
	}
	s.Config.RLockMaps()
	defer s.Config.RUnlockMaps()
	limit := s.Config.MaxListeners
	if ms := s.Config.AdvancedMounts[mount]; ms != nil && ms.MaxListeners > 0 && (limit <= 0 || ms.MaxListeners < limit) {
		limit = ms.MaxListeners
	}
	return limit
}

const defaultMountBurstSize = 512 * 1024
const maxBurstSize = 8 * 1024 * 1024
const fallbackHysteresis = 30 * time.Second

type icyState struct{ bytesSentSinceMeta int }

func effectiveMountBurstSize(cfg *config.Config, mount string) int {
	if cfg != nil {
		cfg.RLockMaps()
		defer cfg.RUnlockMaps()
	}
	if cfg != nil && cfg.AdvancedMounts != nil {
		if adv := cfg.AdvancedMounts[mount]; adv != nil && adv.BurstSize > 0 {
			if adv.BurstSize > maxBurstSize {
				return maxBurstSize
			}
			return adv.BurstSize
		}
	}
	return defaultMountBurstSize
}

type listenerLoopResult struct {
	NextMount      string
	RequestedMount string
	KeepGoing      bool
}

func (s *Server) serveStreamData(w http.ResponseWriter, r *http.Request, stream *relay.Stream, id, originalMount, currentMount string, recoveryTicker *time.Ticker, metaint int, icy *icyState) listenerLoopResult {
	offset, signal := stream.Subscribe(id, effectiveMountBurstSize(s.Config, currentMount))
	if err := s.Relay.Listeners.CompleteMove(id, currentMount, time.Now()); err != nil {
		stream.Unsubscribe(id)
		s.Relay.Listeners.FailMove(id, err.Error())
		return listenerLoopResult{}
	}
	if listener, ok := s.Relay.Listeners.Listener(id); ok && currentMount == originalMount {
		listener.SetRequestedMount(originalMount)
	}
	defer stream.Unsubscribe(id)
	var disconnectCh <-chan struct{}
	var moveCh <-chan relay.ListenerCommand
	if listener, ok := s.Relay.Listeners.Listener(id); ok {
		disconnectCh = listener.DisconnectCh
		moveCh = listener.MoveCh
	}

	// Geo-track this listener for the dashboard map. The full
	// (city, country, lat, lon) tuple is captured here (synchronous
	// mmdb lookup, sub-millisecond) so the deferred Remove uses the
	// SAME tuple we registered with — avoids races where the lookup
	// might briefly differ across calls (DB swap mid-listen) and
	// double-counts.
	listenerGeo := s.GeoTracker.Add(s.clientIP(r), currentMount)
	defer s.GeoTracker.Remove(listenerGeo, currentMount)

	// For Ogg streams (Opus / Vorbis / FLAC-in-Ogg) route all output through
	// a per-listener Ogg page rewriter. It regenerates the bitstream serial,
	// resets the page sequence, and rebases the granule so the listener sees
	// a clean timeline starting near zero — without this, replaying the
	// cached BOS/Tags pages in front of live audio causes a multi-minute
	// granule jump and strict decoders fill the gap with silence / robotic
	// concealment.
	var out io.Writer = w
	var oggRewriter *relay.OggPageRewriter
	if stream.IsOggStream {
		oggRewriter = relay.NewOggPageRewriter(w)
		out = oggRewriter
	}

	if oggHead := stream.GetOggHead(); oggHead != nil {
		if _, err := out.Write(oggHead); err != nil {
			return listenerLoopResult{}
		}
		logger.L.Debugf("Ogg Listener %s: Sending stored headers (%d bytes), then starting burst at %d", id, len(oggHead), offset)
	}

	// Respect the stream's minimum valid offset — set whenever the
	// source reconfigures its codec mid-stream. Without this a listener
	// that subscribes right after an OBS reconfig replays pre-reconfig
	// bytes (old SPS/PPS) and the decoder dies as soon as the new
	// parameters activate.
	if min := stream.GetMinListenerOffset(); min > offset {
		offset = min
	}

	// For raw H.264 video listeners, seek back to the most recent
	// keyframe (so playback doesn't start on a P-frame whose reference
	// frames aren't in the listener's buffer) and prepend the cached
	// SPS/PPS Annex-B headers. Without this the decoder spends the
	// first GOP spamming "non-existing PPS" / "mmco: unref short
	// failure" errors until the next keyframe cycles through.
	isH264, videoHeaders := stream.VideoInfo()
	if isH264 {
		if kf := stream.Buffer.LatestKeyframe(); kf >= 0 && kf > offset {
			offset = kf
		}
		if len(videoHeaders) > 0 {
			if _, err := out.Write(videoHeaders); err != nil {
				return listenerLoopResult{}
			}
			logger.L.Debugf("H264 Listener %s: Sent SPS/PPS (%d bytes), starting at keyframe %d", id, len(videoHeaders), offset)
		}
	}

	// Per-write deadline. Without it, a Write to a slow / dead /
	// half-disconnected listener blocks forever — the handler
	// goroutine sits in TCP-write-blocked state, the listener slot
	// stays registered on the stream, and the conn ends up in
	// CLOSE_WAIT. With many of those accumulating, the runtime ran
	// out of usable handler slots and NEW HTTPS requests started
	// timing out wholesale (recurring "i/o timeout" symptom).
	//
	// 30 s is conservative — typical TCP send to a healthy listener
	// returns in <1 ms; 30 s only fires when the kernel send buffer
	// is full and not draining (i.e. the receiver is gone). On
	// deadline expiry Write returns an error → we exit the handler →
	// Unsubscribe runs → the conn cleans up.
	rc := http.NewResponseController(w)
	const writeTimeout = 30 * time.Second

	// 64 KiB read chunk: we flush after draining the buffer on each signal,
	// so a larger chunk means fewer syscalls / TCP writes per second without
	// adding latency. 4 KiB was causing tiny-segment writes whenever the
	// source broadcast fast.
	buf := make([]byte, 64*1024)
	flusher, _ := w.(http.Flusher)

	lastSong := ""

	consecutiveSkips := 0
	maxConsecutiveSkips := 5

	// Flush-generation tracker. If the stream owner bumps the flush
	// generation (e.g. transcoder encoder restart after a source
	// flap), we snap our offset forward to MinListenerOffset so the
	// listener doesn't replay the stale buffer contents from before
	// the gap. Read once at subscribe time so we only react to bumps
	// that happen AFTER we started reading.
	lastFlushGen := stream.FlushGen()
	var primaryUpSince time.Time

	for {
		select {
		case <-s.done:
			return listenerLoopResult{}
		case <-r.Context().Done():
			return listenerLoopResult{}
		case <-disconnectCh:
			return listenerLoopResult{}
		case cmd := <-moveCh:
			target := cmd.TargetMount
			if target == "" || target == currentMount {
				continue
			}
			if _, ok := s.Relay.Listeners.Listener(id); ok {
				if _, exists := s.Relay.GetStream(target); !exists || s.Config.DisabledMounts[target] {
					s.Relay.Listeners.FailMove(id, "destination unavailable")
					continue
				}
			}
			return listenerLoopResult{
				NextMount:      target,
				RequestedMount: target,
				KeepGoing:      true,
			}
		case <-recoveryTicker.C:
			// Hand control back only once the primary has been up long
			// enough that handleListener will really switch to it.
			// Returning on the first tick meant a re-subscribe every 10 s
			// for the whole 30 s hysteresis window — each one re-bursts
			// and re-aligns the listener's offset, so the audio hiccupped
			// three times before anything changed.
			if currentMount != originalMount {
				if _, ok := s.Relay.GetStream(originalMount); ok {
					if primaryUpSince.IsZero() {
						primaryUpSince = time.Now()
					}
					if time.Since(primaryUpSince) >= fallbackHysteresis {
						return listenerLoopResult{NextMount: originalMount, RequestedMount: originalMount, KeepGoing: true}
					}
				} else {
					primaryUpSince = time.Time{}
				}
			}
		case _, ok := <-signal:
			if !ok {
				return listenerLoopResult{
					NextMount:      currentMount,
					RequestedMount: originalMount,
					KeepGoing:      true,
				}
			}
			if g := stream.FlushGen(); g != lastFlushGen {
				lastFlushGen = g
				if min := stream.GetMinListenerOffset(); min > offset {
					offset = min
					// Reset the ICY meta-interval counter so the
					// 16000-byte boundary stays aligned to the new
					// post-flush byte stream.
					icy.bytesSentSinceMeta = 0
				}
			}
			for {
				readLimit := len(buf)
				if metaint > 0 {
					remaining := metaint - icy.bytesSentSinceMeta
					if remaining < readLimit {
						readLimit = remaining
					}
				}

				n, next, skipped := stream.Buffer.ReadAt(offset, buf[:readLimit])
				if skipped && stream.IsOggStream {
					consecutiveSkips++
					if consecutiveSkips >= maxConsecutiveSkips {
						logger.L.Warnw("Slow listener disconnected (ogg sync skip)",
							"id", id, "mount", currentMount,
							"consecutive_skips", consecutiveSkips,
						)
						return listenerLoopResult{}
					}
					offset = stream.Buffer.FindNextPageBoundaryLocked(next)
					continue
				}
				if n == 0 {
					break
				}
				if skipped {
					atomic.AddInt64(&stream.BytesDropped, next-offset)
					consecutiveSkips++
					if consecutiveSkips >= maxConsecutiveSkips {
						logger.L.Warnw("Slow listener disconnected",
							"id", id, "mount", currentMount,
							"consecutive_skips", consecutiveSkips,
						)
						return listenerLoopResult{}
					}
				} else {
					consecutiveSkips = 0
				}
				offset = next

				_ = rc.SetWriteDeadline(time.Now().Add(writeTimeout))
				if _, err := out.Write(buf[:n]); err != nil {
					return listenerLoopResult{}
				}

				if metaint > 0 {
					icy.bytesSentSinceMeta += n
					if icy.bytesSentSinceMeta >= metaint {
						currentSong := stream.GetCurrentSong()
						meta := ""
						if currentSong != lastSong {
							meta = fmt.Sprintf("StreamTitle='%s';", currentSong)
							lastSong = currentSong
						}

						// The ICY length byte counts 16-byte blocks, so
						// the block can hold at most 255*16 = 4080 bytes.
						// A longer title overflowed byte(l) and the client
						// then read the tail of the title as audio —
						// a burst of noise and a desynchronised stream.
						const maxICYMeta = 255 * 16
						if len(meta) > maxICYMeta {
							meta = meta[:maxICYMeta-2] + "';"
						}
						l := (len(meta) + 15) / 16
						res := make([]byte, 1+l*16)
						res[0] = byte(l)
						copy(res[1:], meta)

						_ = rc.SetWriteDeadline(time.Now().Add(writeTimeout))
						if _, err := w.Write(res); err != nil {
							return listenerLoopResult{}
						}
						icy.bytesSentSinceMeta = 0
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
