package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/relay"
)

func (s *Server) handleAddRelay(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		return
	}
	user, ok := s.checkAuth(r)
	if ok && user.Role == config.RoleSuperAdmin {
		u, m, pw, bs := r.FormValue("url"), r.FormValue("mount"), r.FormValue("password"), r.FormValue("burst_size")
		if u != "" && m != "" {
			if m[0] != '/' {
				m = "/" + m
			}
			burst := 20
			fmt.Sscanf(bs, "%d", &burst)

			_ = s.mutateConfig(func(cfg *config.Config) error {
				found := false
				for _, rc := range cfg.Relays {
					if rc.Mount == m {
						rc.URL = u
						rc.Password = pw
						rc.BurstSize = burst
						found = true
						break
					}
				}

				if !found {
					rc := &config.RelayConfig{URL: u, Mount: m, Password: pw, BurstSize: burst, Enabled: true}
					cfg.Relays = append(cfg.Relays, rc)
				}
				return nil
			})
			s.RelayM.StartRelay(u, m, pw, burst, s.Config.VisibleMounts[m])
		}
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleToggleRelay(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		return
	}
	user, ok := s.checkAuth(r)
	mount := r.FormValue("mount")
	if ok && user.Role == config.RoleSuperAdmin {
		var selected config.RelayConfig
		found := false
		_ = s.mutateConfig(func(cfg *config.Config) error {
			for _, rc := range cfg.Relays {
				if rc.Mount == mount {
					rc.Enabled = !rc.Enabled
					selected = *rc
					found = true
					break
				}
			}
			return nil
		})
		if found {
			if selected.Enabled {
				s.RelayM.StartRelay(selected.URL, selected.Mount, selected.Password, selected.BurstSize, s.Config.VisibleMounts[mount])
			} else {
				s.RelayM.StopRelay(mount)
			}
		}
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleDeleteRelay(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		return
	}
	user, ok := s.checkAuth(r)
	mount := r.FormValue("mount")
	if ok && user.Role == config.RoleSuperAdmin {
		removed := false
		_ = s.mutateConfig(func(cfg *config.Config) error {
			for i, rc := range cfg.Relays {
				if rc.Mount == mount {
					cfg.Relays = append(cfg.Relays[:i], cfg.Relays[i+1:]...)
					removed = true
					break
				}
			}
			return nil
		})
		if removed {
			s.RelayM.StopRelay(mount)
		}
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleRestartRelay(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		return
	}
	user, ok := s.checkAuth(r)
	mount := r.FormValue("mount")
	if ok && user.Role == config.RoleSuperAdmin {
		for _, rc := range s.Config.Relays {
			if rc.Mount == mount {
				s.RelayM.StartRelay(rc.URL, rc.Mount, rc.Password, rc.BurstSize, s.Config.VisibleMounts[mount])
				break
			}
		}
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleAddTranscoder(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	name := r.FormValue("name")
	input := r.FormValue("input")
	output := r.FormValue("output")
	format := r.FormValue("format")
	var bitrate int
	fmt.Sscanf(r.FormValue("bitrate"), "%d", &bitrate)

	tc := &config.TranscoderConfig{
		Name:        name,
		InputMount:  input,
		OutputMount: output,
		Format:      format,
		Bitrate:     bitrate,
		Enabled:     true,
	}

	_ = s.mutateConfig(func(cfg *config.Config) error {
		cfg.Transcoders = append(cfg.Transcoders, tc)
		return nil
	})
	s.TranscoderM.StartTranscoder(tc)

	http.Redirect(w, r, "/admin#tab-transcoding", http.StatusSeeOther)
}

func (s *Server) handleToggleTranscoder(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.checkAuth(r); !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	name := r.FormValue("name")
	for _, tc := range s.Config.Transcoders {
		if tc.Name == name {
			tc.Enabled = !tc.Enabled
			if tc.Enabled {
				s.TranscoderM.StartTranscoder(tc)
			} else {
				s.TranscoderM.StopTranscoder(tc.OutputMount)
			}
			break
		}
	}
	_ = s.saveConfig()
	http.Redirect(w, r, "/admin#tab-transcoding", http.StatusSeeOther)
}

func (s *Server) handleDeleteTranscoder(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.checkAuth(r); !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	name := r.FormValue("name")
	newTCs := []*config.TranscoderConfig{}
	for _, tc := range s.Config.Transcoders {
		if tc.Name != name {
			newTCs = append(newTCs, tc)
		} else {
			s.TranscoderM.StopTranscoder(tc.OutputMount)
		}
	}
	_ = s.mutateConfig(func(cfg *config.Config) error {
		cfg.Transcoders = newTCs
		return nil
	})
	http.Redirect(w, r, "/admin#tab-transcoding", http.StatusSeeOther)
}

func (s *Server) handleTranscoderStats(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.checkAuth(r); !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}
