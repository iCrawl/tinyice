package relay

import (
	"errors"
	"fmt"
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
	MoveID      string
	TargetMount string
}

type Listener struct {
	pendingMount       string
	moveID             string
	moveStatus         string
	moveError          string
	mu                 sync.RWMutex
	ID                 string
	Protocol           ListenerProtocol
	RequestedMount     string
	CurrentMount       string
	RemoteAddr         string
	UserAgent          string
	Connected          time.Time
	LastStreamSwitchAt time.Time
	DisconnectCh       chan struct{}
	MoveCh             chan ListenerCommand
}

type ListenerSnapshot struct {
	MoveID             string           `json:"move_id,omitempty"`
	MoveStatus         string           `json:"move_status,omitempty"`
	MoveError          string           `json:"move_error,omitempty"`
	MoveDestination    string           `json:"move_destination,omitempty"`
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
	l.mu.RLock()
	defer l.mu.RUnlock()

	out := ListenerSnapshot{
		ID:     l.ID,
		MoveID: l.moveID, MoveStatus: l.moveStatus, MoveError: l.moveError, MoveDestination: l.pendingMount,
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

func (l *Listener) CurrentMountName() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.CurrentMount
}

func (l *Listener) RequestedMountName() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.RequestedMount
}

func (l *Listener) SetCurrentMount(mount string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.CurrentMount = mount
}

func (l *Listener) SetRequestedMount(mount string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.RequestedMount = mount
}

type ListenerRegistry struct {
	mu           sync.RWMutex
	listeners    map[string]*Listener
	byMount      map[string]map[string]*Listener
	reservations map[string]map[string]struct{}
	moveSequence uint64
}

func NewListenerRegistry() *ListenerRegistry {
	return &ListenerRegistry{
		listeners:    make(map[string]*Listener),
		reservations: make(map[string]map[string]struct{}),
		byMount:      make(map[string]map[string]*Listener),
	}
}

func (r *ListenerRegistry) Register(l *Listener) { _ = r.Admit(l, 0) }
func (r *ListenerRegistry) Admit(l *Listener, limit int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.listeners[l.ID]; exists {
		return errors.New("listener already registered")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.CurrentMount == "" {
		l.CurrentMount = l.RequestedMount
	}
	if limit > 0 && len(r.byMount[l.CurrentMount])+len(r.reservations[l.CurrentMount]) >= limit {
		return errors.New("mount listener capacity reached")
	}
	r.listeners[l.ID] = l
	if r.byMount[l.CurrentMount] == nil {
		r.byMount[l.CurrentMount] = make(map[string]*Listener)
	}
	r.byMount[l.CurrentMount][l.ID] = l
	return nil
}
func (r *ListenerRegistry) releaseReservationLocked(l *Listener) {
	if l.pendingMount != "" {
		delete(r.reservations[l.pendingMount], l.ID)
		if len(r.reservations[l.pendingMount]) == 0 {
			delete(r.reservations, l.pendingMount)
		}
		l.pendingMount = ""
	}
}
func (r *ListenerRegistry) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	l, ok := r.listeners[id]
	if !ok {
		return
	}
	currentMount := l.CurrentMountName()
	l.mu.Lock()
	r.releaseReservationLocked(l)
	l.mu.Unlock()
	delete(r.listeners, id)
	if mountListeners := r.byMount[currentMount]; mountListeners != nil {
		delete(mountListeners, id)
		if len(mountListeners) == 0 {
			delete(r.byMount, currentMount)
		}
	}
}

func (r *ListenerRegistry) Listener(id string) (*Listener, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	l, ok := r.listeners[id]
	return l, ok
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

func (r *ListenerRegistry) ReserveMove(id, target string, limit int) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reserveLocked(id, target, limit)
}
func (r *ListenerRegistry) reserveLocked(id, target string, limit int) (string, error) {
	l, ok := r.listeners[id]
	if !ok {
		return "", errors.New("listener not found")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pendingMount != "" {
		if l.pendingMount == target {
			return l.moveID, nil
		}
		return "", errors.New("listener move already pending")
	}
	if target == l.CurrentMount {
		return "", nil
	}
	if limit > 0 && len(r.byMount[target])+len(r.reservations[target]) >= limit {
		return "", errors.New("mount listener capacity reached")
	}
	r.moveSequence++
	l.moveID = fmt.Sprintf("move-%d", r.moveSequence)
	l.moveStatus = "pending"
	l.moveError = ""
	l.pendingMount = target
	if r.reservations[target] == nil {
		r.reservations[target] = make(map[string]struct{})
	}
	r.reservations[target][id] = struct{}{}
	return l.moveID, nil
}
func (r *ListenerRegistry) CompleteMove(id, target string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	l, ok := r.listeners[id]
	if !ok {
		return errors.New("listener not found")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.CurrentMount == target && l.pendingMount == "" {
		return nil
	}
	if l.pendingMount != target {
		return errors.New("destination is not reserved")
	}
	delete(r.byMount[l.CurrentMount], id)
	if len(r.byMount[l.CurrentMount]) == 0 {
		delete(r.byMount, l.CurrentMount)
	}
	if r.byMount[target] == nil {
		r.byMount[target] = make(map[string]*Listener)
	}
	r.byMount[target][id] = l
	l.CurrentMount = target
	l.LastStreamSwitchAt = now
	l.moveStatus = "completed"
	r.releaseReservationLocked(l)
	return nil
}
func (r *ListenerRegistry) FailMove(id, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if l := r.listeners[id]; l != nil {
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.pendingMount != "" {
			l.moveStatus = "failed"
			l.moveError = reason
			r.releaseReservationLocked(l)
		}
	}
}
func (r *ListenerRegistry) Move(id, target string, now time.Time) error {
	if _, err := r.ReserveMove(id, target, 0); err != nil {
		return err
	}
	return r.CompleteMove(id, target, now)
}
func (r *ListenerRegistry) RequestMove(id, target string) error {
	_, err := r.RequestMoveWithLimit(id, target, 0)
	return err
}
func (r *ListenerRegistry) RequestMoveWithLimit(id, target string, limit int) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	l := r.listeners[id]
	if l == nil {
		return "", errors.New("listener not found")
	}
	if l.Protocol == ListenerProtocolWebRTC || l.MoveCh == nil {
		return "", errors.New("listener move unsupported")
	}
	l.mu.RLock()
	pending := l.pendingMount != ""
	l.mu.RUnlock()
	if pending {
		return "", errors.New("listener move already pending")
	}
	moveID, err := r.reserveLocked(id, target, limit)
	if err != nil {
		return "", err
	}
	select {
	case l.MoveCh <- ListenerCommand{TargetMount: target, MoveID: moveID}:
		return moveID, nil
	default:
		l.mu.Lock()
		l.moveStatus = "failed"
		l.moveError = "listener move queue unavailable"
		r.releaseReservationLocked(l)
		l.mu.Unlock()
		return "", errors.New("listener move queue unavailable")
	}
}
func (r *ListenerRegistry) Disconnect(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	l := r.listeners[id]
	if l == nil {
		return errors.New("listener not found")
	}
	l.mu.Lock()
	r.releaseReservationLocked(l)
	if l.moveStatus == "pending" {
		l.moveStatus = "failed"
		l.moveError = "listener disconnected"
	}
	l.mu.Unlock()
	if l.DisconnectCh != nil {
		select {
		case <-l.DisconnectCh:
		default:
			close(l.DisconnectCh)
		}
	}
	return nil
}
