package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/relay"
)

func (s *Server) apiGetStreams(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	allStreams := s.Relay.Snapshot()
	type streamInfo struct {
		Mount              string                  `json:"mount"`
		ContentType        string                  `json:"content_type"`
		Bitrate            string                  `json:"bitrate"`
		BurstSize          int                     `json:"burst_size"`
		Listeners          int                     `json:"listeners"`
		MaxListeners       int                     `json:"max_listeners"`
		SourceIP           string                  `json:"source_ip"`
		Visible            bool                    `json:"visible"`
		Enabled            bool                    `json:"enabled"`
		Health             float64                 `json:"health"`
		Uptime             string                  `json:"uptime"`
		CurrentSong        string                  `json:"current_song"`
		Name               string                  `json:"name"`
		Status             string                  `json:"status"`
		StatusClass        string                  `json:"status_class"`
		StatusReason       string                  `json:"status_reason"`
		LastError          string                  `json:"last_error,omitempty"`
		StatusUpdatedAt    int64                   `json:"status_updated_at"`
		LastRecoveryAt     int64                   `json:"last_recovery_at,omitempty"`
		LastRecoveryResult string                  `json:"last_recovery_result,omitempty"`
		History            []relay.DiagnosticEntry `json:"history"`
	}

	var result []streamInfo
	seen := make(map[string]bool)
	for _, st := range allStreams {
		if s.hasAccess(user, st.MountName) {
			seen[st.MountName] = true
			diag := diagnosticInfoFor(s.Relay, st.MountName)
			ms := s.Config.AdvancedMounts[st.MountName]
			burstSize := 0
			maxListeners := 0
			if ms != nil {
				burstSize = ms.BurstSize
				maxListeners = ms.MaxListeners
			}
			result = append(result, streamInfo{
				Mount:              st.MountName,
				ContentType:        st.ContentType,
				Bitrate:            st.Bitrate,
				BurstSize:          burstSize,
				Listeners:          st.ListenersCount,
				MaxListeners:       maxListeners,
				SourceIP:           st.SourceIP,
				Visible:            st.Visible,
				Enabled:            st.Enabled,
				Health:             st.Health,
				Uptime:             st.Uptime,
				CurrentSong:        st.CurrentSong,
				Name:               st.Name,
				Status:             diag.Status,
				StatusClass:        diag.StatusClass,
				StatusReason:       diag.StatusReason,
				LastError:          diag.LastError,
				StatusUpdatedAt:    diag.StatusUpdatedAt,
				LastRecoveryAt:     diag.LastRecoveryAt,
				LastRecoveryResult: diag.LastRecoveryResult,
				History:            diag.History,
			})
		}
	}

	for mount := range s.Config.Mounts {
		if !seen[mount] && s.hasAccess(user, mount) {
			seen[mount] = true
			disabled := s.Config.DisabledMounts[mount]
			visible := s.Config.VisibleMounts[mount]
			diag := diagnosticInfoFor(s.Relay, mount)
			ms := s.Config.AdvancedMounts[mount]
			burstSize := 0
			maxListeners := 0
			if ms != nil {
				burstSize = ms.BurstSize
				maxListeners = ms.MaxListeners
			}
			result = append(result, streamInfo{
				Mount:              mount,
				BurstSize:          burstSize,
				MaxListeners:       maxListeners,
				Visible:            visible,
				Enabled:            !disabled,
				Status:             diag.Status,
				StatusClass:        diag.StatusClass,
				StatusReason:       diag.StatusReason,
				LastError:          diag.LastError,
				StatusUpdatedAt:    diag.StatusUpdatedAt,
				LastRecoveryAt:     diag.LastRecoveryAt,
				LastRecoveryResult: diag.LastRecoveryResult,
				History:            diag.History,
			})
		}
	}
	for _, u := range s.Config.Users {
		for mount := range u.Mounts {
			if !seen[mount] && s.hasAccess(user, mount) {
				seen[mount] = true
				disabled := s.Config.DisabledMounts[mount]
				visible := s.Config.VisibleMounts[mount]
				diag := diagnosticInfoFor(s.Relay, mount)
				ms := s.Config.AdvancedMounts[mount]
				burstSize := 0
				maxListeners := 0
				if ms != nil {
					burstSize = ms.BurstSize
					maxListeners = ms.MaxListeners
				}
				result = append(result, streamInfo{
					Mount:              mount,
					BurstSize:          burstSize,
					MaxListeners:       maxListeners,
					Visible:            visible,
					Enabled:            !disabled,
					Status:             diag.Status,
					StatusClass:        diag.StatusClass,
					StatusReason:       diag.StatusReason,
					LastError:          diag.LastError,
					StatusUpdatedAt:    diag.StatusUpdatedAt,
					LastRecoveryAt:     diag.LastRecoveryAt,
					LastRecoveryResult: diag.LastRecoveryResult,
					History:            diag.History,
				})
			}
		}
	}

	if result == nil {
		result = []streamInfo{}
	}
	jsonResponse(w, result)
}

func (s *Server) apiGetStreamDiagnostics(w http.ResponseWriter, r *http.Request) {
	mount := r.URL.Query().Get("mount")
	if mount == "" {
		jsonError(w, "Mount is required", http.StatusBadRequest)
		return
	}
	if _, ok := s.requireMountAccess(w, r, mount); !ok {
		return
	}
	if s.Relay.History == nil {
		jsonError(w, "History disabled", http.StatusServiceUnavailable)
		return
	}

	limit := 10
	if raw := r.URL.Query().Get("limit"); raw != "" {
		fmt.Sscanf(raw, "%d", &limit)
	}
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	jsonResponse(w, s.Relay.History.GetDiagnostics(mount, limit))
}

func (s *Server) apiCreateStream(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var body struct {
		Mount        string `json:"mount"`
		Password     string `json:"password"`
		BurstSize    int    `json:"burst_size"`
		MaxListeners int    `json:"max_listeners"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if body.Mount == "" || body.Password == "" {
		jsonError(w, "Mount and password are required", http.StatusBadRequest)
		return
	}
	if body.Mount[0] != '/' {
		body.Mount = "/" + body.Mount
	}

	if !s.hasAccess(user, body.Mount) {
		exists := false
		if _, ok := s.Config.Mounts[body.Mount]; ok {
			exists = true
		}
		if !exists {
			for _, u := range s.Config.Users {
				if _, ok := u.Mounts[body.Mount]; ok {
					exists = true
					break
				}
			}
		}
		if exists {
			jsonError(w, "Mount taken", http.StatusConflict)
			return
		}
	}

	hashed, _ := config.HashPassword(body.Password)
	if err := s.mutateConfig(func(cfg *config.Config) error {
		if user.Role == config.RoleSuperAdmin {
			cfg.Mounts[body.Mount] = hashed
		} else {
			user.Mounts[body.Mount] = hashed
		}
		if body.BurstSize > 0 || body.MaxListeners > 0 {
			ms := cfg.AdvancedMounts[body.Mount]
			if ms == nil {
				ms = &config.MountSettings{}
				cfg.AdvancedMounts[body.Mount] = ms
			}
			if body.BurstSize > 0 {
				ms.BurstSize = body.BurstSize
			}
			if body.MaxListeners > 0 {
				ms.MaxListeners = body.MaxListeners
			}
		}
		return nil
	}); err != nil {
		jsonError(w, "Failed to save config", http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "created", "mount": body.Mount})
	s.Audit(r, "mount_created", "stream", body.Mount, "")
}

func (s *Server) apiDeleteStream(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	mount := r.URL.Query().Get("mount")
	if mount == "" {
		jsonError(w, "Mount is required", http.StatusBadRequest)
		return
	}
	if !s.hasAccess(user, mount) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	if err := s.mutateConfig(func(cfg *config.Config) error {
		delete(cfg.Mounts, mount)
		delete(cfg.DisabledMounts, mount)
		delete(cfg.VisibleMounts, mount)
		delete(cfg.AdvancedMounts, mount)
		delete(user.Mounts, mount)
		return nil
	}); err != nil {
		jsonError(w, "Failed to save config", http.StatusInternalServerError)
		return
	}
	s.Relay.RemoveStream(mount)
	jsonResponse(w, map[string]string{"status": "deleted"})
	s.Audit(r, "mount_deleted", "stream", mount, "")
}

func (s *Server) apiUpdateStream(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var body struct {
		Mount        string `json:"mount"`
		BurstSize    int    `json:"burst_size"`
		MaxListeners int    `json:"max_listeners"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if body.Mount == "" {
		jsonError(w, "Mount is required", http.StatusBadRequest)
		return
	}
	if !s.hasAccess(user, body.Mount) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	if err := s.mutateConfig(func(cfg *config.Config) error {
		ms := cfg.AdvancedMounts[body.Mount]
		if ms == nil {
			ms = &config.MountSettings{}
			cfg.AdvancedMounts[body.Mount] = ms
		}
		ms.BurstSize = body.BurstSize
		ms.MaxListeners = body.MaxListeners
		return nil
	}); err != nil {
		jsonError(w, "Failed to save config", http.StatusInternalServerError)
		return
	}

	jsonResponse(w, map[string]string{"status": "updated"})
}

func (s *Server) apiKickStream(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var body struct {
		Mount string `json:"mount"`
		Type  string `json:"type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if body.Mount == "" {
		jsonError(w, "Mount is required", http.StatusBadRequest)
		return
	}
	if !s.hasAccess(user, body.Mount) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	switch body.Type {
	case "listeners":
		if st, ok := s.Relay.GetStream(body.Mount); ok {
			st.DisconnectListeners()
		}
	default:
		s.Relay.RemoveStream(body.Mount)
	}
	jsonResponse(w, map[string]string{"status": "ok"})
}
