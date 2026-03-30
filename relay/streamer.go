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
	"github.com/hajimehoshi/go-mp3"
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
	manualStop         bool
	Loop               bool
	Shuffle            bool
	InjectMetadata     bool
	Visible            bool
	MPDPassword        string
	LastPlaylist       string
	SongCommand        string
	SongCommandTimeout int

	relay  *Relay
	cancel context.CancelFunc
	mu     sync.RWMutex

	fileCancel   context.CancelFunc
	outputSession *AutoDJOutputSession
	titleCache   map[string]string
	titleFetchWg sync.WaitGroup

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
	idleCh              chan string
	stateCh             chan struct{}
}

type StreamerManager struct {
	instances map[string]*Streamer // key is OutputMount
	mu        sync.RWMutex
	relay     *Relay
	config    *config.Config

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
	last := stream.LastDataAt()
	return !last.IsZero() && time.Since(last) <= 5*time.Second
}

func (sm *StreamerManager) runDeadSongCommandRecovery(ctx context.Context, s *Streamer) {
	defer sm.clearDeadRecovery(s.OutputMount)

	bo := &backoff{base: 1 * time.Second, max: 60 * time.Second}
	select {
	case <-ctx.Done():
		return
	default:
	}

	for {
		if !sm.streamerEligibleForDeadRecovery(s) || sm.mountHasFreshData(s.OutputMount) {
			return
		}
		if sm.recoveryExecSongCommand == nil {
			return
		}

		path, err := sm.recoveryExecSongCommand(s)
		if err == nil && sm.recoveryActivatePath != nil {
			err = sm.recoveryActivatePath(ctx, sm, s, path)
		}
		if err == nil && sm.mountHasFreshData(s.OutputMount) {
			return
		}

		delay := bo.next()
		select {
		case <-ctx.Done():
			return
		case <-sm.recoveryAfter(delay):
		}
	}
}

func (s *Streamer) Play() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.State = StatePlaying
	s.manualStop = false
	s.signalStateChange()
}

func (s *Streamer) Next() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fileCancel != nil {
		s.fileCancel()
	}
}

func (s *Streamer) TogglePlay() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.State == StatePlaying {
		s.State = StateStopped
		s.manualStop = true
	} else {
		s.State = StatePlaying
		s.manualStop = false
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
	s.State = StateStopped
	s.manualStop = true
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

	if cancel, ok := sm.deadRecovery[mount]; ok {
		cancel()
		delete(sm.deadRecovery, mount)
	}

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

func (sm *StreamerManager) RemoveStreamer(mount string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if cancel, ok := sm.deadRecovery[mount]; ok {
		cancel()
		delete(sm.deadRecovery, mount)
	}

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
			s.mu.RLock()
			state := s.State
			s.mu.RUnlock()
			if state != StatePlaying {
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

			filePath, filePos, fileID, ok := s.nextTrackCandidate()
			if !ok {
				outputSession.Stop()
				s.mu.Lock()
				s.outputSession = nil
				s.fileCancel = nil
				s.State = StateStopped
				s.manualStop = false
				s.mu.Unlock()
				continue
			}

			if filePath == "" {
				time.Sleep(1 * time.Second)
				continue
			}

			if err := validateAudioFile(filePath); err != nil {
				logger.L.Warnf("Streamer %s: Skipping invalid file %s: %v", s.Name, filePath, err)
				continue
			}

			outputSession.SetSource(silencePCMSource{})

			fileCtx, fileCancel := context.WithCancel(ctx)
			s.mu.Lock()
			s.fileCancel = fileCancel
			s.mu.Unlock()

			source, err := sm.activateTrackSource(fileCtx, s, filePath, filePos, fileID)
			if err != nil {
				fileCancel()
				s.mu.Lock()
				s.fileCancel = nil
				s.mu.Unlock()
				logger.L.Errorf("Streamer %s: Failed to activate %s: %v", s.Name, filePath, err)
				time.Sleep(1 * time.Second)
				continue
			}

			select {
			case <-ctx.Done():
				fileCancel()
				s.mu.Lock()
				s.fileCancel = nil
				s.mu.Unlock()
				return
			case <-s.stateCh:
				s.mu.RLock()
				stillPlaying := s.State == StatePlaying
				s.mu.RUnlock()
				if !stillPlaying {
					fileCancel()
					outputSession.SetSource(silencePCMSource{})
				}
			case <-source.Done():
				outputSession.SetSource(silencePCMSource{})
			}

			fileCancel()
			s.mu.Lock()
			s.fileCancel = nil
			s.mu.Unlock()
		}
	}
}

func (s *Streamer) nextTrackCandidate() (string, int, int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var filePath string
	var fileID int
	var filePos int

	if len(s.Queue) > 0 {
		filePath = s.Queue[0]
		s.Queue = s.Queue[1:]
		fileID = -1
		filePos = -1
		return filePath, filePos, fileID, true
	}

	if s.SongCommand != "" {
		s.mu.Unlock()
		path, err := s.execSongCommand()
		s.mu.Lock()
		if err == nil {
			return path, -1, -1, true
		}
		logger.L.Warnf("Streamer %s: Song command error, falling back to playlist: %v", s.Name, err)
	}

	if len(s.Playlist) == 0 {
		return "", 0, 0, false
	}

	if s.Shuffle {
		s.CurrentPos = rand.Intn(len(s.Playlist))
	} else if s.CurrentPos >= len(s.Playlist) {
		if s.Loop {
			s.CurrentPos = 0
		} else {
			return "", 0, 0, false
		}
	}

	filePath = s.Playlist[s.CurrentPos].Path
	fileID = s.Playlist[s.CurrentPos].ID
	filePos = s.CurrentPos
	if !s.Shuffle {
		s.CurrentPos++
	}

	return filePath, filePos, fileID, true
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
	if header[0] == 'I' && header[1] == 'D' && header[2] == '3' {
		return nil
	}
	if header[0] == 0xFF && (header[1]&0xE0) == 0xE0 {
		return nil
	}
	return fmt.Errorf("unrecognized audio format (header: %x)", header[:4])
}

type trackPCMSource struct {
	ctx    context.Context
	reader *pcmFrameReader
	closer io.Closer
	done   chan struct{}
	once   sync.Once
}

func newTrackPCMSource(ctx context.Context, file *os.File, outputFormat string) (*trackPCMSource, int, time.Duration, error) {
	decoder, err := mp3.NewDecoder(file)
	if err != nil {
		file.Close()
		return nil, 0, 0, err
	}

	sampleRate := decoder.SampleRate()
	frameSampleRate := 44100
	samplesPerFrame := 1152
	if outputFormat == "opus" {
		frameSampleRate = 48000
		samplesPerFrame = 960
	}

	duration := time.Duration(decoder.Length()) * time.Second / time.Duration(decoder.SampleRate()*4)
	return &trackPCMSource{
		ctx:    ctx,
		reader: newPCMFrameReader(decoder, sampleRate, frameSampleRate, 2, samplesPerFrame),
		closer: file,
		done:   make(chan struct{}),
	}, sampleRate, duration, nil
}

func (s *trackPCMSource) Done() <-chan struct{} { return s.done }

func (s *trackPCMSource) Close() error {
	s.once.Do(func() {
		if s.closer != nil {
			s.closer.Close()
		}
		close(s.done)
	})
	return nil
}

func (s *trackPCMSource) ReadFrame(dst []int16) (int, error) {
	select {
	case <-s.ctx.Done():
		s.Close()
		return 0, s.ctx.Err()
	default:
	}

	n, err := s.reader.ReadFrame(dst)
	if err != nil {
		s.Close()
	}
	return n, err
}

func (sm *StreamerManager) activateTrackSource(ctx context.Context, s *Streamer, path string, pos, id int) (*trackPCMSource, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

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

	source, sampleRate, duration, err := newTrackPCMSource(ctx, f, s.Format)
	if err != nil {
		return nil, err
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
	s.CurrentSampleRate = sampleRate
	s.CurrentChannels = 2 // go-mp3 always outputs 2 channels
	s.CurrentFileTime = time.Now()
	s.CurrentFileDuration = duration
	outputSession := s.outputSession
	s.mu.Unlock()

	if s.InjectMetadata {
		sm.relay.UpdateMetadata(s.OutputMount, s.CurrentFile)
	}

	if outputSession == nil {
		source.Close()
		return nil, fmt.Errorf("output session not initialized")
	}

	outputSession.SetSource(source)
	return source, nil
}

func (sm *StreamerManager) activateRecoveredSongCommandPath(ctx context.Context, s *Streamer, path string) error {
	s.mu.RLock()
	restartStopped := s.State == StateStopped && !s.manualStop
	s.mu.RUnlock()
	if restartStopped {
		return sm.resumeRecoveredStoppedMount(s, path)
	}
	if err := sm.ensureOutputSession(ctx, s); err != nil {
		return err
	}
	_, err := sm.activateTrackSource(ctx, s, path, -1, -1)
	return err
}

func (sm *StreamerManager) resumeRecoveredStoppedMount(s *Streamer, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.manualStop {
		return fmt.Errorf("streamer was manually stopped")
	}

	s.Queue = append([]string{path}, s.Queue...)
	s.State = StatePlaying
	s.manualStop = false
	s.signalStateChange()
	return nil
}

func (sm *StreamerManager) ensureOutputSession(ctx context.Context, s *Streamer) error {
	if !sm.streamerEligibleForDeadRecovery(s) {
		return fmt.Errorf("streamer is not in a recoverable playing state")
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

	return outputSession.Start(ctx)
}
