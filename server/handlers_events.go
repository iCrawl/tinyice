package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/DatanoiseTV/tinyice/relay"
)

func (s *Server) sendMetadataEvent(w http.ResponseWriter, mc relay.MetadataChange, sentRevision map[string]uint64) error {
	if current, exists := s.Relay.GetStream(mc.Mount); exists {
		if !current.IsVisible() {
			return nil
		}
	} else if !mc.Visible || mc.Title != "" || mc.PlaybackID != "" {
		return nil
	}
	if previous, ok := sentRevision[mc.Mount]; ok && mc.Revision <= previous {
		return nil
	}
	sentRevision[mc.Mount] = mc.Revision
	metadataJSON, _ := json.Marshal(map[string]interface{}{
		"mount":       mc.Mount,
		"title":       mc.Title,
		"artist":      mc.Artist,
		"started_at":  mc.StartedAt.UTC().Format(time.RFC3339),
		"playback_id": mc.PlaybackID,
		"revision":    mc.Revision,
	})
	if _, err := fmt.Fprintf(w, "event: metadata\ndata: %s\n\n", metadataJSON); err != nil {
		return err
	}
	w.(http.Flusher).Flush()
	return nil
}

// namedStreamEvent converts one entry of the stats payload into the
// `stream` SSE frame the Preact frontend consumes. It used to be a
// hand-listed subset, which had drifted: artist was hardcoded empty and
// the whole transcode group (is_transcoded, source_mount, source_type,
// source_bitrate) never made it out, so the dashboard's
// "<src-format> -> <out-format>" display had nothing to render. Copying
// the entry and renaming only the three fields whose names differ keeps
// a new field on streamEventInfo from going missing again.
func namedStreamEvent(stMap map[string]interface{}) map[string]interface{} {
	ev := make(map[string]interface{}, len(stMap)+3)
	for k, v := range stMap {
		ev[k] = v
	}
	ev["format"] = stMap["type"]
	ev["title"] = stMap["song"]
	ev["artist"] = stMap["name"]
	return ev
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	// Track previous byte counters to compute bandwidth as a rate. The UI
	// formats bandwidth_in/out as "bytes per second"; sending cumulative
	// totals made the dashboard display the session's entire ingest as MB/s.
	var lastBytesIn, lastBytesOut int64
	lastTime := time.Now()

	send := func() error {
		payload, err := s.collectStatsPayload(user)
		if err != nil {
			return err
		}
		// Send as unnamed event for legacy clients
		if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
			return err
		}

		// Also send named events for the new Preact frontend
		var full map[string]interface{}
		json.Unmarshal(payload, &full)

		curIn, _ := full["bytes_in"].(float64)
		curOut, _ := full["bytes_out"].(float64)
		now := time.Now()
		elapsed := now.Sub(lastTime).Seconds()
		var rateIn, rateOut int64
		if elapsed > 0 {
			if lastTime.IsZero() || lastBytesIn == 0 && lastBytesOut == 0 {
				// First tick: no prior sample, report 0 instead of a huge burst.
				rateIn, rateOut = 0, 0
			} else {
				rateIn = int64(float64(int64(curIn)-lastBytesIn) / elapsed)
				rateOut = int64(float64(int64(curOut)-lastBytesOut) / elapsed)
				if rateIn < 0 {
					rateIn = 0
				}
				if rateOut < 0 {
					rateOut = 0
				}
			}
		}
		lastBytesIn = int64(curIn)
		lastBytesOut = int64(curOut)
		lastTime = now

		// stats event
		statsJSON, _ := json.Marshal(map[string]interface{}{
			"listeners":     full["total_listeners"],
			"streams":       full["total_sources"],
			"bandwidth_in":  rateIn,
			"bandwidth_out": rateOut,
			"bandwidth":     rateOut, // backwards compat
			"bytes_in":      int64(curIn),
			"bytes_out":     int64(curOut),
			"uptime":        int(time.Since(s.startTime).Seconds()),
			"goroutines":    full["goroutines"],
			"memory":        full["heap_alloc"],
			"gc":            full["num_gc"],
		})
		fmt.Fprintf(w, "event: stats\ndata: %s\n\n", statsJSON)

		// stream events
		if streams, ok := full["streams"].([]interface{}); ok {
			for _, st := range streams {
				stMap := st.(map[string]interface{})
				streamJSON, _ := json.Marshal(namedStreamEvent(stMap))
				fmt.Fprintf(w, "event: stream\ndata: %s\n\n", streamJSON)
			}
		}

		// autodj events
		if streamers, ok := full["streamers"].([]interface{}); ok {
			for _, st := range streamers {
				stJSON, _ := json.Marshal(st)
				fmt.Fprintf(w, "event: autodj\ndata: %s\n\n", stJSON)
			}
		}

		// geo event — full city list per tick. Cheap (the snapshot
		// is in-memory) and lets the dashboard map auto-refresh
		// without a separate /admin/geo poll. We send it on every
		// SSE tick so the operator sees connect/disconnect events
		// immediately.
		if geo, ok := full["geo"]; ok {
			geoJSON, _ := json.Marshal(geo)
			fmt.Fprintf(w, "event: geo\ndata: %s\n\n", geoJSON)
		}

		flusher.Flush()
		return nil
	}

	if err := send(); err != nil {
		return
	}
	for {
		select {
		case <-s.done:
			return
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if err := send(); err != nil {
				return
			}
		}
	}
}

func (s *Server) handlePublicEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	flusher, _ := w.(http.Flusher)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	metaCh := s.Relay.SubscribeMetadata()
	defer s.Relay.UnsubscribeMetadata(metaCh)

	type PublicStreamInfo struct {
		PlaybackID  string    `json:"playback_id"`
		Revision    uint64    `json:"revision"`
		StartedAt   time.Time `json:"started_at,omitzero"`
		Artist      string    `json:"artist"`
		Mount       string    `json:"mount"`
		Name        string    `json:"name"`
		Listeners   int       `json:"listeners"`
		Viewers     int       `json:"viewers,omitempty"`
		Bitrate     string    `json:"bitrate"`
		Uptime      string    `json:"uptime"`
		Genre       string    `json:"genre"`
		Description string    `json:"description"`
		CurrentSong string    `json:"song"`
		// Live mirrors the landing page's bootstrap field: a mount with
		// no connected source still appears here, and without this the
		// page had no way to tell the two apart.
		Live        bool    `json:"live"`
		HasVideo    bool    `json:"has_video,omitempty"`
		VideoWidth  int     `json:"video_width,omitempty"`
		VideoHeight int     `json:"video_height,omitempty"`
		VideoFPS    float64 `json:"video_fps,omitempty"`
		VideoGOP    float64 `json:"video_gop,omitempty"`
		VideoKbps   int     `json:"video_kbps,omitempty"`
	}
	send := func() error {
		allStreams := s.Relay.Snapshot()
		videoMounts := make(map[string]bool)
		for _, st := range allStreams {
			if strings.HasSuffix(st.MountName, "/video") {
				videoMounts[strings.TrimSuffix(st.MountName, "/video")] = true
			}
		}
		var info []PublicStreamInfo
		// We split visible-only from emitted-to-events: the unnamed
		// `data:` payload (legacy landing card consumers) only sees
		// visible mounts, while the per-stream named "stream" events
		// (player consumers) see everything so a player tab can show
		// stats for an unlisted mount.
		var visible []PublicStreamInfo
		for _, st := range allStreams {
			if strings.HasSuffix(st.MountName, "/video") {
				continue // sub-mount, surfaced via the parent
			}
			playback := st.Playback
			artist := playback.Artist
			if playback.Revision == 0 {
				artist = st.Name
			}
			entry := PublicStreamInfo{PlaybackID: playback.PlaybackID, Revision: playback.Revision, StartedAt: playback.StartedAt, Artist: artist,
				Mount: st.MountName, Name: st.Name, Listeners: st.ListenersCount,
				Bitrate: st.Bitrate, Uptime: st.Uptime, Genre: st.Genre,
				Description: st.Description, CurrentSong: st.CurrentSong,
				Live: st.SourceIP != "", HasVideo: videoMounts[st.MountName],
			}
			if liveStream, ok := s.Relay.GetStream(st.MountName); ok {
				entry.Viewers = liveStream.ViewerCount()
				if entry.HasVideo {
					if vs, ok := s.Relay.GetStream(st.MountName + "/video"); ok {
						vm := vs.VideoMetricsSnapshot()
						if vm.Width > 0 {
							entry.VideoWidth = vm.Width
							entry.VideoHeight = vm.Height
							entry.VideoFPS = vm.FPS
							entry.VideoGOP = vm.GOPSeconds
							entry.VideoKbps = vm.BitrateKbps
						}
						if c := vs.ViewerCount(); c > entry.Viewers {
							entry.Viewers = c
						}
					}
				}
			}
			info = append(info, entry)
			if st.Visible {
				visible = append(visible, entry)
			}
		}
		payload, _ := json.Marshal(visible)
		if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
			return err
		}
		// Per-stream named events so the player's `sse.on('stream', …)`
		// handler actually fires; the unnamed `data:` frame above is
		// kept for legacy listeners.
		for _, entry := range info {
			single, _ := json.Marshal(map[string]interface{}{
				"mount":       entry.Mount,
				"title":       entry.CurrentSong,
				"artist":      entry.Artist,
				"playback_id": entry.PlaybackID, "revision": entry.Revision, "started_at": entry.StartedAt.UTC().Format(time.RFC3339),
				"format":       "",
				"bitrate":      entry.Bitrate,
				"listeners":    entry.Listeners,
				"viewers":      entry.Viewers,
				"live":         entry.Live,
				"has_video":    entry.HasVideo,
				"video_width":  entry.VideoWidth,
				"video_height": entry.VideoHeight,
				"video_fps":    entry.VideoFPS,
				"video_gop":    entry.VideoGOP,
				"video_kbps":   entry.VideoKbps,
			})
			if _, err := fmt.Fprintf(w, "event: stream\ndata: %s\n\n", single); err != nil {
				return err
			}
		}
		present := make(map[string]bool, len(info))
		for _, entry := range info {
			present[entry.Mount] = true
		}
		for _, mc := range s.Relay.LastMetadata() {
			if !present[mc.Mount] && mc.Title == "" {
				single, _ := json.Marshal(map[string]interface{}{"mount": mc.Mount, "title": "", "artist": mc.Artist, "started_at": mc.StartedAt.UTC().Format(time.RFC3339), "playback_id": "", "revision": mc.Revision, "live": false, "listeners": 0, "viewers": 0, "has_video": false})
				if _, err := fmt.Fprintf(w, "event: stream\ndata: %s\n\n", single); err != nil {
					return err
				}
			}
		}
		flusher.Flush()
		return nil
	}
	sentRevision := make(map[string]uint64)

	if err := send(); err != nil {
		return
	}
	for _, mc := range s.Relay.LastMetadata() {
		if err := s.sendMetadataEvent(w, mc, sentRevision); err != nil {
			return
		}
	}
	for {
		select {
		case <-s.done:
			return
		case <-r.Context().Done():
			return
		case mc := <-metaCh:
			if err := s.sendMetadataEvent(w, mc, sentRevision); err != nil {
				return
			}
		case <-ticker.C:
			if err := send(); err != nil {
				return
			}
		}
	}
}

func (s *Server) handleMetadataEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	metaCh := s.Relay.SubscribeMetadata()
	defer s.Relay.UnsubscribeMetadata(metaCh)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	sentRevision := make(map[string]uint64)

	for _, mc := range s.Relay.LastMetadata() {
		if err := s.sendMetadataEvent(w, mc, sentRevision); err != nil {
			return
		}
	}

	for {
		select {
		case <-s.done:
			return
		case <-r.Context().Done():
			return
		case <-ticker.C:
			for _, mc := range s.Relay.LastMetadata() {
				if err := s.sendMetadataEvent(w, mc, sentRevision); err != nil {
					return
				}
			}
		case mc := <-metaCh:
			if err := s.sendMetadataEvent(w, mc, sentRevision); err != nil {
				return
			}
		}
	}
}
