package server

import (
	"encoding/json"
	"net"
	"net/http"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/relay"
)

func jsonResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, msg string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (s *Server) requireMountAccess(w http.ResponseWriter, r *http.Request, mount string) (*config.User, bool) {
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return nil, false
	}
	if !s.hasAccess(user, mount) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return nil, false
	}
	return user, true
}

type diagnosticInfo struct {
	Status             string                  `json:"status"`
	StatusClass        string                  `json:"status_class"`
	StatusReason       string                  `json:"status_reason"`
	LastError          string                  `json:"last_error,omitempty"`
	StatusUpdatedAt    int64                   `json:"status_updated_at"`
	LastRecoveryAt     int64                   `json:"last_recovery_at,omitempty"`
	LastRecoveryResult string                  `json:"last_recovery_result,omitempty"`
	History            []relay.DiagnosticEntry `json:"history"`
}

func diagnosticInfoFor(r *relay.Relay, mount string) diagnosticInfo {
	if r == nil || r.Diagnostics == nil {
		return diagnosticInfo{History: []relay.DiagnosticEntry{}}
	}

	info := diagnosticInfo{History: []relay.DiagnosticEntry{}}
	if current, ok := r.Diagnostics.Current(mount); ok {
		info = diagnosticInfo{
			Status:             string(current.Status),
			StatusClass:        string(current.Class),
			StatusReason:       current.Reason,
			LastError:          current.Error,
			StatusUpdatedAt:    current.UpdatedAt.Unix(),
			LastRecoveryResult: current.LastRecoveryResult,
			History:            current.History,
		}
		if !current.LastRecoveryAt.IsZero() {
			info.LastRecoveryAt = current.LastRecoveryAt.Unix()
		}
	}
	if r.History != nil {
		info.History = r.History.GetDiagnostics(mount, 10)
		if info.History == nil {
			info.History = []relay.DiagnosticEntry{}
		}
		if info.Status == "" && len(info.History) > 0 {
			latest := info.History[0]
			info.Status = string(latest.Status)
			info.StatusClass = string(latest.Class)
			info.StatusReason = latest.Reason
			info.LastError = latest.Error
			info.StatusUpdatedAt = latest.Timestamp.Unix()
		}
	}
	return info
}

func (s *Server) Audit(r *http.Request, action, resourceType, resourceID, detail string) {
	if !s.Config.AuditEnabled {
		return
	}
	username := "system"
	if user, ok := s.checkAuth(r); ok {
		username = user.Username
	}
	ip := r.RemoteAddr
	if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host
	}
	s.Relay.History.RecordAudit(username, action, resourceType, resourceID, detail, ip)
}
