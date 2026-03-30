package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/logger"
)

func (s *Server) handleGetPendingUsers(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok || user.Role != config.RoleSuperAdmin {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var active []*config.PendingUser
	for _, p := range s.Config.PendingUsers {
		if p.DeniedAt == "" {
			active = append(active, p)
		}
	}
	if active == nil {
		active = []*config.PendingUser{}
	}
	jsonResponse(w, active)
}

func (s *Server) handleApprovePendingUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok || user.Role != config.RoleSuperAdmin {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		ID       string `json:"id"`
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if req.Username == "" {
		jsonError(w, "Username is required", http.StatusBadRequest)
		return
	}

	role := req.Role
	if role == "" {
		role = config.RoleDJ
	}
	if role != config.RoleSuperAdmin && role != config.RoleAdmin && role != config.RoleDJ {
		jsonError(w, "Invalid role", http.StatusBadRequest)
		return
	}

	var pending *config.PendingUser
	if err := s.mutateConfig(func(cfg *config.Config) error {
		var pendingIdx int
		for i, p := range cfg.PendingUsers {
			if p.ID == req.ID {
				pending = p
				pendingIdx = i
				break
			}
		}

		if pending == nil {
			return errPendingUserNotFound
		}

		if _, exists := cfg.Users[req.Username]; exists {
			return errUsernameTaken
		}

		cfg.Users[req.Username] = &config.User{
			Username:     req.Username,
			Password:     "",
			Role:         role,
			Mounts:       make(map[string]string),
			LinkedEmails: []string{pending.Email},
		}
		cfg.PendingUsers = append(cfg.PendingUsers[:pendingIdx], cfg.PendingUsers[pendingIdx+1:]...)
		return nil
	}); err != nil {
		switch err {
		case errPendingUserNotFound:
			jsonError(w, "Pending user not found", http.StatusNotFound)
		case errUsernameTaken:
			jsonError(w, "Username already taken", http.StatusConflict)
		default:
			jsonError(w, "Failed to save config", http.StatusInternalServerError)
		}
		return
	}

	logger.L.Infow("Pending user approved", "email", pending.Email, "username", req.Username, "role", role, "approved_by", user.Username)
	jsonResponse(w, map[string]any{"success": true, "username": req.Username})
	s.Audit(r, "pending_approved", "user", req.Username, pending.Email)
}

func (s *Server) handleDenyPendingUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok || user.Role != config.RoleSuperAdmin {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "Invalid request", http.StatusBadRequest)
		return
	}

	var deniedEmail string
	if err := s.mutateConfig(func(cfg *config.Config) error {
		for _, p := range cfg.PendingUsers {
			if p.ID == req.ID {
				p.DeniedAt = time.Now().Format(time.RFC3339)
				deniedEmail = p.Email
				return nil
			}
		}
		return errPendingUserNotFound
	}); err != nil {
		if err == errPendingUserNotFound {
			jsonError(w, "Pending user not found", http.StatusNotFound)
			return
		}
		jsonError(w, "Failed to save config", http.StatusInternalServerError)
		return
	}

	logger.L.Infow("Pending user denied", "email", deniedEmail, "denied_by", user.Username)
	jsonResponse(w, map[string]bool{"success": true})
	s.Audit(r, "pending_denied", "user", deniedEmail, "")
}
