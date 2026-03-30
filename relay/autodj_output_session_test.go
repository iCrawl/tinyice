package relay

import (
	"context"
	"testing"
	"time"
)

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
	if !ok || stream.Buffer.HeadPosition() == 0 {
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
