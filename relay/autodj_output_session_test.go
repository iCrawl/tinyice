package relay

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type blockingFrameWriter struct {
	attemptCh chan struct{}
	releaseCh chan struct{}
}

func (w *blockingFrameWriter) WriteFrame(frame []int16) error {
	select {
	case w.attemptCh <- struct{}{}:
	default:
	}
	<-w.releaseCh
	return nil
}

func (w *blockingFrameWriter) Close() error { return nil }

func writeTestMP3File(t *testing.T) string {
	t.Helper()

	r := NewRelay(false, nil)
	stream := r.GetOrCreateStream("/fixture")
	session, err := NewMP3EncoderSession(stream, r, 64, 44100, nil)
	if err != nil {
		t.Fatalf("NewMP3EncoderSession: %v", err)
	}
	defer session.Close()

	frame := make([]int16, 1152*2)
	for i := 0; i < 8; i++ {
		if err := session.WriteFrame(frame); err != nil {
			t.Fatalf("WriteFrame #%d: %v", i+1, err)
		}
	}

	data := make([]byte, stream.Buffer.HeadOffset())
	n, _, skipped := stream.Buffer.ReadAt(0, data)
	if skipped {
		t.Fatal("unexpected buffer skip while building mp3 fixture")
	}

	path := filepath.Join(t.TempDir(), "track.mp3")
	if err := os.WriteFile(path, data[:n], 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func TestAutoDJOutputSessionEmitsSilenceUntilTrackIsReady(t *testing.T) {
	r := NewRelay(false, nil)
	streamer := &Streamer{
		Name:        "Gap Filler",
		OutputMount: "/gap",
		Format:      "mp3",
		Bitrate:     64,
		relay:       r,
	}

	session, err := NewAutoDJOutputSession(streamer)
	if err != nil {
		t.Fatalf("NewAutoDJOutputSession: %v", err)
	}
	defer session.Stop()

	if err := session.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	stream, ok := r.GetStream("/gap")
	if !ok || stream.Buffer.HeadOffset() == 0 {
		t.Fatal("expected encoded bytes while silence source is active")
	}
}

func TestAutoDJOutputSessionDoesNotPublishMetadataForSilence(t *testing.T) {
	r := NewRelay(false, nil)
	streamer := &Streamer{
		Name:        "Gap Filler",
		OutputMount: "/gap",
		Format:      "mp3",
		Bitrate:     64,
		relay:       r,
	}

	session, err := NewAutoDJOutputSession(streamer)
	if err != nil {
		t.Fatalf("NewAutoDJOutputSession: %v", err)
	}
	defer session.Stop()

	metaCh := r.SubscribeMetadata()
	defer r.UnsubscribeMetadata(metaCh)

	if err := session.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case evt := <-metaCh:
		t.Fatalf("unexpected silence metadata event: %+v", evt)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestAutoDJOutputSessionRegistersMountRuntime(t *testing.T) {
	r := NewRelay(false, nil)
	rr := NewRuntimeRegistry(r)
	streamer := &Streamer{
		Name:            "Gap Filler",
		OutputMount:     "/gap",
		Format:          "mp3",
		Bitrate:         64,
		relay:           r,
		runtimeRegistry: rr,
	}

	session, err := NewAutoDJOutputSession(streamer)
	if err != nil {
		t.Fatalf("NewAutoDJOutputSession: %v", err)
	}
	defer session.Stop()

	rt, ok := rr.Get("/gap")
	if !ok {
		t.Fatal("expected mount runtime registration")
	}
	if rt.Source != SourceAutoDJ {
		t.Fatalf("expected autodj source, got %q", rt.Source)
	}
}

func TestAutoDJPublishesMetadataAfterTrackFrameIsWritten(t *testing.T) {
	r := NewRelay(false, nil)
	streamer := &Streamer{
		Name:           "Gap Filler",
		OutputMount:    "/gap",
		Format:         "mp3",
		Bitrate:        64,
		InjectMetadata: true,
		relay:          r,
	}

	writer := &blockingFrameWriter{
		attemptCh: make(chan struct{}, 1),
		releaseCh: make(chan struct{}),
	}
	streamer.outputSession = &AutoDJOutputSession{
		streamer:    streamer,
		stream:      r.GetOrCreateStream("/gap"),
		frameWriter: writer,
		frameSize:   1152,
		channels:    2,
		sampleRate:  44100,
		source:      &sourceBinding{source: silencePCMSource{}},
	}

	metaCh := r.SubscribeMetadata()
	defer r.UnsubscribeMetadata(metaCh)

	sm := &StreamerManager{relay: r}
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := streamer.outputSession.Start(runCtx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	path := writeTestMP3File(t)
	done := make(chan error, 1)
	go func() {
		done <- sm.streamFile(runCtx, streamer, path, 0, 1)
	}()

	select {
	case <-writer.attemptCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first track frame write")
	}

	select {
	case evt := <-metaCh:
		t.Fatalf("metadata emitted before first track frame write completed: %+v", evt)
	default:
	}

	close(writer.releaseCh)

	select {
	case evt := <-metaCh:
		if evt.Mount != "/gap" {
			t.Fatalf("expected metadata for /gap, got %+v", evt)
		}
		if evt.Title != streamer.CurrentFile {
			t.Fatalf("expected metadata title %q, got %+v", streamer.CurrentFile, evt)
		}
	case err := <-done:
		t.Fatalf("streamFile returned before metadata event: %v", err)
	case <-time.After(time.Second):
		t.Fatal("expected metadata after first track frame write completed")
	}
}
