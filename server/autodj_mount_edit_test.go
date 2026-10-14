package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/relay"
)

func createEditableAutoDJ(t *testing.T) *Server {
	t.Helper()
	s := newAutoDJTestServer(t)
	w := httptest.NewRecorder()
	s.apiCreateAutoDJ(w, autoDJRequest(http.MethodPost, "/api/autodj", `{"name":"radio","mount":"/radio","song_command":"sleep 60","visible":true}`))
	t.Cleanup(func() { s.StreamerM.DeleteStreamer("/radio") })
	if w.Code != http.StatusOK {
		t.Fatalf("create AutoDJ: %d %s", w.Code, w.Body.String())
	}
	if _, ok := s.Config.Mounts["/radio"]; ok {
		t.Fatal("AutoDJ creation unexpectedly assigned a source password")
	}
	return s
}

func TestStreamUpdateEditsConfiguredAutoDJWithoutPassword(t *testing.T) {
	for _, runtime := range []bool{true, false} {
		name := "configured_only"
		if runtime {
			name = "active"
		}
		t.Run(name, func(t *testing.T) {
			s := createEditableAutoDJ(t)
			if !runtime {
				s.StreamerM.DeleteStreamer("/radio")
			}
			out := s.Relay.GetOrCreateStream("/radio")
			out.SetVisible(true)
			w := httptest.NewRecorder()
			s.apiUpdateStream(w, autoDJRequest(http.MethodPut, "/api/streams", `{"mount":"radio","max_listeners":1,"burst_size":4096,"visible":false}`))
			if w.Code != http.StatusOK {
				t.Fatalf("edit AutoDJ mount: %d %s", w.Code, w.Body.String())
			}
			if out.IsVisible() {
				t.Fatal("active stream stayed visible after the mount edit")
			}
			if runtime {
				session, err := relay.NewAutoDJOutputSession(s.StreamerM.GetStreamer("/radio"))
				if err != nil {
					t.Fatal(err)
				}
				if out.IsVisible() {
					t.Fatal("new output session restored stale visibility")
				}
				session.Stop()
			}
			w = httptest.NewRecorder()
			s.apiGetStreams(w, autoDJRequest(http.MethodGet, "/api/streams", ""))
			var streams []struct {
				Mount        string `json:"mount"`
				Visible      bool   `json:"visible"`
				MaxListeners int    `json:"max_listeners"`
				BurstSize    int    `json:"burst_size"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &streams); err != nil {
				t.Fatal(err)
			}
			if len(streams) != 1 || streams[0].Mount != "/radio" || streams[0].Visible || streams[0].MaxListeners != 1 || streams[0].BurstSize != 4096 {
				t.Fatalf("stream API lost edited settings: %#v", streams)
			}
			for _, enabled := range []bool{false, true, false} {
				w = httptest.NewRecorder()
				body := `{"mount":"/radio","enabled":false}`
				wantState := relay.StateStopped
				if enabled {
					body = `{"mount":"/radio","enabled":true}`
					wantState = relay.StatePlaying
				}
				s.apiUpdateStream(w, autoDJRequest(http.MethodPut, "/api/streams", body))
				if w.Code != http.StatusOK {
					t.Fatalf("set enabled=%v: %d %s", enabled, w.Code, w.Body.String())
				}
				if runtime && s.StreamerM.GetStreamer("/radio").GetStats().State != wantState {
					t.Fatalf("enabled=%v did not change playback state to %v", enabled, wantState)
				}
				if runtime && !enabled {
					w = httptest.NewRecorder()
					s.apiAutoDJPlay(w, autoDJRequest(http.MethodPost, "/api/autodj/play?mount=/radio", ""))
					if w.Code != http.StatusConflict {
						t.Fatalf("disabled AutoDJ play: %d %s", w.Code, w.Body.String())
					}
					w = httptest.NewRecorder()
					s.handlePlayerToggle(w, autoDJRequest(http.MethodPost, "/admin/player/toggle?mount=/radio", ""))
					if w.Code != http.StatusSeeOther || s.StreamerM.GetStreamer("/radio").GetStats().State != relay.StateStopped {
						t.Fatal("Studio toggle restarted a disabled mount")
					}
				}
				if runtime && enabled {
					w = httptest.NewRecorder()
					s.apiAutoDJPause(w, autoDJRequest(http.MethodPost, "/api/autodj/pause?mount=/radio", ""))
					if w.Code != http.StatusOK || s.StreamerM.GetStreamer("/radio").GetStats().State != relay.StatePaused {
						t.Fatal("enabled AutoDJ could not pause")
					}
					w = httptest.NewRecorder()
					s.apiAutoDJPlay(w, autoDJRequest(http.MethodPost, "/api/autodj/play?mount=/radio", ""))
					if w.Code != http.StatusOK || s.StreamerM.GetStreamer("/radio").GetStats().State != relay.StatePlaying {
						t.Fatal("enabled AutoDJ could not resume")
					}
				}
				w = httptest.NewRecorder()
				s.apiGetStreams(w, autoDJRequest(http.MethodGet, "/api/streams", ""))
				var mountRows []struct {
					Mount   string `json:"mount"`
					Enabled bool   `json:"enabled"`
					Visible bool   `json:"visible"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &mountRows); err != nil {
					t.Fatal(err)
				}
				if len(mountRows) != 1 || mountRows[0].Mount != "/radio" || mountRows[0].Enabled != enabled || mountRows[0].Visible {
					t.Fatalf("enabled=%v lost the editable mount row: %#v", enabled, mountRows)
				}
				w = httptest.NewRecorder()
				s.apiGetAutoDJ(w, autoDJRequest(http.MethodGet, "/api/autodj", ""))
				var djs []struct {
					Enabled bool `json:"enabled"`
					Visible bool `json:"visible"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &djs); err != nil {
					t.Fatal(err)
				}
				if len(djs) != 1 || djs[0].Enabled != enabled || djs[0].Visible {
					t.Fatalf("AutoDJ API disagrees with mount edit: %#v", djs)
				}
				loaded, err := config.LoadConfig(s.Config.ConfigPath)
				if err != nil {
					t.Fatal(err)
				}
				if loaded.AutoDJs[0].Enabled != enabled || loaded.AutoDJs[0].Visible || loaded.DisabledMounts["/radio"] == enabled || loaded.VisibleMounts["/radio"] || loaded.AdvancedMounts["/radio"].MaxListeners != 1 || loaded.AdvancedMounts["/radio"].BurstSize != 4096 {
					t.Fatalf("reload lost AutoDJ mount settings: %#v", loaded.AutoDJs[0])
				}
				if !enabled {
					restarted := newAutoDJTestServer(t, loaded.AutoDJs[0])
					stats := restarted.StreamerM.GetStreamer("/radio").GetStats()
					if stats.State != relay.StateStopped || stats.Visible {
						t.Fatalf("restarted AutoDJ lost stopped/hidden settings: %#v", stats)
					}
				}
			}
		})
	}
}

func TestStreamUpdateAutoDJRetainsMountAccessAndPasswordOwnership(t *testing.T) {
	s := createEditableAutoDJ(t)
	tenant := &config.User{Username: "tenant", Role: config.RoleAdmin, Mounts: map[string]string{"/own": "password"}}
	s.Config.Users[tenant.Username] = tenant
	s.sessions["tenant"] = &session{User: tenant, CSRFToken: "tok", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour), LastSeen: time.Now()}
	for _, tc := range []struct {
		name string
		body string
		sid  string
		want int
	}{
		{"other_tenant", `{"mount":"/radio","max_listeners":1}`, "tenant", http.StatusForbidden},
		{"password_without_owner", `{"mount":"/radio","password":"new","visible":false}`, "sid", http.StatusBadRequest},
		{"empty_password", `{"mount":"/radio","password":""}`, "sid", http.StatusOK},
		{"runtime_is_not_configuration", `{"mount":"/runtime","max_listeners":1}`, "sid", http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s.Relay.GetOrCreateStream("/runtime")
			r := autoDJRequest(http.MethodPut, "/api/streams", tc.body)
			r.Header.Del("Cookie")
			r.AddCookie(&http.Cookie{Name: "sid", Value: tc.sid})
			w := httptest.NewRecorder()
			s.apiUpdateStream(w, r)
			if w.Code != tc.want {
				t.Fatalf("status=%d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			if !s.Config.AutoDJs[0].Visible || s.Config.AdvancedMounts["/radio"] != nil {
				t.Fatal("rejected update changed AutoDJ settings")
			}
		})
	}
}
