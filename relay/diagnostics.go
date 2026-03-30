package relay

import (
	"sync"
	"time"

	"github.com/DatanoiseTV/tinyice/logger"
)

type DiagnosticStatus string
type DiagnosticClass string
type DiagnosticActor string

const (
	DiagnosticStatusRunning    DiagnosticStatus = "running"
	DiagnosticStatusDegraded   DiagnosticStatus = "degraded"
	DiagnosticStatusDead       DiagnosticStatus = "dead"
	DiagnosticStatusRecovering DiagnosticStatus = "recovering"
	DiagnosticStatusStopped    DiagnosticStatus = "stopped"
	DiagnosticStatusError      DiagnosticStatus = "error"
)

const (
	DiagnosticClassManualStop         DiagnosticClass = "manual_stop"
	DiagnosticClassAutomaticStop      DiagnosticClass = "automatic_stop"
	DiagnosticClassSourceDisconnect   DiagnosticClass = "source_disconnect"
	DiagnosticClassHealthDegraded     DiagnosticClass = "health_degraded"
	DiagnosticClassHealthDead         DiagnosticClass = "health_dead"
	DiagnosticClassSongCommandFailure DiagnosticClass = "song_command_failure"
	DiagnosticClassSongCommandEmpty   DiagnosticClass = "song_command_empty_output"
	DiagnosticClassSongCommandInvalid DiagnosticClass = "song_command_invalid_file"
	DiagnosticClassPlaylistExhausted  DiagnosticClass = "playlist_exhausted"
	DiagnosticClassStartupFailure     DiagnosticClass = "startup_failure"
	DiagnosticClassRecoveryStarted    DiagnosticClass = "recovery_started"
	DiagnosticClassRecoverySucceeded  DiagnosticClass = "recovery_succeeded"
	DiagnosticClassRecoveryFailed     DiagnosticClass = "recovery_failed"
)

const (
	DiagnosticActorAdmin         DiagnosticActor = "admin"
	DiagnosticActorHealthMonitor DiagnosticActor = "health_monitor"
	DiagnosticActorAutoDJ        DiagnosticActor = "autodj"
	DiagnosticActorRelay         DiagnosticActor = "relay"
	DiagnosticActorIcecastSource DiagnosticActor = "icecast_source"
	DiagnosticActorWebRTC        DiagnosticActor = "webrtc"
	DiagnosticActorRTMP          DiagnosticActor = "rtmp"
	DiagnosticActorSRT           DiagnosticActor = "srt"
	DiagnosticActorSystem        DiagnosticActor = "system"
)

type DiagnosticEntry struct {
	Timestamp time.Time         `json:"timestamp"`
	Status    DiagnosticStatus  `json:"status"`
	Class     DiagnosticClass   `json:"class"`
	Reason    string            `json:"reason"`
	Error     string            `json:"error,omitempty"`
	Actor     DiagnosticActor   `json:"actor"`
	Details   map[string]string `json:"details,omitempty"`
}

type MountDiagnostic struct {
	Mount              string            `json:"mount"`
	Status             DiagnosticStatus  `json:"status"`
	Class              DiagnosticClass   `json:"class"`
	Reason             string            `json:"reason"`
	Error              string            `json:"error,omitempty"`
	Actor              DiagnosticActor   `json:"actor"`
	UpdatedAt          time.Time         `json:"updated_at"`
	LastRecoveryAt     time.Time         `json:"last_recovery_at,omitempty"`
	LastRecoveryResult string            `json:"last_recovery_result,omitempty"`
	History            []DiagnosticEntry `json:"history"`
}

type DiagnosticUpdate struct {
	Mount              string
	Status             DiagnosticStatus
	Class              DiagnosticClass
	Reason             string
	Error              string
	Actor              DiagnosticActor
	Timestamp          time.Time
	Details            map[string]string
	LastRecoveryAt     time.Time
	LastRecoveryResult string
}

type DiagnosticsStore struct {
	mu           sync.RWMutex
	historyLimit int
	mounts       map[string]*MountDiagnostic
}

func NewDiagnosticsStore(historyLimit int) *DiagnosticsStore {
	if historyLimit <= 0 {
		historyLimit = 10
	}
	return &DiagnosticsStore{
		historyLimit: historyLimit,
		mounts:       make(map[string]*MountDiagnostic),
	}
}

func (d *DiagnosticsStore) Record(update DiagnosticUpdate) {
	if d == nil || update.Mount == "" {
		return
	}
	if update.Timestamp.IsZero() {
		update.Timestamp = time.Now()
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	current, ok := d.mounts[update.Mount]
	if !ok {
		current = &MountDiagnostic{Mount: update.Mount}
		d.mounts[update.Mount] = current
	}

	entry := DiagnosticEntry{
		Timestamp: update.Timestamp,
		Status:    update.Status,
		Class:     update.Class,
		Reason:    update.Reason,
		Error:     update.Error,
		Actor:     update.Actor,
		Details:   cloneDiagnosticDetails(update.Details),
	}

	current.Status = update.Status
	current.Class = update.Class
	current.Reason = update.Reason
	current.Error = update.Error
	current.Actor = update.Actor
	current.UpdatedAt = update.Timestamp
	if !update.LastRecoveryAt.IsZero() {
		current.LastRecoveryAt = update.LastRecoveryAt
	}
	if update.LastRecoveryResult != "" {
		current.LastRecoveryResult = update.LastRecoveryResult
	}
	current.History = append(current.History, entry)
	if len(current.History) > d.historyLimit {
		current.History = append([]DiagnosticEntry(nil), current.History[len(current.History)-d.historyLimit:]...)
	}

	if logger.L != nil {
		fields := []interface{}{
			"mount", update.Mount,
			"status", update.Status,
			"class", update.Class,
			"reason", update.Reason,
			"actor", update.Actor,
		}
		if update.Error != "" {
			fields = append(fields, "error", update.Error)
		}
		if update.LastRecoveryResult != "" {
			fields = append(fields, "last_recovery_result", update.LastRecoveryResult)
		}

		switch update.Status {
		case DiagnosticStatusDead, DiagnosticStatusError:
			logger.L.Warnw("Stream diagnostic transition", fields...)
		default:
			logger.L.Infow("Stream diagnostic transition", fields...)
		}
	}
}

func (d *DiagnosticsStore) Current(mount string) (MountDiagnostic, bool) {
	if d == nil {
		return MountDiagnostic{}, false
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	current, ok := d.mounts[mount]
	if !ok {
		return MountDiagnostic{}, false
	}
	return cloneMountDiagnostic(*current), true
}

func cloneMountDiagnostic(in MountDiagnostic) MountDiagnostic {
	out := in
	out.History = append([]DiagnosticEntry(nil), in.History...)
	for i := range out.History {
		out.History[i].Details = cloneDiagnosticDetails(out.History[i].Details)
	}
	return out
}

func cloneDiagnosticDetails(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
