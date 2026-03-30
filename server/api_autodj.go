package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/logger"
	"github.com/DatanoiseTV/tinyice/relay"
)

func (s *Server) apiGetAutoDJ(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	type autoDJInfo struct {
		Name               string                  `json:"name"`
		Mount              string                  `json:"mount"`
		State              int                     `json:"state"`
		CurrentSong        string                  `json:"current_song"`
		StartTime          int64                   `json:"start_time"`
		Duration           float64                 `json:"duration"`
		PlaylistPos        int                     `json:"playlist_pos"`
		PlaylistLen        int                     `json:"playlist_len"`
		Shuffle            bool                    `json:"shuffle"`
		Loop               bool                    `json:"loop"`
		InjectMetadata     bool                    `json:"inject_metadata"`
		Visible            bool                    `json:"visible"`
		MusicDir           string                  `json:"music_dir"`
		Format             string                  `json:"format"`
		Bitrate            int                     `json:"bitrate"`
		Enabled            bool                    `json:"enabled"`
		MPDEnabled         bool                    `json:"mpd_enabled"`
		MPDPort            string                  `json:"mpd_port"`
		LastPlaylist       string                  `json:"last_playlist"`
		SongCommand        string                  `json:"song_command"`
		SongCommandTimeout int                     `json:"song_command_timeout"`
		Queue              []relay.PlaylistItem    `json:"queue"`
		Status             string                  `json:"status"`
		StatusClass        string                  `json:"status_class"`
		StatusReason       string                  `json:"status_reason"`
		LastError          string                  `json:"last_error,omitempty"`
		StatusUpdatedAt    int64                   `json:"status_updated_at"`
		LastRecoveryAt     int64                   `json:"last_recovery_at,omitempty"`
		LastRecoveryResult string                  `json:"last_recovery_result,omitempty"`
		History            []relay.DiagnosticEntry `json:"history"`
	}

	var result []autoDJInfo
	streamers := s.StreamerM.GetStreamers()
	streamerMap := make(map[string]*relay.Streamer)
	for _, st := range streamers {
		streamerMap[st.OutputMount] = st
	}

	for _, adj := range s.Config.AutoDJs {
		if !s.hasAccess(user, adj.Mount) {
			continue
		}
		info := autoDJInfo{
			Name:               adj.Name,
			Mount:              adj.Mount,
			Format:             adj.Format,
			Bitrate:            adj.Bitrate,
			Enabled:            adj.Enabled,
			MusicDir:           adj.MusicDir,
			MPDEnabled:         adj.MPDEnabled,
			MPDPort:            adj.MPDPort,
			LastPlaylist:       adj.LastPlaylist,
			SongCommand:        adj.SongCommand,
			SongCommandTimeout: adj.SongCommandTimeout,
			Loop:               adj.Loop,
			InjectMetadata:     adj.InjectMetadata,
			Visible:            adj.Visible,
		}
		diag := diagnosticInfoFor(s.Relay, adj.Mount)
		info.Status = diag.Status
		info.StatusClass = diag.StatusClass
		info.StatusReason = diag.StatusReason
		info.LastError = diag.LastError
		info.StatusUpdatedAt = diag.StatusUpdatedAt
		info.LastRecoveryAt = diag.LastRecoveryAt
		info.LastRecoveryResult = diag.LastRecoveryResult
		info.History = diag.History
		if st, ok := streamerMap[adj.Mount]; ok {
			stats := st.GetStats()
			info.State = int(stats.State)
			info.CurrentSong = stats.CurrentSong
			info.StartTime = stats.StartTime.Unix()
			info.Duration = stats.Duration.Seconds()
			info.PlaylistPos = stats.PlaylistPos
			info.PlaylistLen = stats.PlaylistLen
			info.Shuffle = stats.Shuffle
			info.Loop = stats.Loop
			info.InjectMetadata = stats.InjectMetadata
			info.Visible = stats.Visible
			info.Queue = st.GetQueueInfo()
		}
		if info.Queue == nil {
			info.Queue = []relay.PlaylistItem{}
		}
		if info.History == nil {
			info.History = []relay.DiagnosticEntry{}
		}
		result = append(result, info)
	}
	if result == nil {
		result = []autoDJInfo{}
	}
	jsonResponse(w, result)
}

func (s *Server) apiCreateAutoDJ(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	var body struct {
		Name               string `json:"name"`
		Mount              string `json:"mount"`
		MusicDir           string `json:"music_dir"`
		Format             string `json:"format"`
		Bitrate            int    `json:"bitrate"`
		Loop               bool   `json:"loop"`
		InjectMetadata     bool   `json:"inject_metadata"`
		MPDEnabled         bool   `json:"mpd_enabled"`
		MPDPort            string `json:"mpd_port"`
		MPDPassword        string `json:"mpd_password"`
		Visible            bool   `json:"visible"`
		SongCommand        string `json:"song_command"`
		SongCommandTimeout int    `json:"song_command_timeout"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if body.Name == "" || body.Mount == "" || body.MusicDir == "" {
		jsonError(w, "Name, mount, and music_dir are required", http.StatusBadRequest)
		return
	}
	if body.Mount[0] != '/' {
		body.Mount = "/" + body.Mount
	}
	if _, ok := s.requireMountAccess(w, r, body.Mount); !ok {
		return
	}
	if body.Format == "" {
		body.Format = "mp3"
	}
	if body.Bitrate == 0 {
		body.Bitrate = 128
	}

	absMusicDir, _ := filepath.Abs(body.MusicDir)
	adj := &config.AutoDJConfig{
		Name:               body.Name,
		Mount:              body.Mount,
		MusicDir:           absMusicDir,
		Format:             body.Format,
		Bitrate:            body.Bitrate,
		Enabled:            true,
		Loop:               body.Loop,
		InjectMetadata:     body.InjectMetadata,
		MPDEnabled:         body.MPDEnabled,
		MPDPort:            body.MPDPort,
		MPDPassword:        body.MPDPassword,
		Visible:            body.Visible,
		SongCommand:        body.SongCommand,
		SongCommandTimeout: body.SongCommandTimeout,
	}

	streamer, err := s.StreamerM.StartStreamer(adj.Name, adj.Mount, adj.MusicDir, adj.Loop, adj.Format, adj.Bitrate, adj.InjectMetadata, nil, adj.MPDEnabled, adj.MPDPort, adj.MPDPassword, adj.Visible, "", adj.SongCommand, adj.SongCommandTimeout)
	if err != nil {
		jsonError(w, fmt.Sprintf("Failed to start AutoDJ: %v", err), http.StatusInternalServerError)
		return
	}
	if err := s.mutateConfig(func(cfg *config.Config) error {
		cfg.AutoDJs = append(cfg.AutoDJs, adj)
		return nil
	}); err != nil {
		s.StreamerM.RemoveStreamer(adj.Mount)
		jsonError(w, "Failed to save config", http.StatusInternalServerError)
		return
	}
	if adj.InjectMetadata {
		if st, ok := s.Relay.GetStream(adj.Mount); ok {
			st.SetVisible(adj.Visible)
		}
	}
	streamer.ScanMusicDir()
	streamer.Play()
	jsonResponse(w, map[string]string{"status": "created", "mount": adj.Mount})
	s.Audit(r, "autodj_created", "autodj", body.Mount, body.Name)
}

func (s *Server) apiDeleteAutoDJ(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	mount := r.URL.Query().Get("mount")
	if mount == "" {
		jsonError(w, "Mount is required", http.StatusBadRequest)
		return
	}
	if _, ok := s.requireMountAccess(w, r, mount); !ok {
		return
	}

	newADJs := []*config.AutoDJConfig{}
	found := false
	for _, adj := range s.Config.AutoDJs {
		if adj.Mount != mount {
			newADJs = append(newADJs, adj)
		} else {
			s.StreamerM.RemoveStreamer(mount)
			found = true
		}
	}
	if !found {
		jsonError(w, "AutoDJ not found", http.StatusNotFound)
		return
	}
	if err := s.mutateConfig(func(cfg *config.Config) error {
		cfg.AutoDJs = newADJs
		return nil
	}); err != nil {
		jsonError(w, "Failed to save config", http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "deleted"})
	s.Audit(r, "autodj_deleted", "autodj", mount, "")
}

func (s *Server) apiAutoDJPlay(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	mount := r.URL.Query().Get("mount")
	if _, ok := s.requireMountAccess(w, r, mount); !ok {
		return
	}
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		jsonError(w, "Streamer not found", http.StatusNotFound)
		return
	}
	streamer.Play()
	jsonResponse(w, map[string]string{"status": "playing"})
}

func (s *Server) apiAutoDJPause(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	mount := r.URL.Query().Get("mount")
	if _, ok := s.requireMountAccess(w, r, mount); !ok {
		return
	}
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		jsonError(w, "Streamer not found", http.StatusNotFound)
		return
	}
	streamer.Stop()
	jsonResponse(w, map[string]string{"status": "paused"})
}

func (s *Server) apiAutoDJNext(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	mount := r.URL.Query().Get("mount")
	if _, ok := s.requireMountAccess(w, r, mount); !ok {
		return
	}
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		jsonError(w, "Streamer not found", http.StatusNotFound)
		return
	}
	streamer.Next()
	jsonResponse(w, map[string]string{"status": "skipped"})
}

func (s *Server) apiAutoDJShuffle(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	mount := r.URL.Query().Get("mount")
	if _, ok := s.requireMountAccess(w, r, mount); !ok {
		return
	}
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		jsonError(w, "Streamer not found", http.StatusNotFound)
		return
	}
	streamer.ToggleShuffle()
	stats := streamer.GetStats()
	jsonResponse(w, map[string]interface{}{"status": "ok", "shuffle": stats.Shuffle})
}

func (s *Server) apiAutoDJLoop(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	mount := r.URL.Query().Get("mount")
	if _, ok := s.requireMountAccess(w, r, mount); !ok {
		return
	}
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		jsonError(w, "Streamer not found", http.StatusNotFound)
		return
	}
	streamer.ToggleLoop()
	stats := streamer.GetStats()
	_ = s.mutateConfig(func(cfg *config.Config) error {
		for _, adj := range cfg.AutoDJs {
			if adj.Mount == mount {
				adj.Loop = stats.Loop
				break
			}
		}
		return nil
	})
	jsonResponse(w, map[string]interface{}{"status": "ok", "loop": stats.Loop})
}

func (s *Server) apiGetPlaylist(w http.ResponseWriter, r *http.Request) {
	mount := r.URL.Query().Get("mount")
	if _, ok := s.requireMountAccess(w, r, mount); !ok {
		return
	}
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		jsonError(w, "Streamer not found", http.StatusNotFound)
		return
	}
	jsonResponse(w, streamer.GetPlaylistInfo())
}

func (s *Server) apiAddToPlaylist(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	var body struct {
		Mount string   `json:"mount"`
		Files []string `json:"files"`
		Path  string   `json:"path"`
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if body.Path != "" {
		body.Files = append(body.Files, body.Path)
	}
	if len(body.Paths) > 0 {
		body.Files = append(body.Files, body.Paths...)
	}

	mount := body.Mount
	if mount == "" {
		mount = r.URL.Query().Get("mount")
	}
	if _, ok := s.requireMountAccess(w, r, mount); !ok {
		return
	}

	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		jsonError(w, "Streamer not found", http.StatusNotFound)
		return
	}

	musicDir := streamer.GetMusicDir()
	for _, file := range body.Files {
		if !filepath.IsAbs(file) {
			file = filepath.Join(musicDir, file)
		}
		fullPath, err := s.validatePathInMusicDir(musicDir, file)
		if err != nil {
			logger.L.Warnw("Security: Blocked playlist addition", "path", file, "mount", mount, "error", err)
			continue
		}
		info, err := os.Stat(fullPath)
		if err != nil {
			continue
		}
		if info.IsDir() {
			filepath.Walk(fullPath, func(p string, i os.FileInfo, e error) error {
				if e != nil {
					return nil
				}
				ext := strings.ToLower(filepath.Ext(p))
				if !i.IsDir() && (ext == ".mp3" || ext == ".ogg" || ext == ".opus" || ext == ".flac" || ext == ".wav") {
					streamer.AddToPlaylist(p)
				}
				return nil
			})
		} else {
			streamer.AddToPlaylist(fullPath)
		}
	}

	playlistCopy := streamer.GetPlaylist()
	lastPl := streamer.GetStats().LastPlaylist
	if lastPl == "" {
		lastPl = streamer.Name + ".pls"
		streamer.SetLastPlaylist(lastPl)
	}
	streamer.SavePlaylist()
	_ = s.mutateConfig(func(cfg *config.Config) error {
		for _, adj := range cfg.AutoDJs {
			if adj.Mount == mount {
				adj.Playlist = playlistCopy
				adj.LastPlaylist = lastPl
				break
			}
		}
		return nil
	})
	jsonResponse(w, map[string]string{"status": "ok"})
}

func (s *Server) apiRemoveFromPlaylist(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	mount := r.URL.Query().Get("mount")
	var body struct {
		Mount string `json:"mount"`
		ID    int    `json:"id"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if mount == "" {
		mount = body.Mount
	}
	idx := body.ID
	if idx == 0 {
		fmt.Sscanf(r.URL.Query().Get("id"), "%d", &idx)
	}
	if _, ok := s.requireMountAccess(w, r, mount); !ok {
		return
	}

	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		jsonError(w, "Streamer not found", http.StatusNotFound)
		return
	}
	streamer.RemoveFromPlaylist(idx)

	playlistCopy := streamer.GetPlaylist()
	_ = s.mutateConfig(func(cfg *config.Config) error {
		for _, adj := range cfg.AutoDJs {
			if adj.Mount == mount {
				adj.Playlist = playlistCopy
				break
			}
		}
		return nil
	})
	jsonResponse(w, map[string]string{"status": "ok"})
}

func (s *Server) apiClearPlaylist(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	var body struct {
		Mount string `json:"mount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		mount := r.URL.Query().Get("mount")
		if mount == "" {
			jsonError(w, "Mount is required", http.StatusBadRequest)
			return
		}
		body.Mount = mount
	}
	if _, ok := s.requireMountAccess(w, r, body.Mount); !ok {
		return
	}

	streamer := s.StreamerM.GetStreamer(body.Mount)
	if streamer == nil {
		jsonError(w, "Streamer not found", http.StatusNotFound)
		return
	}
	streamer.ClearPlaylist()

	_ = s.mutateConfig(func(cfg *config.Config) error {
		for _, adj := range cfg.AutoDJs {
			if adj.Mount == body.Mount {
				adj.Playlist = []string{}
				break
			}
		}
		return nil
	})
	jsonResponse(w, map[string]string{"status": "ok"})
}

func (s *Server) apiReorderPlaylist(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	var body struct {
		Mount string `json:"mount"`
		From  int    `json:"from"`
		To    int    `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if _, ok := s.requireMountAccess(w, r, body.Mount); !ok {
		return
	}

	streamer := s.StreamerM.GetStreamer(body.Mount)
	if streamer == nil {
		jsonError(w, "Streamer not found", http.StatusNotFound)
		return
	}
	streamer.MovePlaylistItem(body.From, body.To)

	playlistCopy := streamer.GetPlaylist()
	_ = s.mutateConfig(func(cfg *config.Config) error {
		for _, adj := range cfg.AutoDJs {
			if adj.Mount == body.Mount {
				adj.Playlist = playlistCopy
				break
			}
		}
		return nil
	})
	jsonResponse(w, map[string]string{"status": "ok"})
}

func (s *Server) apiGetQueue(w http.ResponseWriter, r *http.Request) {
	mount := r.URL.Query().Get("mount")
	if _, ok := s.requireMountAccess(w, r, mount); !ok {
		return
	}
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		jsonError(w, "Streamer not found", http.StatusNotFound)
		return
	}
	jsonResponse(w, streamer.GetQueueInfo())
}

func (s *Server) apiAddToQueue(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	var body struct {
		Mount string `json:"mount"`
		Path  string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if _, ok := s.requireMountAccess(w, r, body.Mount); !ok {
		return
	}

	streamer := s.StreamerM.GetStreamer(body.Mount)
	if streamer == nil {
		jsonError(w, "Streamer not found", http.StatusNotFound)
		return
	}

	musicDir := streamer.GetMusicDir()
	fullPath, err := s.validatePathInMusicDir(musicDir, body.Path)
	if err != nil {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	streamer.PushToQueue(fullPath)
	jsonResponse(w, map[string]string{"status": "ok"})
}

func (s *Server) apiGetFiles(w http.ResponseWriter, r *http.Request) {
	mount := r.URL.Query().Get("mount")
	if _, ok := s.requireMountAccess(w, r, mount); !ok {
		return
	}
	subDir := r.URL.Query().Get("path")
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		jsonError(w, "Streamer not found", http.StatusNotFound)
		return
	}

	musicDir := streamer.GetMusicDir()
	fullPath, err := s.validatePathInMusicDir(musicDir, filepath.Join(musicDir, subDir))
	if err != nil {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type fileEntry struct {
		Name    string `json:"name"`
		Title   string `json:"title"`
		IsDir   bool   `json:"is_dir"`
		Path    string `json:"path"`
		AbsPath string `json:"abs_path"`
		IsPLS   bool   `json:"is_pls"`
	}
	var res []fileEntry

	if subDir == "" {
		if plsEntries, err := os.ReadDir("playlists"); err == nil {
			for _, f := range plsEntries {
				if !f.IsDir() && strings.HasSuffix(f.Name(), ".pls") {
					abs, _ := filepath.Abs(filepath.Join("playlists", f.Name()))
					res = append(res, fileEntry{
						Name:    f.Name(),
						Title:   "Playlist: " + f.Name(),
						IsDir:   false,
						Path:    f.Name(),
						AbsPath: abs,
						IsPLS:   true,
					})
				}
			}
		}
	}

	supportedExts := map[string]bool{".mp3": true, ".ogg": true, ".opus": true, ".flac": true, ".wav": true}
	for _, f := range entries {
		ext := strings.ToLower(filepath.Ext(f.Name()))
		if f.IsDir() || supportedExts[ext] {
			title := f.Name()
			full := filepath.Join(fullPath, f.Name())
			if !f.IsDir() {
				title = streamer.GetSongTitle(full)
			}
			abs, _ := filepath.Abs(full)
			res = append(res, fileEntry{
				Name:    f.Name(),
				Title:   title,
				IsDir:   f.IsDir(),
				Path:    filepath.Join(subDir, f.Name()),
				AbsPath: abs,
				IsPLS:   false,
			})
		}
	}
	if res == nil {
		res = []fileEntry{}
	}
	jsonResponse(w, res)
}
