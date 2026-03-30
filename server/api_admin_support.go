package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
)

func (s *Server) apiGetUsers(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	type userInfo struct {
		Username string   `json:"username"`
		Role     string   `json:"role"`
		Mounts   []string `json:"mounts"`
	}
	var result []userInfo
	for _, u := range s.Config.Users {
		mounts := make([]string, 0, len(u.Mounts))
		for m := range u.Mounts {
			mounts = append(mounts, m)
		}
		result = append(result, userInfo{Username: u.Username, Role: u.Role, Mounts: mounts})
	}
	if result == nil {
		result = []userInfo{}
	}
	jsonResponse(w, result)
}

func (s *Server) apiCreateUser(w http.ResponseWriter, r *http.Request) {
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
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if body.Username == "" || body.Password == "" {
		jsonError(w, "Username and password are required", http.StatusBadRequest)
		return
	}
	if body.Role == "" {
		body.Role = config.RoleAdmin
	}

	hp, _ := config.HashPassword(body.Password)
	if err := s.mutateConfig(func(cfg *config.Config) error {
		cfg.Users[body.Username] = &config.User{
			Username: body.Username,
			Password: hp,
			Role:     body.Role,
			Mounts:   make(map[string]string),
		}
		return nil
	}); err != nil {
		jsonError(w, "Failed to save config", http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "created", "username": body.Username})
	s.Audit(r, "user_created", "user", body.Username, body.Role)
}

func (s *Server) apiUpdateUser(w http.ResponseWriter, r *http.Request) {
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
		Username string `json:"username"`
		Password string `json:"password,omitempty"`
		Role     string `json:"role,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if body.Username == "" {
		jsonError(w, "Username is required", http.StatusBadRequest)
		return
	}

	u, exists := s.Config.Users[body.Username]
	if !exists {
		jsonError(w, "User not found", http.StatusNotFound)
		return
	}
	if body.Password != "" {
		hp, _ := config.HashPassword(body.Password)
		u.Password = hp
	}
	if body.Role != "" {
		u.Role = body.Role
	}
	if err := s.saveConfig(); err != nil {
		jsonError(w, "Failed to save config", http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "updated"})
	s.Audit(r, "user_updated", "user", body.Username, "")
}

func (s *Server) apiDeleteUser(w http.ResponseWriter, r *http.Request) {
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

	username := r.URL.Query().Get("username")
	if username == "" {
		jsonError(w, "Username is required", http.StatusBadRequest)
		return
	}
	if username == user.Username {
		jsonError(w, "Cannot delete yourself", http.StatusBadRequest)
		return
	}
	if _, exists := s.Config.Users[username]; !exists {
		jsonError(w, "User not found", http.StatusNotFound)
		return
	}
	if err := s.mutateConfig(func(cfg *config.Config) error {
		delete(cfg.Users, username)
		return nil
	}); err != nil {
		jsonError(w, "Failed to save config", http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "deleted"})
	s.Audit(r, "user_deleted", "user", username, "")
}

func (s *Server) apiGetBans(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	jsonResponse(w, s.Config.BannedIPs)
}

func (s *Server) apiAddBan(w http.ResponseWriter, r *http.Request) {
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
		IP string `json:"ip"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IP == "" {
		jsonError(w, "IP is required", http.StatusBadRequest)
		return
	}
	if err := s.mutateConfig(func(cfg *config.Config) error {
		cfg.AddBannedIP(body.IP)
		return nil
	}); err != nil {
		jsonError(w, "Failed to save config", http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "added", "ip": body.IP})
	s.Audit(r, "ip_banned", "security", body.IP, "")
}

func (s *Server) apiRemoveBan(w http.ResponseWriter, r *http.Request) {
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

	ip := r.URL.Query().Get("ip")
	if ip == "" {
		jsonError(w, "IP is required", http.StatusBadRequest)
		return
	}
	if err := s.mutateConfig(func(cfg *config.Config) error {
		cfg.RemoveBannedIP(ip)
		return nil
	}); err != nil {
		jsonError(w, "Failed to save config", http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "removed", "ip": ip})
	s.Audit(r, "ip_unbanned", "security", ip, "")
}

func (s *Server) apiGetWhitelist(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	jsonResponse(w, s.Config.WhitelistedIPs)
}

func (s *Server) apiAddWhitelist(w http.ResponseWriter, r *http.Request) {
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
		IP string `json:"ip"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IP == "" {
		jsonError(w, "IP is required", http.StatusBadRequest)
		return
	}
	if err := s.mutateConfig(func(cfg *config.Config) error {
		cfg.AddWhitelistedIP(body.IP)
		return nil
	}); err != nil {
		jsonError(w, "Failed to save config", http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "added", "ip": body.IP})
	s.Audit(r, "ip_whitelisted", "security", body.IP, "")
}

func (s *Server) apiRemoveWhitelist(w http.ResponseWriter, r *http.Request) {
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

	ip := r.URL.Query().Get("ip")
	if ip == "" {
		jsonError(w, "IP is required", http.StatusBadRequest)
		return
	}
	if err := s.mutateConfig(func(cfg *config.Config) error {
		cfg.RemoveWhitelistedIP(ip)
		return nil
	}); err != nil {
		jsonError(w, "Failed to save config", http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "removed", "ip": ip})
	s.Audit(r, "ip_unwhitelisted", "security", ip, "")
}

func (s *Server) apiGetBranding(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.checkAuth(r); !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	jsonResponse(w, map[string]interface{}{
		"page_title":       s.Config.PageTitle,
		"page_subtitle":    s.Config.PageSubtitle,
		"accent_color":     s.Config.AccentColor,
		"logo_path":        s.Config.LogoPath,
		"landing_markdown": s.Config.LandingMarkdown,
	})
}

func (s *Server) apiUpdateBranding(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var body struct {
		PageTitle       *string `json:"page_title"`
		PageSubtitle    *string `json:"page_subtitle"`
		AccentColor     *string `json:"accent_color"`
		LogoPath        *string `json:"logo_path"`
		LandingMarkdown *string `json:"landing_markdown"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if err := s.mutateConfig(func(cfg *config.Config) error {
		if body.PageTitle != nil {
			cfg.PageTitle = *body.PageTitle
		}
		if body.PageSubtitle != nil {
			cfg.PageSubtitle = *body.PageSubtitle
		}
		if body.AccentColor != nil {
			cfg.AccentColor = *body.AccentColor
		}
		if body.LogoPath != nil {
			cfg.LogoPath = *body.LogoPath
		}
		if body.LandingMarkdown != nil {
			cfg.LandingMarkdown = *body.LandingMarkdown
		}
		return nil
	}); err != nil {
		jsonError(w, "Failed to save config", http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "updated"})
	s.Audit(r, "branding_updated", "branding", "", "")
}

func (s *Server) apiUploadLogo(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if err := r.ParseMultipartForm(2 << 20); err != nil {
		jsonError(w, "File too large (max 2MB)", http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("logo")
	if err != nil {
		jsonError(w, "No file uploaded", http.StatusBadRequest)
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext == "" {
		ext = ".png"
	}
	destPath := filepath.Join("branding", "logo"+ext)

	os.MkdirAll("branding", 0755)
	dst, err := os.Create(destPath)
	if err != nil {
		jsonError(w, "Failed to save logo", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	if _, err := dst.ReadFrom(file); err != nil {
		jsonError(w, "Failed to write logo", http.StatusInternalServerError)
		return
	}

	if err := s.mutateConfig(func(cfg *config.Config) error {
		cfg.LogoPath = destPath
		return nil
	}); err != nil {
		jsonError(w, "Failed to save config", http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "ok", "path": destPath})
	s.Audit(r, "logo_uploaded", "branding", destPath, "")
}

func (s *Server) handleServeLogo(w http.ResponseWriter, r *http.Request) {
	if s.Config.LogoPath == "" {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, s.Config.LogoPath)
}

func (s *Server) apiGetTokens(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	type tokenInfo struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Username   string `json:"username"`
		CreatedAt  string `json:"created_at"`
		LastUsedAt string `json:"last_used_at"`
		LastUsedIP string `json:"last_used_ip"`
		ExpiresAt  string `json:"expires_at"`
		Prefix     string `json:"prefix"`
	}

	var result []tokenInfo
	for _, tok := range s.Config.APITokens {
		if user.Role != config.RoleSuperAdmin && tok.Username != user.Username {
			continue
		}
		prefix := "ti_XXXX..."
		if len(tok.TokenHash) >= 8 {
			prefix = "ti_" + tok.TokenHash[:4] + "..."
		}
		result = append(result, tokenInfo{
			ID:         tok.ID,
			Name:       tok.Name,
			Username:   tok.Username,
			CreatedAt:  tok.CreatedAt,
			LastUsedAt: tok.LastUsedAt,
			LastUsedIP: tok.LastUsedIP,
			ExpiresAt:  tok.ExpiresAt,
			Prefix:     prefix,
		})
	}
	if result == nil {
		result = []tokenInfo{}
	}
	jsonResponse(w, result)
}

func (s *Server) apiCreateToken(w http.ResponseWriter, r *http.Request) {
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
		Name      string `json:"name"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if body.Name == "" {
		jsonError(w, "Name is required", http.StatusBadRequest)
		return
	}

	raw, err := generateToken()
	if err != nil {
		jsonError(w, "Failed to generate token", http.StatusInternalServerError)
		return
	}

	hash := hashToken(raw)
	id := hash[:16]

	tok := &config.APIToken{
		ID:        id,
		Name:      body.Name,
		TokenHash: hash,
		Username:  user.Username,
		Role:      user.Role,
		CreatedAt: time.Now().Format(time.RFC3339),
		ExpiresAt: body.ExpiresAt,
	}

	if err := s.mutateConfig(func(cfg *config.Config) error {
		cfg.APITokens = append(cfg.APITokens, tok)
		return nil
	}); err != nil {
		jsonError(w, "Failed to save config", http.StatusInternalServerError)
		return
	}

	jsonResponse(w, map[string]string{
		"id":    id,
		"token": raw,
		"name":  body.Name,
	})
	s.Audit(r, "token_created", "auth", body.Name, "")
}

func (s *Server) apiDeleteToken(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		jsonError(w, "ID is required", http.StatusBadRequest)
		return
	}

	newTokens := make([]*config.APIToken, 0, len(s.Config.APITokens))
	found := false
	for _, tok := range s.Config.APITokens {
		if tok.ID == id {
			if user.Role != config.RoleSuperAdmin && tok.Username != user.Username {
				jsonError(w, "Forbidden", http.StatusForbidden)
				return
			}
			found = true
			continue
		}
		newTokens = append(newTokens, tok)
	}
	if !found {
		jsonError(w, "Token not found", http.StatusNotFound)
		return
	}

	if err := s.mutateConfig(func(cfg *config.Config) error {
		cfg.APITokens = newTokens
		return nil
	}); err != nil {
		jsonError(w, "Failed to save config", http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "deleted"})
	s.Audit(r, "token_revoked", "auth", id, "")
}

func (s *Server) apiGetSettings(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	jsonResponse(w, map[string]interface{}{
		"bind_host":         s.Config.BindHost,
		"port":              s.Config.Port,
		"hostname":          s.Config.HostName,
		"base_url":          s.Config.BaseURL,
		"location":          s.Config.Location,
		"admin_email":       s.Config.AdminEmail,
		"admin_user":        s.Config.AdminUser,
		"low_latency_mode":  s.Config.LowLatencyMode,
		"max_listeners":     s.Config.MaxListeners,
		"use_https":         s.Config.UseHTTPS,
		"auto_https":        s.Config.AutoHTTPS,
		"https_port":        s.Config.HTTPSPort,
		"acme_email":        s.Config.ACMEEmail,
		"domains":           s.Config.Domains,
		"directory_listing": s.Config.DirectoryListing,
		"directory_server":  s.Config.DirectoryServer,
		"auto_update":       s.Config.AutoUpdate,
		"audit_enabled":     s.Config.AuditEnabled,
	})
}

func (s *Server) apiUpdateSettings(w http.ResponseWriter, r *http.Request) {
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

	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if err := s.mutateConfig(func(cfg *config.Config) error {
		if v, ok := body["hostname"]; ok {
			cfg.HostName = fmt.Sprintf("%v", v)
		}
		if v, ok := body["base_url"]; ok {
			cfg.BaseURL = fmt.Sprintf("%v", v)
		}
		if v, ok := body["location"]; ok {
			cfg.Location = fmt.Sprintf("%v", v)
		}
		if v, ok := body["admin_email"]; ok {
			cfg.AdminEmail = fmt.Sprintf("%v", v)
		}
		if v, ok := body["low_latency_mode"]; ok {
			if b, ok := v.(bool); ok {
				cfg.LowLatencyMode = b
				s.Relay.LowLatency = b
			}
		}
		if v, ok := body["max_listeners"]; ok {
			if n, ok := v.(float64); ok {
				cfg.MaxListeners = int(n)
			}
		}
		if v, ok := body["directory_listing"]; ok {
			if b, ok := v.(bool); ok {
				cfg.DirectoryListing = b
			}
		}
		if v, ok := body["auto_update"]; ok {
			if b, ok := v.(bool); ok {
				cfg.AutoUpdate = b
			}
		}
		if v, ok := body["audit_enabled"]; ok {
			if b, ok := v.(bool); ok {
				cfg.AuditEnabled = b
			}
		}
		return nil
	}); err != nil {
		jsonError(w, "Failed to save config", http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "updated"})
	s.Audit(r, "settings_updated", "settings", "", "")
}
