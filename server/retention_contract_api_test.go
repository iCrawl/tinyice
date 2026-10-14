package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/relay"
)

func TestMountCreatePersistsFlagsAndPartialCapacityUpdates(t *testing.T) {
	s, sid := hardeningServer(t, config.RoleSuperAdmin)
	apply := func(body string, create bool) {
		t.Helper()
		request := withSession(httptest.NewRequest(http.MethodPost, "/api/streams", strings.NewReader(body)), sid)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		if create {
			s.apiCreateStream(response, request)
		} else {
			s.apiUpdateStream(response, request)
		}
		if response.Code != 200 {
			t.Fatalf("mount mutation %d %s", response.Code, response.Body.String())
		}
	}
	apply(`{"mount":"/contract","password":"secret","enabled":false,"visible":true,"max_listeners":7,"burst_size":4096}`, true)
	apply(`{"mount":"/contract","max_listeners":9}`, false)
	if !s.Config.DisabledMounts["/contract"] || !s.Config.VisibleMounts["/contract"] || mountMaxListeners(s.Config, "/contract") != 9 || mountBurstSize(s.Config, "/contract") != 4096 {
		t.Fatal("create flags or omitted fields lost")
	}
	reloaded, err := config.LoadConfig(s.Config.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.AdvancedMounts["/contract"].MaxListeners != 9 || reloaded.AdvancedMounts["/contract"].BurstSize != 4096 || !reloaded.DisabledMounts["/contract"] || !reloaded.VisibleMounts["/contract"] || reloaded.MaxListeners != 0 {
		t.Fatal("capacity, flags, or explicit unlimited ceiling did not survive reload")
	}
	s.Config.MaxListeners = 4
	if s.effectiveMountListenerLimit("/contract") != 4 {
		t.Fatal("mount cap ignored positive configured ceiling")
	}
	s.Config.MaxListeners = 0
	if s.effectiveMountListenerLimit("/contract") != 9 {
		t.Fatal("zero configured ceiling was bounded")
	}
	apply(`{"mount":"/contract","max_listeners":0,"burst_size":0}`, false)
	if mountMaxListeners(s.Config, "/contract") != 0 || effectiveMountBurstSize(s.Config, "/contract") != 512*1024 {
		t.Fatal("explicit zero did not restore unlimited cap/default burst")
	}
}

func TestAutoDJAPIIncludesRetainedDiagnosticsWithoutChangingNumericState(t *testing.T) {
	s, sid := hardeningServer(t, config.RoleSuperAdmin)
	s.StreamerM = relay.NewStreamerManager(s.Relay, s.Config)
	s.Config.AutoDJs = []*config.AutoDJConfig{{Name: "diagnostic", Mount: "/diagnostic", Format: "mp3", Enabled: true}}
	s.Relay.Diagnostics.Record(relay.DiagnosticUpdate{Mount: "/diagnostic", Status: relay.DiagnosticStatusDead, Class: relay.DiagnosticClassHealthDead, Reason: "real producer stalled", Error: "selection failed", Actor: relay.DiagnosticActorHealthMonitor, Timestamp: time.Now()})
	response := httptest.NewRecorder()
	s.apiGetAutoDJ(response, withSession(httptest.NewRequest(http.MethodGet, "/api/autodj", nil), sid))
	var entries []map[string]interface{}
	if err := json.Unmarshal(response.Body.Bytes(), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0]["state"] != float64(0) || entries[0]["status"] != "dead" || entries[0]["status_class"] != "health_dead" || entries[0]["last_error"] != "selection failed" || entries[0]["recovery"] == nil || entries[0]["history"] == nil {
		t.Fatalf("AutoDJ diagnostics missing or numeric state changed: %#v", entries)
	}
}

func TestCombinedStreamTicksCarryAtomicPlaybackForHiddenMountsAndClears(t *testing.T) {
	s := newListenerAPITestServer(t)
	out := s.Relay.GetOrCreateStream("/hidden")
	out.Name = "Station"
	out.SetVisible(false)
	token, _ := out.ClaimSource("source")
	first, _ := out.ActivatePlayback(token, "Track", s.Relay)
	httpServer := httptest.NewServer(http.HandlerFunc(s.handlePublicEvents))
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, httpServer.URL, nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	readStream := func() map[string]interface{} {
		t.Helper()
		event := ""
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(line, "event: ") {
				event = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
			}
			if strings.HasPrefix(line, "data: ") && event == "stream" {
				var payload map[string]interface{}
				if err = json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data: "))), &payload); err != nil {
					t.Fatal(err)
				}
				return payload
			}
			if line == "\n" {
				event = ""
			}
		}
	}
	initial := readStream()
	if initial["title"] != "Track" || initial["artist"] != first.Artist || initial["playback_id"] != first.PlaybackID || initial["revision"] != float64(first.Revision) || initial["started_at"] != first.StartedAt.UTC().Format(time.RFC3339) {
		t.Fatalf("stream tick did not capture one atomic playback snapshot: %#v", initial)
	}
	s.Relay.RemoveStream("/hidden")
	cleared := readStream()
	if cleared["title"] != "" || cleared["playback_id"] != "" || cleared["revision"].(float64) <= initial["revision"].(float64) || cleared["live"] != false {
		t.Fatalf("removed hidden mount did not reconcile terminal clear: %#v", cleared)
	}
}

func TestStudioSharedHandlersReturnJSONSuccess(t *testing.T) {
	name := "Studio JSON " + time.Now().Format("150405.000000000")
	dj := &config.AutoDJConfig{Name: name, Mount: "/json", MusicDir: t.TempDir(), Format: "mp3", Bitrate: 128}
	s := newAutoDJTestServer(t, dj)
	playlistName := name + ".pls"
	defer os.Remove(filepath.Join("playlists", playlistName))
	checks := []struct {
		handler      http.HandlerFunc
		target, body string
	}{
		{s.handlePlayerSavePlaylist, "/player/save?mount=/json", ""},
		{s.handlePlayerLoadPlaylist, "/player/load?mount=/json&file=" + url.QueryEscape(playlistName), ""},
		{s.handlePlayerMetadata, "/player/metadata?mount=/json", `{"enabled":true}`},
	}
	for _, check := range checks {
		response := httptest.NewRecorder()
		check.handler(response, autoDJRequest(http.MethodPost, check.target, check.body))
		if response.Code != 200 {
			t.Fatalf("Studio mutation status %d", response.Code)
		}
		var body map[string]string
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body["status"] != "ok" {
			t.Fatalf("Studio success is not JSON: %q, %v", response.Body.String(), err)
		}
	}
	if dj.LastPlaylist != playlistName {
		t.Fatal("Studio load did not persist selected playlist path")
	}
}
