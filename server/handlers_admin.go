package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/logger"
	"github.com/DatanoiseTV/tinyice/relay"
)

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	allStreams := s.Relay.Snapshot()
	var streams []relay.StreamStats
	mountMap := make(map[string]bool)
	for _, st := range allStreams {
		if s.hasAccess(user, st.MountName) {
			streams = append(streams, st)
			mountMap[st.MountName] = true
		}
	}

	for m := range user.Mounts {
		mountMap[m] = true
	}
	if user.Role == config.RoleSuperAdmin {
		for m := range s.Config.Mounts {
			mountMap[m] = true
		}
		for _, rc := range s.Config.Relays {
			mountMap[rc.Mount] = true
		}
	}
	var allMounts []string
	for m := range mountMap {
		allMounts = append(allMounts, m)
	}
	sort.Strings(allMounts)

	csrf := ""
	if cookie, err := r.Cookie("sid"); err == nil {
		s.sessionsMu.RLock()
		if sess, ok := s.sessions[cookie.Value]; ok {
			csrf = sess.CSRFToken
		}
		s.sessionsMu.RUnlock()
	}

	pageData := s.BasePageData(csrf)
	pageData["user"] = map[string]interface{}{
		"username": user.Username,
		"role":     user.Role,
	}
	pageData["mounts"] = allMounts
	s.shell.Render(w, "admin", "Admin — "+s.Config.PageTitle, pageData)
}

func (s *Server) handleUpdateFallback(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		return
	}
	mount := r.FormValue("mount")
	fallback := r.FormValue("fallback")
	if !s.hasAccess(user, mount) {
		return
	}

	if fallback == "" {
		_ = s.mutateConfig(func(cfg *config.Config) error {
			delete(cfg.FallbackMounts, mount)
			return nil
		})
	} else {
		if fallback[0] != '/' {
			fallback = "/" + fallback
		}
		_ = s.mutateConfig(func(cfg *config.Config) error {
			cfg.FallbackMounts[mount] = fallback
			return nil
		})
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleAddMount(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	mount, password := r.FormValue("mount"), r.FormValue("password")
	if mount == "" || password == "" {
		http.Error(w, "Missing fields", http.StatusBadRequest)
		return
	}
	if mount[0] != '/' {
		mount = "/" + mount
	}
	if !s.hasAccess(user, mount) {
		exists := false
		if _, ok := s.Config.Mounts[mount]; ok {
			exists = true
		}
		if !exists {
			for _, u := range s.Config.Users {
				if _, ok := u.Mounts[mount]; ok {
					exists = true
					break
				}
			}
		}
		if exists {
			http.Error(w, "Mount taken", http.StatusConflict)
			return
		}
	}
	hashed, _ := config.HashPassword(password)
	_ = s.mutateConfig(func(cfg *config.Config) error {
		if user.Role == config.RoleSuperAdmin {
			cfg.Mounts[mount] = hashed
		} else {
			user.Mounts[mount] = hashed
		}
		return nil
	})
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleRemoveMount(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		return
	}
	mount := r.FormValue("mount")
	if !s.hasAccess(user, mount) {
		return
	}
	_ = s.mutateConfig(func(cfg *config.Config) error {
		delete(cfg.Mounts, mount)
		delete(cfg.DisabledMounts, mount)
		delete(cfg.VisibleMounts, mount)
		delete(user.Mounts, mount)
		return nil
	})
	s.Relay.RemoveStream(mount)
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleToggleLatency(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		return
	}
	user, ok := s.checkAuth(r)
	if !ok || user.Role != config.RoleSuperAdmin {
		return
	}
	_ = s.mutateConfig(func(cfg *config.Config) error {
		cfg.LowLatencyMode = !cfg.LowLatencyMode
		s.Relay.LowLatency = cfg.LowLatencyMode
		return nil
	})
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleMetadata(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	mount, song := r.URL.Query().Get("mount"), r.URL.Query().Get("song")
	if !ok {
		_, p, okAuth := r.BasicAuth()
		if !okAuth {
			w.Header().Set("WWW-Authenticate", `Basic realm="TinyIce"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		allowed := false
		if config.CheckPasswordHash(p, s.Config.DefaultSourcePassword) || config.CheckPasswordHash(p, s.Config.Mounts[mount]) {
			allowed = true
		}
		if !allowed {
			for _, u := range s.Config.Users {
				if config.CheckPasswordHash(p, u.Mounts[mount]) {
					allowed = true
					break
				}
			}
		}
		if !allowed {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	} else {
		if !s.hasAccess(user, mount) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}
	if mount != "" && song != "" {
		if st, ok := s.Relay.GetStream(mount); ok {
			st.SetCurrentSong(song, s.Relay)
		}
	}
	fmt.Fprint(w, "<?xml version=\"1.0\"?>\n<iceresponse><message>OK</message><return>1</return></iceresponse>\n")
}

func (s *Server) handleKick(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	mount := r.FormValue("mount")
	if ok && s.hasAccess(user, mount) {
		s.Relay.RemoveStream(mount)
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleKickAllListeners(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if ok && user.Role == config.RoleSuperAdmin {
		s.Relay.DisconnectAllListeners()
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleToggleMount(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	mount := r.FormValue("mount")
	if ok && s.hasAccess(user, mount) {
		disabled := false
		_ = s.mutateConfig(func(cfg *config.Config) error {
			cfg.DisabledMounts[mount] = !cfg.DisabledMounts[mount]
			disabled = cfg.DisabledMounts[mount]
			return nil
		})
		if disabled {
			s.Relay.RemoveStream(mount)
		}
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleToggleVisible(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		return
	}
	mount := r.FormValue("mount")
	if ok && s.hasAccess(user, mount) {
		visible := false
		_ = s.mutateConfig(func(cfg *config.Config) error {
			cfg.VisibleMounts[mount] = !cfg.VisibleMounts[mount]
			visible = cfg.VisibleMounts[mount]
			return nil
		})
		if st, ok := s.Relay.GetStream(mount); ok {
			st.SetVisible(visible)
		}
		logger.L.Infow("Admin toggled visibility", "mount", mount, "visible", visible)
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleAddUser(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		return
	}
	user, ok := s.checkAuth(r)
	if ok && user.Role == config.RoleSuperAdmin {
		un, pw := r.FormValue("username"), r.FormValue("password")
		if un != "" && pw != "" {
			hp, _ := config.HashPassword(pw)
			_ = s.mutateConfig(func(cfg *config.Config) error {
				cfg.Users[un] = &config.User{Username: un, Password: hp, Role: config.RoleAdmin, Mounts: make(map[string]string)}
				return nil
			})
		}
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleRemoveUser(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		return
	}
	user, ok := s.checkAuth(r)
	if ok && user.Role == config.RoleSuperAdmin {
		un := r.FormValue("username")
		if un != user.Username {
			_ = s.mutateConfig(func(cfg *config.Config) error {
				delete(cfg.Users, un)
				return nil
			})
		}
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleAddBannedIP(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		return
	}
	user, ok := s.checkAuth(r)
	if ok && user.Role == config.RoleSuperAdmin {
		ip := r.FormValue("ip")
		if ip != "" {
			_ = s.mutateConfig(func(cfg *config.Config) error {
				cfg.BannedIPs = append(cfg.BannedIPs, ip)
				return nil
			})
		}
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleRemoveBannedIP(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		return
	}
	user, ok := s.checkAuth(r)
	if ok && user.Role == config.RoleSuperAdmin {
		ip := r.FormValue("ip")
		_ = s.mutateConfig(func(cfg *config.Config) error {
			for i, b := range cfg.BannedIPs {
				if b == ip {
					cfg.BannedIPs = append(cfg.BannedIPs[:i], cfg.BannedIPs[i+1:]...)
					break
				}
			}
			return nil
		})
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleAddWhitelistedIP(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok || user.Role != config.RoleSuperAdmin {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ip := r.FormValue("ip")
	if ip == "" {
		http.Error(w, "IP address cannot be empty", http.StatusBadRequest)
		return
	}

	// Check for duplicates
	for _, existingIP := range s.Config.WhitelistedIPs {
		if existingIP == ip {
			http.Error(w, "IP address already in the whitelist", http.StatusConflict)
			return
		}
	}

	if err := s.mutateConfig(func(cfg *config.Config) error {
		cfg.WhitelistedIPs = append(cfg.WhitelistedIPs, ip)
		sort.Strings(cfg.WhitelistedIPs)
		return nil
	}); err != nil {
		http.Error(w, "Failed to save config", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"ip": ip, "status": "added"})
}

func (s *Server) handleRemoveWhitelistedIP(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok || user.Role != config.RoleSuperAdmin {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ip := r.FormValue("ip")
	if ip == "" {
		http.Error(w, "IP address cannot be empty", http.StatusBadRequest)
		return
	}

	found := false
	if err := s.mutateConfig(func(cfg *config.Config) error {
		for i, b := range cfg.WhitelistedIPs {
			if b == ip {
				cfg.WhitelistedIPs = append(cfg.WhitelistedIPs[:i], cfg.WhitelistedIPs[i+1:]...)
				found = true
				break
			}
		}
		return nil
	}); err != nil {
		http.Error(w, "Failed to save config", http.StatusInternalServerError)
		return
	}

	if !found {
		http.Error(w, "IP not found in whitelist", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"ip": ip, "status": "removed"})
}

func (s *Server) handleClearAuthLockout(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}
	ip := r.FormValue("ip")
	s.authAttemptsMu.Lock()
	delete(s.authAttempts, ip)
	s.authAttemptsMu.Unlock()
	http.Redirect(w, r, "/admin#tab-security", http.StatusSeeOther)
}

func (s *Server) handleClearScanLockout(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}
	ip := r.FormValue("ip")
	s.scanAttemptsMu.Lock()
	delete(s.scanAttempts, ip)
	s.scanAttemptsMu.Unlock()
	http.Redirect(w, r, "/admin#tab-security", http.StatusSeeOther)
}

func (s *Server) handleAddWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	url := r.FormValue("url")
	events := r.Form["events"]

	if url == "" || len(events) == 0 {
		http.Error(w, "URL and at least one event required", http.StatusBadRequest)
		return
	}

	_ = s.mutateConfig(func(cfg *config.Config) error {
		cfg.Webhooks = append(cfg.Webhooks, &config.WebhookConfig{
			URL:     url,
			Events:  events,
			Enabled: true,
		})
		return nil
	})

	http.Redirect(w, r, "/admin#tab-webhooks", http.StatusSeeOther)
}

func (s *Server) handleDeleteWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	url := r.FormValue("url")
	newWHs := []*config.WebhookConfig{}
	for _, wh := range s.Config.Webhooks {
		if wh.URL != url {
			newWHs = append(newWHs, wh)
		}
	}
	_ = s.mutateConfig(func(cfg *config.Config) error {
		cfg.Webhooks = newWHs
		return nil
	})

	http.Redirect(w, r, "/admin#tab-webhooks", http.StatusSeeOther)
}

func (s *Server) handleGetSecurityStats(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.checkAuth(r); !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	type ipStat struct {
		IP        string `json:"ip"`
		Count     int    `json:"count"`
		Locked    bool   `json:"locked"`
		ExpiresIn string `json:"expires_in"`
	}

	authStats := []ipStat{}
	s.authAttemptsMu.Lock()
	for ip, att := range s.authAttempts {
		expires := "0s"
		locked := time.Now().Before(att.LockoutBy)
		if locked {
			expires = time.Until(att.LockoutBy).Round(time.Second).String()
		}
		authStats = append(authStats, ipStat{ip, att.Count, locked, expires})
	}
	s.authAttemptsMu.Unlock()

	scanStats := []ipStat{}
	s.scanAttemptsMu.Lock()
	for ip, att := range s.scanAttempts {
		expires := "0s"
		locked := time.Now().Before(att.LockoutBy)
		if locked {
			expires = time.Until(att.LockoutBy).Round(time.Second).String()
		}
		scanStats = append(scanStats, ipStat{ip, att.Count, locked, expires})
	}
	s.scanAttemptsMu.Unlock()

	sort.Slice(authStats, func(i, j int) bool { return authStats[i].Count > authStats[j].Count })
	sort.Slice(scanStats, func(i, j int) bool { return scanStats[i].Count > scanStats[j].Count })

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"auth_fails": authStats,
		"scanners":   scanStats,
	})
}

func (s *Server) handleHotSwap(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	if err := s.HotSwap(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
