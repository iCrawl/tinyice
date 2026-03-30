package relay

import (
	"errors"
	"sort"
	"sync"
	"time"
)

type ListenerProtocol string

const (
	ListenerProtocolHTTP   ListenerProtocol = "http"
	ListenerProtocolWebRTC ListenerProtocol = "webrtc"
)

type ListenerCommand struct {
	TargetMount string
}

type Listener struct {
	ID                 string
	Protocol           ListenerProtocol
	RequestedMount     string
	CurrentMount       string
	RemoteAddr         string
	UserAgent          string
	Connected          time.Time
	LastStreamSwitchAt time.Time
	Signal             chan struct{}
	DisconnectCh       chan struct{}
	MoveCh             chan ListenerCommand
}

type ListenerSnapshot struct {
	ID                 string           `json:"id"`
	Protocol           ListenerProtocol `json:"protocol"`
	RequestedMount     string           `json:"requested_mount"`
	CurrentMount       string           `json:"current_mount"`
	RemoteAddr         string           `json:"remote_addr"`
	UserAgent          string           `json:"user_agent"`
	ConnectedAt        int64            `json:"connected_at"`
	DurationSeconds    int64            `json:"duration_seconds"`
	LastStreamSwitchAt int64            `json:"last_stream_switch_at"`
}

func (l *Listener) snapshotAt(now time.Time) ListenerSnapshot {
	out := ListenerSnapshot{
		ID:              l.ID,
		Protocol:        l.Protocol,
		RequestedMount:  l.RequestedMount,
		CurrentMount:    l.CurrentMount,
		RemoteAddr:      l.RemoteAddr,
		UserAgent:       l.UserAgent,
		ConnectedAt:     l.Connected.Unix(),
		DurationSeconds: int64(now.Sub(l.Connected).Seconds()),
	}
	if !l.LastStreamSwitchAt.IsZero() {
		out.LastStreamSwitchAt = l.LastStreamSwitchAt.Unix()
	}
	return out
}

type ListenerRegistry struct {
	mu        sync.RWMutex
	listeners map[string]*Listener
	byMount   map[string]map[string]*Listener
}

func NewListenerRegistry() *ListenerRegistry {
	return &ListenerRegistry{
		listeners: make(map[string]*Listener),
		byMount:   make(map[string]map[string]*Listener),
	}
}

func (r *ListenerRegistry) Register(l *Listener) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if l.CurrentMount == "" {
		l.CurrentMount = l.RequestedMount
	}
	r.listeners[l.ID] = l
	if r.byMount[l.CurrentMount] == nil {
		r.byMount[l.CurrentMount] = make(map[string]*Listener)
	}
	r.byMount[l.CurrentMount][l.ID] = l
}

func (r *ListenerRegistry) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	l, ok := r.listeners[id]
	if !ok {
		return
	}
	delete(r.listeners, id)
	if mountListeners := r.byMount[l.CurrentMount]; mountListeners != nil {
		delete(mountListeners, id)
		if len(mountListeners) == 0 {
			delete(r.byMount, l.CurrentMount)
		}
	}
}

func (r *ListenerRegistry) CountForMount(mount string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byMount[mount])
}

func (r *ListenerRegistry) Get(id string) (ListenerSnapshot, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	l, ok := r.listeners[id]
	if !ok {
		return ListenerSnapshot{}, false
	}
	return l.snapshotAt(time.Now()), true
}

func (r *ListenerRegistry) Listener(id string) (*Listener, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	l, ok := r.listeners[id]
	return l, ok
}

func (r *ListenerRegistry) List() []ListenerSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()

	now := time.Now()
	out := make([]ListenerSnapshot, 0, len(r.listeners))
	for _, l := range r.listeners {
		out = append(out, l.snapshotAt(now))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ConnectedAt > out[j].ConnectedAt
	})
	return out
}

func (r *ListenerRegistry) Move(id, target string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	l, ok := r.listeners[id]
	if !ok {
		return errors.New("listener not found")
	}
	if mountListeners := r.byMount[l.CurrentMount]; mountListeners != nil {
		delete(mountListeners, id)
		if len(mountListeners) == 0 {
			delete(r.byMount, l.CurrentMount)
		}
	}
	if r.byMount[target] == nil {
		r.byMount[target] = make(map[string]*Listener)
	}
	l.CurrentMount = target
	l.LastStreamSwitchAt = now
	r.byMount[target][id] = l
	return nil
}

func (r *ListenerRegistry) RequestMove(id, target string) error {
	r.mu.RLock()
	l, ok := r.listeners[id]
	r.mu.RUnlock()
	if !ok {
		return errors.New("listener not found")
	}
	if l.MoveCh == nil {
		return errors.New("listener move unsupported")
	}

	cmd := ListenerCommand{TargetMount: target}
	select {
	case l.MoveCh <- cmd:
		return nil
	default:
	}

	select {
	case <-l.MoveCh:
	default:
	}

	select {
	case l.MoveCh <- cmd:
		return nil
	default:
		return errors.New("listener move queue unavailable")
	}
}

func (r *ListenerRegistry) Disconnect(id string) error {
	r.mu.RLock()
	l, ok := r.listeners[id]
	r.mu.RUnlock()
	if !ok {
		return errors.New("listener not found")
	}
	if l.DisconnectCh != nil {
		select {
		case <-l.DisconnectCh:
		default:
			close(l.DisconnectCh)
		}
	}
	return nil
}
