package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DatanoiseTV/tinyice/relay"
)

func TestMetadataSSEReplaysTerminalClearAfterSourceDisappearance(t *testing.T) {
	s := newListenerAPITestServer(t)
	out := s.Relay.GetOrCreateStream("/live")
	out.SetVisible(true)
	token, _ := out.ClaimSource("source")
	initial, _ := out.ActivatePlayback(token, "Current track", s.Relay)
	httpServer := httptest.NewServer(http.HandlerFunc(s.handleMetadataEvents))
	defer httpServer.Close()
	connect := func() (*bufio.Reader, func()) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, httpServer.URL, nil)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		return bufio.NewReader(response.Body), func() { cancel(); response.Body.Close() }
	}
	readMetadata := func(reader *bufio.Reader) map[string]interface{} {
		t.Helper()
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(line, "data: ") {
				var metadata map[string]interface{}
				if err = json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data: "))), &metadata); err != nil {
					t.Fatal(err)
				}
				return metadata
			}
		}
	}
	reader, closeConnection := connect()
	first := readMetadata(reader)
	if first["title"] != "Current track" || first["playback_id"] != initial.PlaybackID || first["artist"] != initial.Artist {
		t.Fatalf("metadata replay lost compatible fields: %#v", first)
	}
	s.Relay.RemoveStream("/live")
	cleared := readMetadata(reader)
	if cleared["title"] != "" || cleared["playback_id"] != "" || cleared["revision"].(float64) <= first["revision"].(float64) {
		t.Fatalf("source disappearance did not emit clear: %#v", cleared)
	}
	closeConnection()
	reader, closeConnection = connect()
	defer closeConnection()
	replay := readMetadata(reader)
	if replay["title"] != "" || replay["revision"] != cleared["revision"] {
		t.Fatalf("terminal clear did not replace last replay: %#v", replay)
	}
}

func TestMetadataSSEFiltersDisappearedPlaybackWithEmptyTitle(t *testing.T) {
	for _, endpoint := range []string{"combined", "metadata"} {
		t.Run(endpoint, func(t *testing.T) {
			s := newListenerAPITestServer(t)
			stale := relay.MetadataChange{Mount: "/gone", Visible: true, PlaybackID: "active-playback", Revision: 10}
			s.Relay.NotifyMetadataChange(stale)
			clear := relay.MetadataChange{Mount: "/clear", Visible: true, Revision: 1}
			s.Relay.NotifyMetadataChange(clear)
			handler := s.handleMetadataEvents
			if endpoint == "combined" {
				handler = s.handlePublicEvents
			}
			httpServer := httptest.NewServer(http.HandlerFunc(handler))
			defer httpServer.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, httpServer.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			reader := bufio.NewReader(response.Body)
			event := ""
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(line, "event: ") {
					event = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
				}
				if strings.HasPrefix(line, "data: ") && event == "metadata" {
					var metadata map[string]interface{}
					if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data: "))), &metadata); err != nil {
						t.Fatal(err)
					}
					if metadata["mount"] == "/gone" {
						t.Fatalf("disappeared active playback leaked as a terminal clear: %#v", metadata)
					}
					if metadata["mount"] != "/clear" || metadata["title"] != "" || metadata["playback_id"] != "" {
						t.Fatalf("terminal clear lost compatible fields: %#v", metadata)
					}
					if metadata["revision"] == float64(2) {
						return
					}
					stale.Revision++
					s.Relay.NotifyMetadataChange(stale)
					clear.Revision++
					s.Relay.NotifyMetadataChange(clear)
				}
				if line == "\n" {
					event = ""
				}
			}
		})
	}
}
