package relay

import (
	"sync"
	"time"
)

type MountSource string

const (
	SourceUnknown MountSource = "unknown"
	SourceIcecast MountSource = "icecast"
	SourceAutoDJ  MountSource = "autodj"
	SourceRelay   MountSource = "relay"
	SourceWebRTC  MountSource = "webrtc"
	SourceRTMP    MountSource = "rtmp"
	SourceSRT     MountSource = "srt"
)

type MountOutput string

const (
	OutputHLS MountOutput = "hls"
)

type MountRuntime struct {
	Mount      string
	Stream     *Stream
	TenantID   string
	Source     MountSource
	Outputs    map[MountOutput]time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
	LastActive time.Time
}

type RuntimeRegistry struct {
	relay         *Relay
	tenantManager *TenantManager
	runtimes      map[string]*MountRuntime
	mu            sync.RWMutex
}

func NewRuntimeRegistry(r *Relay) *RuntimeRegistry {
	return &RuntimeRegistry{
		relay:    r,
		runtimes: make(map[string]*MountRuntime),
	}
}

func (rr *RuntimeRegistry) SetTenantManager(tm *TenantManager) {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	rr.tenantManager = tm
}

func (rr *RuntimeRegistry) GetOrCreate(mount string) *MountRuntime {
	rr.mu.Lock()
	defer rr.mu.Unlock()

	if rt, ok := rr.runtimes[mount]; ok {
		return rt
	}

	now := time.Now()
	rt := &MountRuntime{
		Mount:      mount,
		Stream:     rr.relay.GetOrCreateStream(mount),
		Source:     SourceUnknown,
		Outputs:    make(map[MountOutput]time.Time),
		CreatedAt:  now,
		UpdatedAt:  now,
		LastActive: now,
	}
	rr.runtimes[mount] = rt
	return rt
}

func (rr *RuntimeRegistry) Get(mount string) (*MountRuntime, bool) {
	rr.mu.RLock()
	defer rr.mu.RUnlock()
	rt, ok := rr.runtimes[mount]
	return rt, ok
}

func (rr *RuntimeRegistry) AttachSource(mount string, src MountSource, tenantID string) {
	rt := rr.GetOrCreate(mount)

	rr.mu.Lock()
	defer rr.mu.Unlock()
	if rr.tenantManager != nil && rt.TenantID != "" && rt.TenantID != tenantID {
		if tenant := rr.tenantManager.GetTenant(rt.TenantID); tenant != nil {
			tenant.DetachMount(mount)
		}
	}
	rt.Source = src
	rt.TenantID = tenantID
	rt.UpdatedAt = time.Now()
	rt.LastActive = rt.UpdatedAt
	if rr.tenantManager != nil && tenantID != "" {
		if tenant := rr.tenantManager.GetTenant(tenantID); tenant != nil {
			tenant.AttachMount(mount)
		}
	}
}

func (rr *RuntimeRegistry) AttachOutput(mount string, output MountOutput) {
	rt := rr.GetOrCreate(mount)

	rr.mu.Lock()
	defer rr.mu.Unlock()
	rt.Outputs[output] = time.Now()
	rt.UpdatedAt = time.Now()
}

func (rr *RuntimeRegistry) DetachOutput(mount string, output MountOutput) {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	if rt, ok := rr.runtimes[mount]; ok {
		delete(rt.Outputs, output)
		rt.UpdatedAt = time.Now()
	}
}

func (rr *RuntimeRegistry) Remove(mount string) {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	if rt, ok := rr.runtimes[mount]; ok && rr.tenantManager != nil && rt.TenantID != "" {
		if tenant := rr.tenantManager.GetTenant(rt.TenantID); tenant != nil {
			tenant.DetachMount(mount)
		}
	}
	delete(rr.runtimes, mount)
}
