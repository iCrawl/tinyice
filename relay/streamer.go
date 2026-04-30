package relay

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/logger"
	"github.com/bogem/id3v2/v2"
	"github.com/dhowden/tag"
)

type StreamerState int

const (
	StateStopped StreamerState = iota
	StatePlaying
	StatePaused
)

type Streamer struct {
	Name               string
	OutputMount        string
	MusicDir           string
	Format             string
	Bitrate            int
	Playlist           []PlaylistSong
	Queue              []string
	CurrentPos         int
	State              StreamerState
	Loop               bool
	Shuffle            bool
	InjectMetadata     bool
	Visible            bool
	MPDPassword        string
	LastPlaylist       string
	SongCommand        string
	SongCommandTimeout int
	Volume             float64 // 0..1 playback gain applied before encode
	manualStop         bool

	relay  *Relay
	cancel context.CancelFunc
	mu     sync.RWMutex

	fileCancel    context.CancelFunc
	outputSession *AutoDJOutputSession
	titleCache    map[string]string
	titleFetchWg  sync.WaitGroup

	// Stats
	BytesStreamed       int64
	CurrentFile         string
	CurrentArtist       string
	CurrentTitle        string
	CurrentAlbum        string
	CurrentFilePath     string
	CurrentPlayingPos   int
	CurrentPlayingID    int
	CurrentSampleRate   int
	CurrentChannels     int
	CurrentFileTime     time.Time
	CurrentFileDuration time.Duration
	MPDServer           *MPDServer
	NextID              int
	PlaylistVersion     uint32
	runtimeRegistry     *RuntimeRegistry
	idleCh              chan string
	stateCh             chan struct{}
}

type StreamerManager struct {
	instances       map[string]*Streamer // key is OutputMount
	mu              sync.RWMutex
	relay           *Relay
	runtimeRegistry *RuntimeRegistry
	config          *config.Config

	deadRecovery map[string]context.CancelFunc

	recoveryExecSongCommand func(*Streamer) (string, error)
	recoveryAfter           func(time.Duration) <-chan time.Time
	recoveryActivatePath    func(context.Context, *StreamerManager, *Streamer, string) error
}

func NewStreamerManager(r *Relay, cfg *config.Config) *StreamerManager {
	sm := &StreamerManager{
		instances:    make(map[string]*Streamer),
		relay:        r,
		config:       cfg,
		deadRecovery: make(map[string]context.CancelFunc),
	}
	sm.recoveryExecSongCommand = func(s *Streamer) (string, error) { return s.execSongCommand() }
	sm.recoveryAfter = time.After
	sm.recoveryActivatePath = func(ctx context.Context, sm *StreamerManager, s *Streamer, path string) error {
		return sm.activateRecoveredSongCommandPath(ctx, s, path)
	}
	return sm
}

func (sm *StreamerManager) SetRuntimeRegistry(rr *RuntimeRegistry) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.runtimeRegistry = rr
	for _, inst := range sm.instances {
		inst.runtimeRegistry = rr
	}
}

func (sm *StreamerManager) DeadRecoveryActive(mount string) bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	_, ok := sm.deadRecovery[mount]
	return ok
}

func (sm *StreamerManager) RecoverDeadSongCommandMount(mount string) {
	sm.mu.Lock()
	streamer, ok := sm.instances[mount]
	if !ok || streamer.SongCommand == "" {
		sm.mu.Unlock()
		return
	}
	if _, running := sm.deadRecovery[mount]; running {
		sm.mu.Unlock()
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	sm.deadRecovery[mount] = cancel
	sm.mu.Unlock()

	go sm.runDeadSongCommandRecovery(ctx, streamer)
}

func (sm *StreamerManager) clearDeadRecovery(mount string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.deadRecovery, mount)
}

func (sm *StreamerManager) runDeadSongCommandRecovery(ctx context.Context, s *Streamer) {
	defer sm.clearDeadRecovery(s.OutputMount)

	if sm.recoveryExecSongCommand == nil {
		return
	}

	if s.relay != nil && s.OutputMount != "" {
		s.relay.Diagnostics.Record(DiagnosticUpdate{
			Mount:     s.OutputMount,
			Status:    DiagnosticStatusRecovering,
			Class:     DiagnosticClassRecoveryStarted,
			Reason:    "retrying song_command after dead health event",
			Actor:     DiagnosticActorAutoDJ,
			Timestamp: time.Now(),
		})
	}

	bo := &backoff{base: time.Second, max: time.Minute}
	for {
		if !sm.streamerEligibleForDeadRecovery(s) {
			return
		}
		if sm.mountHasFreshData(s.OutputMount) {
			return
		}
		path, err := sm.recoveryExecSongCommand(s)
		if err == nil && sm.recoveryActivatePath != nil {
			err = sm.recoveryActivatePath(ctx, sm, s, path)
		}
		if err == nil && sm.mountHasFreshData(s.OutputMount) {
			if s.relay != nil && s.OutputMount != "" {
				now := time.Now()
				s.relay.Diagnostics.Record(DiagnosticUpdate{
					Mount:              s.OutputMount,
					Status:             DiagnosticStatusRunning,
					Class:              DiagnosticClassRecoverySucceeded,
					Reason:             "dead mount recovered and resumed playback",
					Actor:              DiagnosticActorAutoDJ,
					Timestamp:          now,
					LastRecoveryAt:     now,
					LastRecoveryResult: "success",
				})
			}
			return
		}
		if err != nil && s.relay != nil && s.OutputMount != "" {
			now := time.Now()
			s.relay.Diagnostics.Record(DiagnosticUpdate{
				Mount:              s.OutputMount,
				Status:             DiagnosticStatusError,
				Class:              DiagnosticClassRecoveryFailed,
				Reason:             "dead mount recovery attempt failed",
				Error:              err.Error(),
				Actor:              DiagnosticActorAutoDJ,
				Timestamp:          now,
				LastRecoveryAt:     now,
				LastRecoveryResult: "failed",
			})
		}
		delay := bo.next()
		select {
		case <-ctx.Done():
			return
		case <-sm.recoveryAfter(delay):
		}
	}
}

func (sm *StreamerManager) streamerEligibleForDeadRecovery(s *Streamer) bool {
	if s == nil || s.SongCommand == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.manualStop {
		return false
	}
	return s.State == StatePlaying || s.State == StateStopped
}

func (sm *StreamerManager) mountHasFreshData(mount string) bool {
	stream, ok := sm.relay.GetStream(mount)
	if !ok {
		return false
	}
	stream.mu.RLock()
	last := stream.LastDataReceived
	stream.mu.RUnlock()
	return !last.IsZero() && time.Since(last) <= 5*time.Second
}

func (sm *StreamerManager) activateRecoveredSongCommandPath(ctx context.Context, s *Streamer, path string) error {
	s.mu.RLock()
	restartStopped := s.State == StateStopped && !s.manualStop
	s.mu.RUnlock()
	if restartStopped {
		s.Play()
	}

	if err := validateAudioFile(path); err != nil {
		return err
	}

	s.mu.Lock()
	if s.outputSession == nil {
		outputSession, err := NewAutoDJOutputSession(s)
		if err != nil {
			s.mu.Unlock()
			return err
		}
		s.outputSession = outputSession
	}
	outputSession := s.outputSession
	s.mu.Unlock()

	if err := outputSession.Start(ctx); err != nil {
		return err
	}
	return sm.streamFile(ctx, s, path, -1, -1)
}

func (s *Streamer) Play() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.manualStop = false
	s.State = StatePlaying
	s.signalStateChange()
}

func (s *Streamer) Next() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fileCancel != nil {
		s.fileCancel()
	}
}

// Previous rewinds the playlist by one track and cancels the currently
// playing file. The main loop increments CurrentPos after reading, so we
// subtract 2 here: the cancel triggers the next iteration which does
// CurrentPos++ before picking the file, landing one before the current
// track. Stops at the beginning of the playlist rather than underflowing.
func (s *Streamer) Previous() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CurrentPos -= 2
	if s.CurrentPos < 0 {
		s.CurrentPos = 0
	}
	if s.fileCancel != nil {
		s.fileCancel()
	}
}

// SetVolume records a playback gain (0.0 = silence, 1.0 = unity). The
// encode path applies it to every PCM sample before it reaches the MP3 /
// Opus encoder. Clamped to [0, 1] so the operator can't push into clipping
// territory by accident.
func (s *Streamer) SetVolume(v float64) {
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	s.mu.Lock()
	s.Volume = v
	s.mu.Unlock()
}

// Volume returns the current playback gain. 1.0 if never set.
func (s *Streamer) GetVolume() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.Volume <= 0 {
		return 1.0
	}
	return s.Volume
}

func (s *Streamer) TogglePlay() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.State == StatePlaying {
		s.State = StateStopped
	} else {
		s.State = StatePlaying
	}
	s.signalStateChange()
}

func (s *Streamer) ToggleShuffle() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Shuffle = !s.Shuffle
}

func (s *Streamer) ToggleLoop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Loop = !s.Loop
}

func (s *Streamer) ToggleInjectMetadata() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.InjectMetadata = !s.InjectMetadata
}

func (s *Streamer) ClearQueue() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Queue = []string{}
}

func (s *Streamer) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.manualStop = true
	s.State = StateStopped
	if s.fileCancel != nil {
		s.fileCancel()
	}
	s.signalStateChange()
}

func (s *Streamer) Restart() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.CurrentPos > 0 {
		s.CurrentPos--
	}
	if s.fileCancel != nil {
		s.fileCancel()
	}
}

func (s *Streamer) PushToQueue(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Queue = append(s.Queue, path)
}

func (s *Streamer) RemoveFromQueue(index int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if index < 0 || index >= len(s.Queue) {
		return
	}
	s.Queue = append(s.Queue[:index], s.Queue[index+1:]...)
}

func (s *Streamer) MoveQueueItem(from, to int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if from < 0 || from >= len(s.Queue) || to < 0 || to >= len(s.Queue) {
		return
	}
	item := s.Queue[from]
	s.Queue = append(s.Queue[:from], s.Queue[from+1:]...)
	s.Queue = append(s.Queue[:to], append([]string{item}, s.Queue[to:]...)...)
}

type PlaylistSong struct {
	Path string
	ID   int
}

type PlaylistItem struct {
	Title string
	Path  string
	ID    int
}

func (s *Streamer) GetPlaylistInfo() []PlaylistItem {
	s.mu.RLock()
	playlist := make([]PlaylistSong, len(s.Playlist))
	copy(playlist, s.Playlist)
	s.mu.RUnlock()

	res := make([]PlaylistItem, len(playlist))
	for i, p := range playlist {
		res[i] = PlaylistItem{
			Title: s.GetSongTitle(p.Path),
			Path:  p.Path,
			ID:    p.ID,
		}
	}
	return res
}

func (s *Streamer) GetSongTitle(path string) string {
	s.mu.RLock()
	if title, ok := s.titleCache[path]; ok {
		s.mu.RUnlock()
		return title
	}
	s.mu.RUnlock()

	// Trigger background fetch if not already in progress
	go s.fetchTitleAndCache(path)

	// Fallback to filename if no title found (yet)
	return filepath.Base(path)
}

func (s *Streamer) fetchTitleAndCache(path string) {
	s.titleFetchWg.Add(1)
	go func() {
		defer s.titleFetchWg.Done()

		s.mu.Lock()
		if _, ok := s.titleCache[path]; ok {
			s.mu.Unlock()
			return // Already fetched by another concurrent call
		}
		s.mu.Unlock()

		title := filepath.Base(path)

		// Use id3v2 for extraction (Pure Go, no CGO/iconv)
		logger.L.Debugf("fetchTitleAndCache: Opening %s for ID3v2 parsing...", path)
		tag, err := id3v2.Open(path, id3v2.Options{Parse: true})
		if err != nil {
			logger.L.Errorf("fetchTitleAndCache: Failed to open %s for id3v2 parsing: %v", path, err)
		} else {
			defer tag.Close()
			artist := strings.TrimSpace(tag.Artist())
			song := strings.TrimSpace(tag.Title())

			logger.L.Debugf("fetchTitleAndCache: Raw tags for %s: artist=[%s] title=[%s]", path, artist, song)

			if artist != "" && song != "" {
				title = fmt.Sprintf("%s - %s", artist, song)
			} else if song != "" {
				title = song
			}
			logger.L.Debugf("fetchTitleAndCache: Final title for %s set to: %s", path, title)
		}

		s.mu.Lock()
		s.titleCache[path] = title
		s.mu.Unlock()
	}()
}

func (s *Streamer) GetQueueInfo() []PlaylistItem {
	s.mu.RLock()
	queue := make([]string, len(s.Queue))
	copy(queue, s.Queue)
	s.mu.RUnlock()

	res := make([]PlaylistItem, len(queue))
	for i, p := range queue {
		res[i] = PlaylistItem{
			Title: s.GetSongTitle(p),
			Path:  p,
			ID:    -1, // Queue items don't have stable IDs yet
		}
	}
	return res
}

func (s *Streamer) GetPlaylistNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]string, len(s.Playlist))
	for i, p := range s.Playlist {
		res[i] = filepath.Base(p.Path)
	}
	return res
}

func (s *Streamer) GetQueueNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]string, len(s.Queue))
	for i, p := range s.Queue {
		res[i] = filepath.Base(p)
	}
	return res
}

func (s *Streamer) ScanMusicDir() error {
	s.mu.Lock()
	if s.MusicDir == "" {
		s.mu.Unlock()
		return fmt.Errorf("music directory not configured")
	}

	// Clear cache
	s.titleCache = make(map[string]string)

	// Copy playlist to process outside of lock
	currentPlaylist := make([]PlaylistSong, len(s.Playlist))
	copy(currentPlaylist, s.Playlist)
	s.mu.Unlock()

	// Re-verify files and update cache in background
	go func() {
		for _, ps := range currentPlaylist {
			if _, err := os.Stat(ps.Path); err == nil {
				s.fetchTitleAndCache(ps.Path)
			}
		}
	}()

	return nil
}

func (s *Streamer) SavePlaylist() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if err := os.MkdirAll("playlists", 0755); err != nil {
		return err
	}

	path := filepath.Join("playlists", s.Name+".pls")
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	fmt.Fprintf(f, "[playlist]\nNumberOfEntries=%d\n", len(s.Playlist))
	for i, p := range s.Playlist {
		fmt.Fprintf(f, "File%d=%s\n", i+1, p.Path)
		fmt.Fprintf(f, "Title%d=%s\n", i+1, s.GetSongTitle(p.Path))
	}
	fmt.Fprintf(f, "Version=2\n")
	return nil
}

func (s *Streamer) LoadPlaylist(filename string) error {
	if filename == "" {
		filename = s.Name + ".pls"
	}
	path := filepath.Join("playlists", filename)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			if filename == s.Name+".pls" {
				// Only save if we don't have a playlist in memory either
				s.mu.RLock()
				empty := len(s.Playlist) == 0
				s.mu.RUnlock()
				if empty {
					return s.SavePlaylist()
				}
			}
			return nil
		}
		return err
	}
	defer f.Close()

	var newPlaylistPaths []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "[") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(strings.ToLower(line), "file") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				newPlaylistPaths = append(newPlaylistPaths, strings.TrimSpace(parts[1]))
			}
		}
	}

	if len(newPlaylistPaths) > 0 {
		s.mu.Lock()
		s.Playlist = []PlaylistSong{}
		for _, path := range newPlaylistPaths {
			s.Playlist = append(s.Playlist, PlaylistSong{Path: path, ID: s.NextID})
			s.NextID++
		}
		s.LastPlaylist = filename
		s.mu.Unlock()
	}
	return nil
}

func (s *Streamer) GetMusicDir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.MusicDir
}

func (s *Streamer) GetPlaylist() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]string, len(s.Playlist))
	for i, p := range s.Playlist {
		res[i] = p.Path
	}
	return res
}

func (s *Streamer) SetPlaylist(p []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Playlist = []PlaylistSong{}
	for _, path := range p {
		s.Playlist = append(s.Playlist, PlaylistSong{Path: path, ID: s.NextID})
		s.NextID++
	}
	s.PlaylistVersion++
	s.broadcastIdle("playlist")
}

func (s *Streamer) SetLastPlaylist(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LastPlaylist = name
}

func (s *Streamer) AddToPlaylist(path string) {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Playlist = append(s.Playlist, PlaylistSong{Path: path, ID: s.NextID})
	s.NextID++
	s.PlaylistVersion++
	s.broadcastIdle("playlist")
}

func (s *Streamer) RemoveFromPlaylist(idx int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if idx >= 0 && idx < len(s.Playlist) {
		s.Playlist = append(s.Playlist[:idx], s.Playlist[idx+1:]...)
		s.PlaylistVersion++
		s.broadcastIdle("playlist")
	}
}

func (s *Streamer) ClearPlaylist() {
	s.mu.Lock()
	s.Playlist = []PlaylistSong{}
	s.PlaylistVersion++
	s.mu.Unlock()
	s.SavePlaylist()
	s.broadcastIdle("playlist")
}

func (s *Streamer) broadcastIdle(subsystem string) {
	// Non-blocking broadcast
	select {
	case s.idleCh <- subsystem:
	default:
	}
}

func (s *Streamer) signalStateChange() {
	select {
	case s.stateCh <- struct{}{}:
	default:
	}
}

func (s *Streamer) execSongCommand() (string, error) {
	if s.SongCommand == "" {
		return "", fmt.Errorf("no song command configured")
	}

	timeout := s.SongCommandTimeout
	if timeout <= 0 {
		timeout = 5
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", s.SongCommand)
	cmd.Dir = s.MusicDir

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("song command failed: %w", err)
	}

	filePath := strings.TrimSpace(string(output))
	if filePath == "" {
		return "", fmt.Errorf("song command returned empty output")
	}

	// Take only the first line
	if idx := strings.IndexByte(filePath, '\n'); idx >= 0 {
		filePath = filePath[:idx]
	}
	filePath = strings.TrimSpace(filePath)

	// Resolve relative paths against music dir
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(s.MusicDir, filePath)
	}

	// Validate the file exists and is a supported audio format
	if err := validateAudioFile(filePath); err != nil {
		return "", fmt.Errorf("song command returned invalid file %q: %w", filePath, err)
	}

	return filePath, nil
}

type StreamerStats struct {
	Name           string
	Mount          string
	State          StreamerState
	CurrentSong    string
	StartTime      time.Time
	Duration       time.Duration
	PlaylistPos    int
	PlaylistLen    int
	Shuffle        bool
	MPDPort        string
	MPDPassword    string
	MusicDir       string
	Loop           bool
	InjectMetadata bool
	Visible        bool
	LastPlaylist   string
}

func (s *Streamer) GetStats() StreamerStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	mpdPort := ""
	mpdPassword := ""
	if s.MPDServer != nil {
		mpdPort = s.MPDServer.Port
		mpdPassword = s.MPDPassword
	}

	return StreamerStats{
		Name:           s.Name,
		Mount:          s.OutputMount,
		State:          s.State,
		CurrentSong:    s.CurrentFile,
		StartTime:      s.CurrentFileTime,
		Duration:       s.CurrentFileDuration,
		PlaylistPos:    s.CurrentPos,
		PlaylistLen:    len(s.Playlist),
		Shuffle:        s.Shuffle,
		MPDPort:        mpdPort,
		MPDPassword:    mpdPassword,
		MusicDir:       s.MusicDir,
		Loop:           s.Loop,
		InjectMetadata: s.InjectMetadata,
		Visible:        s.Visible,
		LastPlaylist:   s.LastPlaylist,
	}
}

func (sm *StreamerManager) StartStreamer(name, mount, musicDir string, loop bool, format string, bitrate int, injectMetadata bool, initialPlaylistPaths []string, mpdEnabled bool, mpdPort, mpdPassword string, visible bool, lastPlaylist string, songCommand string, songCommandTimeout int) (*Streamer, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if _, ok := sm.instances[mount]; ok {
		return nil, fmt.Errorf("streamer for mount %s already exists", mount)
	}

	ctx, cancel := context.WithCancel(context.Background())
	absMusicDir, _ := filepath.Abs(musicDir)

	initialPlaylist := make([]PlaylistSong, 0, len(initialPlaylistPaths))
	nextID := 1
	for _, path := range initialPlaylistPaths {
		initialPlaylist = append(initialPlaylist, PlaylistSong{Path: path, ID: nextID})
		nextID++
	}

	s := &Streamer{
		Name:               name,
		OutputMount:        mount,
		MusicDir:           absMusicDir,
		Format:             format,
		Bitrate:            bitrate,
		Playlist:           initialPlaylist,
		State:              StateStopped,
		Loop:               loop,
		InjectMetadata:     injectMetadata,
		Visible:            visible,
		MPDPassword:        mpdPassword,
		LastPlaylist:       lastPlaylist,
		SongCommand:        songCommand,
		SongCommandTimeout: songCommandTimeout,
		relay:              sm.relay,
		runtimeRegistry:    sm.runtimeRegistry,
		cancel:             cancel,
		titleCache:         make(map[string]string),
		NextID:             nextID, // Start NextID after initial playlist
		CurrentPlayingPos:  -1,
		CurrentPlayingID:   -1,
		PlaylistVersion:    1,
		idleCh:             make(chan string, 10),
		stateCh:            make(chan struct{}, 1),
	}

	if mpdEnabled && mpdPort != "" {
		logger.L.Debugf("AutoDJ %s: MPD enabled on port %s", name, mpdPort)
		// Check for port conflicts within our own instances
		for _, inst := range sm.instances {
			inst.mu.RLock()
			if inst.MPDServer != nil && inst.MPDServer.Port == mpdPort {
				inst.mu.RUnlock()
				logger.L.Warnf("AutoDJ %s: MPD port %s is already in use by %s", name, mpdPort, inst.Name)
				return nil, fmt.Errorf("MPD port %s is already in use by AutoDJ %s", mpdPort, inst.Name)
			}
			inst.mu.RUnlock()
		}

		s.MPDServer = NewMPDServer(mpdPort, mpdPassword, s)
		if err := s.MPDServer.Start(); err != nil {
			logger.L.Errorf("Failed to start MPD server for AutoDJ %s: %v", name, err)
		} else {
			logger.L.Infof("MPD Server for %s listening on port %s", name, mpdPort)
		}
	} else {
		logger.L.Debugf("AutoDJ %s: MPD not enabled or no port specified (enabled=%v, port=%s)", name, mpdEnabled, mpdPort)
	}

	sm.instances[mount] = s

	if lastPlaylist != "" {
		s.LoadPlaylist(lastPlaylist)
	} else {
		s.LoadPlaylist("")
	}

	go sm.runStreamerLoop(ctx, s)
	return s, nil
}

func (sm *StreamerManager) StopStreamer(mount string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if s, ok := sm.instances[mount]; ok {
		if s.MPDServer != nil {
			logger.L.Debugf("AutoDJ %s: Stopping MPD server", s.Name)
			s.MPDServer.Stop()
		}
		s.Stop()
		// We DON'T delete it from sm.instances anymore,
		// so it remains manageable via UI even when stopped.
	}
}

// DeleteStreamer stops the streamer and removes it from the manager entirely.
// Use this when the underlying AutoDJ config is being deleted (as opposed to
// StopStreamer which keeps the instance around so the UI can restart it).
func (sm *StreamerManager) DeleteStreamer(mount string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if s, ok := sm.instances[mount]; ok {
		if s.MPDServer != nil {
			logger.L.Debugf("AutoDJ %s: Stopping MPD server", s.Name)
			s.MPDServer.Stop()
		}
		s.Stop()
		delete(sm.instances, mount)
	}
}

func (sm *StreamerManager) GetStreamers() []*Streamer {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	res := make([]*Streamer, 0, len(sm.instances))
	for _, s := range sm.instances {
		res = append(res, s)
	}
	return res
}

func (sm *StreamerManager) GetStreamer(mount string) *Streamer {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.instances[mount]
}

func (s *Streamer) MovePlaylistItem(from, to int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if from < 0 || from >= len(s.Playlist) || to < 0 || to >= len(s.Playlist) {
		return
	}
	item := s.Playlist[from]
	// Remove
	s.Playlist = append(s.Playlist[:from], s.Playlist[from+1:]...)
	// Insert
	s.Playlist = append(s.Playlist[:to], append([]PlaylistSong{item}, s.Playlist[to:]...)...)
	s.PlaylistVersion++
	s.broadcastIdle("playlist")
}

func (sm *StreamerManager) runStreamerLoop(ctx context.Context, s *Streamer) {
	logger.L.Infof("Streamer %s starting for mount %s", s.Name, s.OutputMount)
	defer func() {
		s.mu.Lock()
		outputSession := s.outputSession
		s.outputSession = nil
		s.fileCancel = nil
		s.mu.Unlock()
		if outputSession != nil {
			outputSession.Stop()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
			if s.State != StatePlaying {
				s.mu.Lock()
				outputSession := s.outputSession
				s.outputSession = nil
				s.fileCancel = nil
				s.mu.Unlock()
				if outputSession != nil {
					outputSession.Stop()
				}
				select {
				case <-ctx.Done():
					return
				case <-s.stateCh:
					continue
				}
			}

			s.mu.Lock()
			if s.outputSession == nil {
				outputSession, err := NewAutoDJOutputSession(s)
				if err != nil {
					s.mu.Unlock()
					logger.L.Errorf("Streamer %s: failed to create output session: %v", s.Name, err)
					time.Sleep(1 * time.Second)
					continue
				}
				s.outputSession = outputSession
			}
			outputSession := s.outputSession
			s.mu.Unlock()

			if err := outputSession.Start(ctx); err != nil {
				logger.L.Errorf("Streamer %s: failed to start output session: %v", s.Name, err)
				time.Sleep(1 * time.Second)
				continue
			}

			s.mu.Lock()
			var filePath string
			var fileID int

			var filePos int
			// 1. Check Queue first
			if len(s.Queue) > 0 {
				filePath = s.Queue[0]
				s.Queue = s.Queue[1:]
				fileID = -1 // Queue items don't have an ID from the playlist
				filePos = -1
			} else if s.SongCommand != "" {
				// 2. External song command (unlock during exec to avoid blocking)
				s.mu.Unlock()
				if path, err := s.execSongCommand(); err == nil {
					filePath = path
					fileID = -1
					filePos = -1
				} else {
					logger.L.Warnf("Streamer %s: Song command error, falling back to playlist: %v", s.Name, err)
				}
				s.mu.Lock()
				// If command failed, try playlist as fallback
				if filePath == "" && len(s.Playlist) > 0 {
					if s.Shuffle {
						s.CurrentPos = rand.Intn(len(s.Playlist))
					} else {
						if s.CurrentPos >= len(s.Playlist) {
							if s.Loop {
								s.CurrentPos = 0
							} else {
								s.State = StateStopped
								s.mu.Unlock()
								continue
							}
						}
					}
					filePath = s.Playlist[s.CurrentPos].Path
					fileID = s.Playlist[s.CurrentPos].ID
					filePos = s.CurrentPos
					if !s.Shuffle {
						s.CurrentPos++
					}
				}
			} else if len(s.Playlist) > 0 {
				// 3. Normal playlist selection
				if s.Shuffle {
					s.CurrentPos = rand.Intn(len(s.Playlist))
				} else {
					if s.CurrentPos >= len(s.Playlist) {
						if s.Loop {
							s.CurrentPos = 0
						} else {
							s.State = StateStopped
							s.mu.Unlock()
							continue
						}
					}
				}
				filePath = s.Playlist[s.CurrentPos].Path
				fileID = s.Playlist[s.CurrentPos].ID
				filePos = s.CurrentPos
				if !s.Shuffle {
					s.CurrentPos++
				}
			}
			s.mu.Unlock()

			if filePath == "" {
				outputSession.SetSource(silencePCMSource{})
				time.Sleep(1 * time.Second)
				continue
			}

			if err := validateAudioFile(filePath); err != nil {
				logger.L.Warnf("Streamer %s: Skipping invalid file %s: %v", s.Name, filePath, err)
				outputSession.SetSource(silencePCMSource{})
				continue
			}

			// Create a per-file context for skipping
			fileCtx, fileCancel := context.WithCancel(ctx)
			s.mu.Lock()
			s.fileCancel = fileCancel
			s.mu.Unlock()

			err := sm.streamFile(fileCtx, s, filePath, filePos, fileID)
			if err != nil && fileCtx.Err() == nil {
				logger.L.Errorf("Streamer %s: Failed to stream %s: %v", s.Name, filePath, err)
				time.Sleep(1 * time.Second)
			}

			s.mu.Lock()
			s.fileCancel = nil
			fileCancel()
			s.mu.Unlock()
		}
	}
}

func validateAudioFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("file not accessible: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("path is a directory")
	}
	if info.Size() == 0 {
		return fmt.Errorf("file is empty")
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot open: %w", err)
	}
	defer f.Close()

	header := make([]byte, 4)
	if _, err := f.Read(header); err != nil {
		return fmt.Errorf("cannot read header: %w", err)
	}
	// MP3 with ID3v2 tags
	if header[0] == 'I' && header[1] == 'D' && header[2] == '3' {
		return nil
	}
	// Raw MP3 frame sync
	if header[0] == 0xFF && (header[1]&0xE0) == 0xE0 {
		return nil
	}
	// Ogg container (Vorbis / Opus / FLAC-in-Ogg)
	if string(header) == "OggS" {
		return nil
	}
	// Native FLAC
	if string(header) == "fLaC" {
		return nil
	}
	// WAV / RIFF
	if string(header) == "RIFF" {
		return nil
	}
	return fmt.Errorf("unrecognized audio format (header: %x)", header[:4])
}

func (sm *StreamerManager) streamFile(ctx context.Context, s *Streamer, path string, pos, id int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// Extract metadata
	songTitle := filepath.Base(path)
	var tagMeta tag.Metadata
	if m, err := tag.ReadFrom(f); err == nil {
		tagMeta = m
		if m.Artist() != "" && m.Title() != "" {
			songTitle = fmt.Sprintf("%s - %s", m.Artist(), m.Title())
		} else if m.Title() != "" {
			songTitle = m.Title()
		}
	}
	// Seek back to start after reading tags
	f.Seek(0, 0)

	decoder, err := OpenDecoder(f)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.CurrentFile = songTitle
	s.CurrentArtist = ""
	s.CurrentTitle = songTitle
	s.CurrentAlbum = ""
	s.CurrentFilePath = path
	s.CurrentPlayingPos = pos
	s.CurrentPlayingID = id
	if tagMeta != nil {
		s.CurrentArtist = tagMeta.Artist()
		s.CurrentTitle = tagMeta.Title()
		s.CurrentAlbum = tagMeta.Album()
		if s.CurrentTitle == "" {
			s.CurrentTitle = songTitle
		}
	}
	s.CurrentSampleRate = decoder.SampleRate()
	s.CurrentChannels = 2 // decoders always emit 2 channels via OpenDecoder
	s.CurrentFileTime = time.Now()
	s.CurrentFileDuration = 0 // PCM length isn't known up-front for non-MP3 inputs
	s.mu.Unlock()

	// Update stream metadata under the output stream's mutex so concurrent
	// Snapshot / listener reads see a coherent set of fields.
	output := sm.relay.GetOrCreateStream(s.OutputMount)
	if s.runtimeRegistry != nil {
		rt := s.runtimeRegistry.GetOrCreate(s.OutputMount)
		rt.Stream = output
		s.runtimeRegistry.AttachSource(s.OutputMount, SourceAutoDJ, "default")
	}
	output.mu.Lock()
	output.Name = s.Name
	output.Visible = s.Visible
	output.Bitrate = fmt.Sprintf("%d", s.Bitrate)
	if s.Format == "opus" {
		output.ContentType = "audio/ogg"
	} else {
		output.ContentType = "audio/mpeg"
	}
	output.mu.Unlock()

	// Apply the streamer's volume setting (0..1) to the PCM stream before
	// it reaches the encoder. When Volume is 1.0 the wrapper is a no-op.
	var pcm io.Reader = decoder
	if gain := s.GetVolume(); gain < 1.0 {
		pcm = newGainReader(pcm, gain)
	}

	if s.Format == "opus" {
		// Opus encoder is locked at 48 kHz; resample if the file is at a
		// different rate so playback isn't sped up / slowed down.
		if decoder.SampleRate() != 48000 {
			pcm = NewLinearResampler(pcm, decoder.SampleRate(), 48000)
		}
	} else {
		// MP3 output session is fixed at 44.1 kHz, so normalize other
		// decoder rates before handing frames to the persistent encoder.
		if decoder.SampleRate() != 44100 {
			pcm = NewLinearResampler(pcm, decoder.SampleRate(), 44100)
		}
	}

	s.mu.RLock()
	outputSession := s.outputSession
	metadataSong := s.CurrentFile
	metadataMount := s.OutputMount
	injectMetadata := s.InjectMetadata
	s.mu.RUnlock()
	createdSession := false
	if outputSession == nil {
		s.mu.Lock()
		if s.outputSession == nil {
			created, err := NewAutoDJOutputSession(s)
			if err != nil {
				s.mu.Unlock()
				return err
			}
			s.outputSession = created
			createdSession = true
		}
		outputSession = s.outputSession
		s.mu.Unlock()
		if err := outputSession.Start(ctx); err != nil {
			return err
		}
	}
	if createdSession {
		defer func() {
			outputSession.Stop()
			s.mu.Lock()
			if s.outputSession == outputSession {
				s.outputSession = nil
			}
			s.mu.Unlock()
		}()
	}

	source := newReaderPCMFrameSource(pcm)
	outputSession.SetSourceWithActivation(source, func() {
		if injectMetadata && sm.relay != nil {
			sm.relay.UpdateMetadata(metadataMount, metadataSong)
		}
	})

	select {
	case <-ctx.Done():
		outputSession.SetSource(silencePCMSource{})
		return ctx.Err()
	case <-source.Done():
		return nil
	}
}

// gainReader multiplies every S16LE stereo sample it passes through by a
// fixed gain in [0, 1]. Used to apply the AutoDJ's Volume setting without
// touching the decoder / encoder interfaces.
type gainReader struct {
	src  io.Reader
	gain float64
}

func newGainReader(src io.Reader, gain float64) *gainReader {
	if gain < 0 {
		gain = 0
	}
	if gain > 1 {
		gain = 1
	}
	return &gainReader{src: src, gain: gain}
}

func (g *gainReader) Read(p []byte) (int, error) {
	n, err := g.src.Read(p)
	if n == 0 {
		return n, err
	}
	// Process whole 2-byte samples only; defer odd trailing byte to the
	// next read (the source always feeds us whole stereo frames so this
	// rarely matters).
	end := n - (n % 2)
	for i := 0; i+1 < end; i += 2 {
		s := int32(int16(p[i]) | int16(p[i+1])<<8)
		s = int32(float64(s) * g.gain)
		if s > 32767 {
			s = 32767
		} else if s < -32768 {
			s = -32768
		}
		p[i] = byte(s)
		p[i+1] = byte(s >> 8)
	}
	return n, err
}
