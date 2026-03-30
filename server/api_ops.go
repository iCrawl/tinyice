package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/relay"
)

func (s *Server) apiGetAuditLog(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	page := 1
	limit := 25
	if v := r.URL.Query().Get("page"); v != "" {
		fmt.Sscanf(v, "%d", &page)
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		fmt.Sscanf(v, "%d", &limit)
	}
	if limit > 100 {
		limit = 100
	}
	if page < 1 {
		page = 1
	}
	category := r.URL.Query().Get("category")

	entries, total := s.Relay.History.GetAuditLog(page, limit, category)
	jsonResponse(w, map[string]interface{}{
		"entries": entries,
		"total":   total,
		"page":    page,
		"limit":   limit,
	})
}

func (s *Server) apiGetRelays(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	type relayInfo struct {
		URL       string `json:"url"`
		Mount     string `json:"mount"`
		BurstSize int    `json:"burst_size"`
		Enabled   bool   `json:"enabled"`
		Active    bool   `json:"active"`
	}

	var result []relayInfo
	for _, rc := range s.Config.Relays {
		active := false
		if st, ok := s.Relay.GetStream(rc.Mount); ok && st.SourceIP == "relay-pull" {
			active = true
		}
		result = append(result, relayInfo{
			URL:       rc.URL,
			Mount:     rc.Mount,
			BurstSize: rc.BurstSize,
			Enabled:   rc.Enabled,
			Active:    active,
		})
	}
	if result == nil {
		result = []relayInfo{}
	}
	jsonResponse(w, result)
}

func (s *Server) apiCreateRelay(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	var body struct {
		URL       string `json:"url"`
		Mount     string `json:"mount"`
		Password  string `json:"password"`
		BurstSize int    `json:"burst_size"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if body.URL == "" || body.Mount == "" {
		jsonError(w, "URL and mount are required", http.StatusBadRequest)
		return
	}
	if body.Mount[0] != '/' {
		body.Mount = "/" + body.Mount
	}
	if body.BurstSize == 0 {
		body.BurstSize = 20
	}

	if err := s.mutateConfig(func(cfg *config.Config) error {
		found := false
		for _, rc := range cfg.Relays {
			if rc.Mount == body.Mount {
				rc.URL = body.URL
				rc.Password = body.Password
				rc.BurstSize = body.BurstSize
				found = true
				break
			}
		}
		if !found {
			cfg.Relays = append(cfg.Relays, &config.RelayConfig{
				URL: body.URL, Mount: body.Mount, Password: body.Password, BurstSize: body.BurstSize, Enabled: true,
			})
		}
		return nil
	}); err != nil {
		jsonError(w, "Failed to save config", http.StatusInternalServerError)
		return
	}
	s.RelayM.StartRelay(body.URL, body.Mount, body.Password, body.BurstSize, s.Config.VisibleMounts[body.Mount])
	jsonResponse(w, map[string]string{"status": "created"})
	s.Audit(r, "relay_created", "relay", body.Mount, body.URL)
}

func (s *Server) apiDeleteRelay(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	mount := r.URL.Query().Get("mount")
	if mount == "" {
		jsonError(w, "Mount is required", http.StatusBadRequest)
		return
	}

	newRelays := []*config.RelayConfig{}
	found := false
	for _, rc := range s.Config.Relays {
		if rc.Mount != mount {
			newRelays = append(newRelays, rc)
		} else {
			found = true
		}
	}
	if !found {
		jsonError(w, "Relay not found", http.StatusNotFound)
		return
	}
	if err := s.mutateConfig(func(cfg *config.Config) error {
		cfg.Relays = newRelays
		return nil
	}); err != nil {
		jsonError(w, "Failed to save config", http.StatusInternalServerError)
		return
	}
	s.RelayM.StopRelay(mount)
	jsonResponse(w, map[string]string{"status": "deleted"})
	s.Audit(r, "relay_deleted", "relay", mount, "")
}

func (s *Server) apiToggleRelay(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	var body struct {
		Mount string `json:"mount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	var selected config.RelayConfig
	found := false
	if err := s.mutateConfig(func(cfg *config.Config) error {
		for _, rc := range cfg.Relays {
			if rc.Mount == body.Mount {
				rc.Enabled = !rc.Enabled
				selected = *rc
				found = true
				break
			}
		}
		return nil
	}); err != nil {
		jsonError(w, "Failed to save config", http.StatusInternalServerError)
		return
	}
	if found {
		if selected.Enabled {
			s.RelayM.StartRelay(selected.URL, selected.Mount, selected.Password, selected.BurstSize, s.Config.VisibleMounts[body.Mount])
		} else {
			s.RelayM.StopRelay(body.Mount)
		}
		jsonResponse(w, map[string]interface{}{"status": "ok", "enabled": selected.Enabled})
		return
	}
	jsonError(w, "Relay not found", http.StatusNotFound)
}

func (s *Server) apiGetTranscoders(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.checkAuth(r); !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var stats []relay.TranscoderStats
	for _, tc := range s.Config.Transcoders {
		inst := s.TranscoderM.GetInstance(tc.OutputMount)
		uptime := "OFF"
		var frames, bytes int64
		active := false
		if inst != nil {
			active = true
			uptime = time.Since(inst.StartTime).Round(time.Second).String()
			frames = atomic.LoadInt64(&inst.FramesProcessed)
			bytes = atomic.LoadInt64(&inst.BytesEncoded)
		}
		stats = append(stats, relay.TranscoderStats{
			Name:            tc.Name,
			Input:           tc.InputMount,
			Output:          tc.OutputMount,
			Format:          tc.Format,
			Bitrate:         tc.Bitrate,
			Active:          active,
			FramesProcessed: frames,
			BytesEncoded:    bytes,
			Uptime:          uptime,
		})
	}
	if stats == nil {
		stats = []relay.TranscoderStats{}
	}
	jsonResponse(w, stats)
}

func (s *Server) apiCreateTranscoder(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	var body struct {
		Name        string `json:"name"`
		InputMount  string `json:"input_mount"`
		OutputMount string `json:"output_mount"`
		Format      string `json:"format"`
		Bitrate     int    `json:"bitrate"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if body.Name == "" || body.InputMount == "" || body.OutputMount == "" {
		jsonError(w, "Name, input_mount, and output_mount are required", http.StatusBadRequest)
		return
	}

	tc := &config.TranscoderConfig{
		Name:        body.Name,
		InputMount:  body.InputMount,
		OutputMount: body.OutputMount,
		Format:      body.Format,
		Bitrate:     body.Bitrate,
		Enabled:     true,
	}
	if err := s.mutateConfig(func(cfg *config.Config) error {
		cfg.Transcoders = append(cfg.Transcoders, tc)
		return nil
	}); err != nil {
		jsonError(w, "Failed to save config", http.StatusInternalServerError)
		return
	}
	s.TranscoderM.StartTranscoder(tc)
	jsonResponse(w, map[string]string{"status": "created"})
	s.Audit(r, "transcoder_created", "transcoder", body.OutputMount, body.Name)
}

func (s *Server) apiDeleteTranscoder(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	name := r.URL.Query().Get("name")
	if name == "" {
		jsonError(w, "Name is required", http.StatusBadRequest)
		return
	}

	newTCs := []*config.TranscoderConfig{}
	found := false
	for _, tc := range s.Config.Transcoders {
		if tc.Name != name {
			newTCs = append(newTCs, tc)
		} else {
			s.TranscoderM.StopTranscoder(tc.OutputMount)
			found = true
		}
	}
	if !found {
		jsonError(w, "Transcoder not found", http.StatusNotFound)
		return
	}
	if err := s.mutateConfig(func(cfg *config.Config) error {
		cfg.Transcoders = newTCs
		return nil
	}); err != nil {
		jsonError(w, "Failed to save config", http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "deleted"})
	s.Audit(r, "transcoder_deleted", "transcoder", name, "")
}

func (s *Server) apiGetStats(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	bi, bo := s.Relay.GetMetrics()
	allStreams := s.Relay.Snapshot()
	totalListeners := 0
	totalDropped := int64(0)

	type streamStat struct {
		Mount       string  `json:"mount"`
		Name        string  `json:"name"`
		Listeners   int     `json:"listeners"`
		Bitrate     string  `json:"bitrate"`
		Uptime      string  `json:"uptime"`
		ContentType string  `json:"content_type"`
		SourceIP    string  `json:"source_ip"`
		BytesIn     int64   `json:"bytes_in"`
		BytesOut    int64   `json:"bytes_out"`
		CurrentSong string  `json:"current_song"`
		Health      float64 `json:"health"`
	}

	var streams []streamStat
	for _, st := range allStreams {
		if !s.hasAccess(user, st.MountName) {
			continue
		}
		totalListeners += st.ListenersCount
		totalDropped += st.BytesDropped
		streams = append(streams, streamStat{
			Mount:       st.MountName,
			Name:        st.Name,
			Listeners:   st.ListenersCount,
			Bitrate:     st.Bitrate,
			Uptime:      st.Uptime,
			ContentType: st.ContentType,
			SourceIP:    st.SourceIP,
			BytesIn:     st.BytesIn,
			BytesOut:    st.BytesOut,
			CurrentSong: st.CurrentSong,
			Health:      st.Health,
		})
	}
	if streams == nil {
		streams = []streamStat{}
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	jsonResponse(w, map[string]interface{}{
		"bytes_in":        bi,
		"bytes_out":       bo,
		"total_listeners": totalListeners,
		"total_streams":   len(streams),
		"total_dropped":   totalDropped,
		"streams":         streams,
		"server_uptime":   time.Since(s.startTime).Round(time.Second).String(),
		"goroutines":      runtime.NumGoroutine(),
		"sys_ram":         m.Sys,
		"heap_alloc":      m.HeapAlloc,
		"num_gc":          m.NumGC,
		"version":         s.Version,
		"commit":          s.Commit,
	})
}
