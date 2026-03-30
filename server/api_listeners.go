package server

import (
	"encoding/json"
	"net/http"

	"github.com/DatanoiseTV/tinyice/relay"
)

func (s *Server) apiGetListeners(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	snapshots := s.Relay.Listeners.List()
	filtered := make([]relay.ListenerSnapshot, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if s.hasAccess(user, snapshot.CurrentMount) || s.hasAccess(user, snapshot.RequestedMount) {
			filtered = append(filtered, snapshot)
		}
	}
	jsonResponse(w, filtered)
}

func (s *Server) apiDisconnectListener(w http.ResponseWriter, r *http.Request) {
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
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if body.ID == "" {
		jsonError(w, "Listener ID is required", http.StatusBadRequest)
		return
	}
	listener, ok := s.Relay.Listeners.Listener(body.ID)
	if !ok {
		jsonError(w, "Listener not found", http.StatusNotFound)
		return
	}
	if !s.hasAccess(user, listener.CurrentMount) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	if err := s.Relay.Listeners.Disconnect(body.ID); err != nil {
		jsonError(w, "Listener not found", http.StatusNotFound)
		return
	}
	jsonResponse(w, map[string]string{"status": "ok"})
}

func (s *Server) apiMoveListener(w http.ResponseWriter, r *http.Request) {
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
		ID          string `json:"id"`
		TargetMount string `json:"target_mount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if body.ID == "" || body.TargetMount == "" {
		jsonError(w, "Listener ID and target mount are required", http.StatusBadRequest)
		return
	}
	listener, ok := s.Relay.Listeners.Listener(body.ID)
	if !ok {
		jsonError(w, "Listener not found", http.StatusNotFound)
		return
	}
	if !s.hasAccess(user, listener.CurrentMount) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	if listener.Protocol == relay.ListenerProtocolWebRTC {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "error",
			"warning": "disconnect_required",
		})
		return
	}
	if body.TargetMount[0] != '/' {
		body.TargetMount = "/" + body.TargetMount
	}
	if !s.hasAccess(user, body.TargetMount) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.Relay.GetStream(body.TargetMount); !ok {
		jsonError(w, "Target mount not found", http.StatusNotFound)
		return
	}
	if body.TargetMount == listener.CurrentMount {
		jsonResponse(w, map[string]string{"status": "ok"})
		return
	}
	if err := s.Relay.Listeners.RequestMove(body.ID, body.TargetMount); err != nil {
		jsonError(w, "Listener move failed", http.StatusConflict)
		return
	}
	jsonResponse(w, map[string]string{"status": "ok"})
}
