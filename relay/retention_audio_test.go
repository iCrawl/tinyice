package relay

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	gomp3 "github.com/hajimehoshi/go-mp3"
)

func retentionToneWAV(t *testing.T, rate, seconds int) string {
	t.Helper()
	wav := makeWavHeader(2, rate, 16, rate*seconds*4)
	for i := 0; i < rate*seconds; i++ {
		v := int16(12000 * math.Sin(2*math.Pi*440*float64(i)/float64(rate)))
		binary.LittleEndian.PutUint16(wav[44+i*4:], uint16(v))
		binary.LittleEndian.PutUint16(wav[46+i*4:], uint16(v))
	}
	path := filepath.Join(t.TempDir(), "tone.wav")
	if err := os.WriteFile(path, wav, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func retentionBytes(t *testing.T, out *Stream) []byte {
	t.Helper()
	data := make([]byte, out.Buffer.HeadOffset())
	n, _, skipped := out.Buffer.ReadAt(0, data)
	if skipped {
		t.Fatal("fixture buffer wrapped")
	}
	return data[:n]
}

func TestPersistentAutoDJNormalizesPCMWithoutChangingDurationOrPitch(t *testing.T) {
	for _, rate := range []int{22050, 48000} {
		t.Run(itoa(rate), func(t *testing.T) {
			r := NewRelay(false, nil)
			s := &Streamer{Name: "tone", OutputMount: "/tone", Format: "mp3", Bitrate: 192, State: StatePlaying, InjectMetadata: true, relay: r}
			sm := NewStreamerManager(r, &config.Config{})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			path := retentionToneWAV(t, rate, 1)
			if err := sm.streamFile(ctx, s, path, 0, 1); err != nil {
				t.Fatal(err)
			}
			s.Stop()
			out, _ := r.GetStream("/tone")
			encoded := retentionBytes(t, out)
			for _, bitrate := range mp3FrameBitrates(t, encoded, 44100) {
				if bitrate != 192 {
					t.Fatalf("persistent output bitrate %d, want 192", bitrate)
				}
			}
			decoder, err := gomp3.NewDecoder(bytes.NewReader(encoded))
			if err != nil {
				t.Fatal(err)
			}
			pcm, err := io.ReadAll(decoder)
			if err != nil {
				t.Fatal(err)
			}
			frames := len(pcm) / 4
			if frames < 40000 || frames > 51000 {
				t.Fatalf("one-second %d Hz input decoded to %d frames at 44100 Hz", rate, frames)
			}
			start, end := 8192, frames-8192
			crossings := 0
			for i := start + 1; i < end; i++ {
				previous := int16(binary.LittleEndian.Uint16(pcm[(i-1)*4:]))
				current := int16(binary.LittleEndian.Uint16(pcm[i*4:]))
				if previous < 0 && current >= 0 {
					crossings++
				}
			}
			frequency := float64(crossings) * 44100 / float64(end-start)
			if math.Abs(frequency-440) > 8 {
				t.Fatalf("decoded tone frequency %.1f Hz, want 440", frequency)
			}
		})
	}
}

func TestPersistentAutoDJPauseResumesWithoutBurstOrNewPlayback(t *testing.T) {
	r := NewRelay(false, nil)
	sm := NewStreamerManager(r, &config.Config{})
	path := retentionToneWAV(t, 22050, 3)
	s, err := sm.StartStreamer("pause", "/pause", filepath.Dir(path), false, "mp3", 128, true, []string{path}, false, "", "", true, "", "", 0, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sm.Shutdown()
	s.Play()
	waitRetention(t, func() bool { out, ok := r.GetStream("/pause"); return ok && out.PlaybackSnapshot().PlaybackID != "" })
	out, _ := r.GetStream("/pause")
	before := out.PlaybackSnapshot()
	s.Pause()
	time.Sleep(80 * time.Millisecond)
	head := out.Buffer.HeadOffset()
	time.Sleep(350 * time.Millisecond)
	if out.Buffer.HeadOffset() != head {
		t.Fatal("pause published audio or gap filler")
	}
	s.Play()
	time.Sleep(200 * time.Millisecond)
	emitted := out.Buffer.HeadOffset() - head
	if emitted < 1000 || emitted > 5000 {
		t.Fatalf("resume emitted %d bytes in 200 ms at 128 kbps", emitted)
	}
	if out.PlaybackSnapshot().PlaybackID != before.PlaybackID {
		t.Fatal("pause resumed a different playback")
	}
}

func TestAutoDJManagerCannotAlterALiveClaimedMount(t *testing.T) {
	for _, format := range []string{"mp3", "opus"} {
		t.Run(format, func(t *testing.T) {
			r := NewRelay(false, nil)
			out := r.GetOrCreateStream("/live")
			out.ClaimSource("live encoder")
			out.ContentType = "live/type"
			out.Name = "Live"
			out.StoreOggHead([]byte("live setup"), 0)
			out.SetCurrentSong("Live track", r)
			snapshot := out.PlaybackSnapshot()
			head := out.Buffer.HeadOffset()
			sm := NewStreamerManager(r, &config.Config{})
			path := retentionToneWAV(t, 44100, 1)
			s, err := sm.StartStreamer("competing", "/live", filepath.Dir(path), true, format, 128, true, []string{path}, false, "", "", true, "", "", 0, "", 0)
			if err != nil {
				t.Fatal(err)
			}
			defer sm.Shutdown()
			s.Play()
			waitRetention(t, func() bool { return s.RecoverySnapshot().Attempt > 0 })
			if out.Buffer.HeadOffset() != head || out.Name != "Live" || out.ContentType != "live/type" || !bytes.Equal(out.GetOggHead(), []byte("live setup")) || out.PlaybackSnapshot() != snapshot || out.GetSourceIP() != "live encoder" {
				t.Fatal("actual competing AutoDJ changed live producer state")
			}
		})
	}
}

func TestHeaderOnlyAutoDJSelectionDoesNotOpenAnOutputSession(t *testing.T) {
	r := NewRelay(false, nil)
	s := &Streamer{Name: "empty", OutputMount: "/empty", Format: "opus", Bitrate: 128, State: StatePlaying, relay: r}
	sm := NewStreamerManager(r, &config.Config{})
	path := filepath.Join(t.TempDir(), "empty.wav")
	if err := os.WriteFile(path, makeWavHeader(2, 44100, 16, 0), 0600); err != nil {
		t.Fatal(err)
	}
	if err := sm.streamFile(context.Background(), s, path, 0, 1); err == nil {
		t.Fatal("header-only track was accepted")
	}
	if _, exists := r.GetStream("/empty"); exists {
		t.Fatal("invalid first track configured or published an output")
	}
}

func TestAutoDJWithoutMetadataInjectionStillActivatesPlaybackIdentity(t *testing.T) {
	r := NewRelay(false, nil)
	sm := NewStreamerManager(r, &config.Config{})
	path := retentionToneWAV(t, 44100, 1)
	s := &Streamer{Name: "private", OutputMount: "/private", Format: "mp3", Bitrate: 128, State: StatePlaying, InjectMetadata: false, relay: r}
	defer s.Shutdown()
	if err := sm.streamFile(context.Background(), s, path, 0, 1); err != nil {
		t.Fatal(err)
	}
	out, _ := r.GetStream("/private")
	playback := out.PlaybackSnapshot()
	if playback.PlaybackID == "" || playback.StartedAt.IsZero() || s.GetStats().StartTime.IsZero() {
		t.Fatal("metadata injection disabled real playback identity or timing")
	}
	if out.GetCurrentSong() != "" {
		t.Fatal("disabled metadata published public title")
	}
}
