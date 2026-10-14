package relay

import (
	"sync"
	"time"
)

type MountSource string

const (
	SourceUnknown   MountSource = "unknown"
	SourceIcecast   MountSource = "icecast"
	SourceAutoDJ    MountSource = "autodj"
	SourceRelay     MountSource = "relay"
	SourceWebRTC    MountSource = "webrtc"
	SourceRTMP      MountSource = "rtmp"
	SourceSRT       MountSource = "srt"
	SourceTranscode MountSource = "transcode"
)

type MountOutput string

const OutputHLS MountOutput = "hls"

type MountRuntime struct {
	Mount            string
	TenantID         string
	Source           MountSource
	SourceGeneration int64
	Outputs          map[MountOutput]time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
	LastActive       time.Time
	owner            *Stream
}
type RuntimeRegistry struct {
	relay         *Relay
	tenantManager *TenantManager
	runtimes      map[string]*MountRuntime
	mu            sync.RWMutex
}

func NewRuntimeRegistry(r *Relay) *RuntimeRegistry {
	return &RuntimeRegistry{relay: r, runtimes: make(map[string]*MountRuntime)}
}
func (rr *RuntimeRegistry) SetTenantManager(tm *TenantManager) {
	rr.mu.Lock()
	rr.tenantManager = tm
	rr.mu.Unlock()
}
func copyRuntime(rt *MountRuntime) *MountRuntime {
	if rt == nil {
		return nil
	}
	out := *rt
	out.owner = nil
	out.Outputs = make(map[MountOutput]time.Time, len(rt.Outputs))
	for k, v := range rt.Outputs {
		out.Outputs[k] = v
	}
	return &out
}
func (rr *RuntimeRegistry) ensureLocked(mount string) *MountRuntime {
	if rt := rr.runtimes[mount]; rt != nil {
		return rt
	}
	now := time.Now()
	rt := &MountRuntime{Mount: mount, Source: SourceUnknown, Outputs: make(map[MountOutput]time.Time), CreatedAt: now, UpdatedAt: now}
	rr.runtimes[mount] = rt
	return rt
}
func (rr *RuntimeRegistry) GetOrCreate(mount string) *MountRuntime {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	return copyRuntime(rr.ensureLocked(mount))
}
func (rr *RuntimeRegistry) Get(mount string) (*MountRuntime, bool) {
	rr.mu.RLock()
	defer rr.mu.RUnlock()
	rt, ok := rr.runtimes[mount]
	return copyRuntime(rt), ok
}
func (rr *RuntimeRegistry) tenantLocked(rt *MountRuntime, tenantID string) {
	if rr.tenantManager != nil && rt.TenantID != "" && rt.TenantID != tenantID {
		if t := rr.tenantManager.GetTenant(rt.TenantID); t != nil {
			t.DetachMount(rt.Mount)
		}
	}
	rt.TenantID = tenantID
	if rr.tenantManager != nil && tenantID != "" {
		if t := rr.tenantManager.GetTenant(tenantID); t != nil {
			t.AttachMount(rt.Mount)
		}
	}
}
func (rr *RuntimeRegistry) AttachSource(mount string, src MountSource, tenantID string) {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	rt := rr.ensureLocked(mount)
	rr.tenantLocked(rt, tenantID)
	rt.Source = src
	rt.UpdatedAt = time.Now()
	rt.LastActive = rt.UpdatedAt
}
func (rr *RuntimeRegistry) AttachClaim(stream *Stream, token int64, src MountSource, tenantID string) {
	if !stream.IsCurrentSource(token) {
		return
	}
	rr.mu.Lock()
	defer rr.mu.Unlock()
	rt := rr.ensureLocked(stream.MountName)
	rr.tenantLocked(rt, tenantID)
	rt.Source = src
	rt.SourceGeneration = token
	rt.owner = stream
	rt.UpdatedAt = time.Now()
	rt.LastActive = rt.UpdatedAt
}
func (rr *RuntimeRegistry) DetachClaim(stream *Stream, token int64) {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	rt := rr.runtimes[stream.MountName]
	if rt == nil || rt.owner != stream || rt.SourceGeneration != token {
		return
	}
	rr.tenantLocked(rt, "")
	rt.Source = SourceUnknown
	rt.owner = nil
	rt.SourceGeneration = 0
	rt.UpdatedAt = time.Now()
}
func (rr *RuntimeRegistry) AttachOutput(mount string, output MountOutput) {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	rt := rr.ensureLocked(mount)
	rt.Outputs[output] = time.Now()
	rt.UpdatedAt = time.Now()
}
func (rr *RuntimeRegistry) DetachOutput(mount string, output MountOutput) {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	if rt := rr.runtimes[mount]; rt != nil {
		delete(rt.Outputs, output)
		rt.UpdatedAt = time.Now()
	}
}
func (rr *RuntimeRegistry) Remove(mount string) {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	if rt := rr.runtimes[mount]; rt != nil {
		rr.tenantLocked(rt, "")
	}
	delete(rr.runtimes, mount)
}
