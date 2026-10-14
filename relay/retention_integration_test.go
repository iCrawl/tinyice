package relay

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitRetention(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for !condition() {
		select {
		case <-deadline.C:
			t.Fatal("retained feature did not reach expected state")
		case <-tick.C:
		}
	}
}

func TestAutoDJSessionRejectsClaimBeforeAnyHeaderOrConfiguration(t *testing.T) {
	for _, format := range []string{"mp3", "opus"} {
		t.Run(format, func(t *testing.T) {
			r := NewRelay(false, nil)
			out := r.GetOrCreateStream("/live")
			token, ok := out.ClaimSource("live-source")
			if !ok {
				t.Fatal("claim")
			}
			out.ContentType = "live/type"
			out.Name = "Live"
			out.StoreOggHead([]byte("live headers"), 0)
			out.SetCurrentSong("Live track", r)
			before := out.Buffer.HeadOffset()
			replay := out.PlaybackSnapshot()
			rr := NewRuntimeRegistry(r)
			rr.AttachClaim(out, token, SourceIcecast, "live-tenant")
			s := &Streamer{Name: "AutoDJ", OutputMount: "/live", Format: format, Bitrate: 128, relay: r, runtimeRegistry: rr}
			if session, err := NewAutoDJOutputSession(s); err == nil {
				session.Stop()
				t.Fatal("claimed live mount accepted AutoDJ")
			}
			if out.Buffer.HeadOffset() != before || out.ContentType != "live/type" || out.Name != "Live" || !bytes.Equal(out.GetOggHead(), []byte("live headers")) || out.PlaybackSnapshot() != replay {
				t.Fatal("rejected AutoDJ changed live stream")
			}
			rt, _ := rr.Get("/live")
			if rt.Source != SourceIcecast || rt.TenantID != "live-tenant" {
				t.Fatal("rejected AutoDJ changed ownership")
			}
		})
	}
}

func TestPlaybackRevisionSurvivesMountRecreationAndRejectsStaleReplay(t *testing.T) {
	r := NewRelay(false, nil)
	old := r.GetOrCreateStream("/song")
	token, _ := old.ClaimSource("producer")
	first, _ := old.ActivatePlayback(token, "same title", r)
	second, _ := old.ActivatePlayback(token, "same title", r)
	if first.PlaybackID == second.PlaybackID || second.Revision <= first.Revision || second.StartedAt.IsZero() {
		t.Fatal("repeat title lost playback identity")
	}
	r.RemoveStream("/song")
	clear := r.LastMetadata()[0]
	if clear.Title != "" || clear.PlaybackID != "" {
		t.Fatal("source disappearance did not clear replay")
	}
	next := r.GetOrCreateStream("/song")
	nextToken, _ := next.ClaimSource("successor")
	current, _ := next.ActivatePlayback(nextToken, "new title", r)
	r.NotifyMetadataChange(first)
	replay := r.LastMetadata()[0]
	if replay.PlaybackID != current.PlaybackID || replay.Revision <= clear.Revision {
		t.Fatal("recreated playback was overwritten by stale revision")
	}
}

func TestOwnershipSnapshotsAndOldCleanupPreserveHLSAndSuccessor(t *testing.T) {
	r := NewRelay(false, nil)
	rr := NewRuntimeRegistry(r)
	out := r.GetOrCreateStream("/live")
	old, _ := out.ClaimSource("old")
	rr.AttachClaim(out, old, SourceIcecast, "old-tenant")
	rr.AttachOutput("/live", OutputHLS)
	snapshot, _ := rr.Get("/live")
	snapshot.Source = SourceSRT
	delete(snapshot.Outputs, OutputHLS)
	out.ReleaseSourceIfCurrent(old)
	next, _ := out.ClaimSource("next")
	rr.AttachClaim(out, next, SourceSRT, "next-tenant")
	var oldCloserCalled atomic.Bool
	out.SetSourceCloserIfCurrent(next, func() { oldCloserCalled.Store(true) })
	if out.SetSourceCloserIfCurrent(old, nil) {
		t.Fatal("old cleanup replaced successor closer")
	}
	rr.DetachClaim(out, old)
	current, _ := rr.Get("/live")
	if current.Source != SourceSRT || current.TenantID != "next-tenant" || len(current.Outputs) != 1 {
		t.Fatal("snapshot or old detach corrupted ownership")
	}
	rr.DetachClaim(out, next)
	current, _ = rr.Get("/live")
	if current.Source != SourceUnknown || len(current.Outputs) != 1 {
		t.Fatal("source flap removed HLS output attachment")
	}
	out.KickSource()
	if !oldCloserCalled.Load() {
		t.Fatal("successor closer was lost")
	}
	if _, exists := r.GetStream("/facts-only"); exists {
		t.Fatal("unexpected fixture")
	}
	rr.GetOrCreate("/facts-only")
	if _, exists := r.GetStream("/facts-only"); exists {
		t.Fatal("registry created a data plane stream")
	}
}

func TestListenerAdmissionAndReservationsAreAtomicPerMount(t *testing.T) {
	r := NewListenerRegistry()
	var wg sync.WaitGroup
	var admitted atomic.Int32
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l := &Listener{ID: time.Now().Add(time.Duration(i)).Format(time.RFC3339Nano), CurrentMount: "/a", Connected: time.Now()}
			if r.Admit(l, 4) == nil {
				admitted.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if admitted.Load() != 4 || r.CountForMount("/a") != 4 {
		t.Fatalf("admitted %d, want 4", admitted.Load())
	}
	for _, id := range []string{"b1", "b2", "b3", "b4"} {
		if err := r.Admit(&Listener{ID: id, CurrentMount: "/b", Connected: time.Now()}, 4); err != nil {
			t.Fatal("global per-mount ceiling became server-total cap")
		}
	}
	l := &Listener{ID: "moving", CurrentMount: "/origin", Protocol: ListenerProtocolHTTP, MoveCh: make(chan ListenerCommand, 1), DisconnectCh: make(chan struct{}), Connected: time.Now()}
	r.Register(l)
	id, err := r.RequestMoveWithLimit(l.ID, "/destination", 1)
	if err != nil || id == "" {
		t.Fatal("move was not reserved")
	}
	if l.CurrentMountName() != "/origin" {
		t.Fatal("pending move changed current mount")
	}
	if err := r.Admit(&Listener{ID: "competing", CurrentMount: "/destination"}, 1); err == nil {
		t.Fatal("reservation did not occupy destination capacity")
	}
	if _, err := r.RequestMoveWithLimit(l.ID, "/other", 1); err == nil {
		t.Fatal("pending move was replaced")
	}
	if err := r.CompleteMove(l.ID, "/destination", time.Now()); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := r.Get(l.ID)
	if snapshot.MoveStatus != "completed" || snapshot.CurrentMount != "/destination" {
		t.Fatal("committed move not reported")
	}
	if _, err := r.ReserveMove(l.ID, "/other", 1); err != nil {
		t.Fatal(err)
	}
	r.Disconnect(l.ID)
	if err := r.Admit(&Listener{ID: "replacement", CurrentMount: "/other"}, 1); err != nil {
		t.Fatal("disconnect leaked reservation")
	}
}

func TestPartialFinalPCMIsEncodedAndNormalGapExpires(t *testing.T) {
	r := NewRelay(false, nil)
	s := &Streamer{Name: "partial", OutputMount: "/partial", Format: "mp3", Bitrate: 128, State: StatePlaying, relay: r}
	session, err := NewAutoDJOutputSession(s)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Stop()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := newReaderPCMFrameSource(ctx, bytes.NewReader(make([]byte, 400)), nil)
	var activated atomic.Bool
	session.SetSourceWithActivation(source, func() { activated.Store(true) })
	session.Start(ctx)
	select {
	case <-source.Done():
	case <-time.After(time.Second):
		t.Fatal("partial final PCM did not complete")
	}
	if !activated.Load() || session.stream.Buffer.HeadOffset() == 0 {
		t.Fatal("partial final PCM was dropped")
	}
	producer := session.stream.ProducerLastProgress()
	head := session.stream.Buffer.HeadOffset()
	time.Sleep(100 * time.Millisecond)
	if session.stream.Buffer.HeadOffset() <= head || !session.stream.ProducerLastProgress().Equal(producer) {
		t.Fatal("gap did not separate availability from producer progress")
	}
	time.Sleep(10 * time.Second)
	head = session.stream.Buffer.HeadOffset()
	time.Sleep(100 * time.Millisecond)
	if session.stream.Buffer.HeadOffset() != head {
		t.Fatal("normal gap exceeded ten-second bound")
	}
	frame := make([]int16, 5)
	_, err = source.ReadFrame(frame)
	if err != io.EOF {
		t.Fatalf("finished source read error %v", err)
	}
}

func TestIntentionalPauseAndStopSurviveHealthPolling(t *testing.T) {
	r := NewRelay(false, nil)
	s := &Streamer{Name: "intent", OutputMount: "/intent", Format: "mp3", Bitrate: 128, State: StatePlaying, relay: r}
	session, err := NewAutoDJOutputSession(s)
	if err != nil {
		t.Fatal(err)
	}
	s.outputSession = session
	out := session.stream
	out.mu.Lock()
	out.Started = time.Now().Add(-time.Hour)
	out.producerLastProgress = time.Now().Add(-time.Hour)
	out.mu.Unlock()
	health := NewHealthMonitor(r)
	var recoveryRequests atomic.Int32
	health.OnEvent(func(e StreamHealthEvent) {
		if e.NewStatus == StatusDead {
			recoveryRequests.Add(1)
		}
	})
	s.Pause()
	health.check()
	diagnostic, _ := r.Diagnostics.Current("/intent")
	if diagnostic.Status != DiagnosticStatusPaused || recoveryRequests.Load() != 0 {
		t.Fatal("health polling replaced intentional pause with dead recovery")
	}
	s.Stop()
	health.check()
	diagnostic, _ = r.Diagnostics.Current("/intent")
	if diagnostic.Status != DiagnosticStatusStopped || recoveryRequests.Load() != 0 {
		t.Fatal("health polling replaced intentional stop with dead recovery")
	}
}

func TestRemovedPersistentProducerCannotPublishIntoSuccessor(t *testing.T) {
	for _, format := range []string{"mp3", "opus"} {
		t.Run(format, func(t *testing.T) {
			r := NewRelay(false, nil)
			s := &Streamer{Name: "old", OutputMount: "/radio", Format: format, Bitrate: 128, State: StatePlaying, relay: r}
			session, err := NewAutoDJOutputSession(s)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Stop()
			r.RemoveStream("/radio")
			next := r.GetOrCreateStream("/radio")
			token, _ := next.ClaimSource("live-icecast")
			head := next.Buffer.HeadOffset()
			frameSize := 2304
			if format == "opus" {
				frameSize = 1920
			}
			if err = session.frameWriter.WriteFrame(make([]int16, frameSize)); !errors.Is(err, ErrSourceOwnershipLost) {
				t.Fatalf("stale writer error %v", err)
			}
			if next.Buffer.HeadOffset() != head || !next.IsCurrentSource(token) || s.GetStats().State != StateStopped {
				t.Fatal("removed producer wrote into successor or retained playback intent")
			}
		})
	}
}
func TestEndedSourceRemainsStoppedUnderHealthPolling(t *testing.T) {
	r := NewRelay(false, nil)
	out := r.GetOrCreateStream("/ended")
	token, _ := out.ClaimSource("source")
	r.Diagnostics.Record(DiagnosticUpdate{Mount: "/ended", Status: DiagnosticStatusStopped, Class: DiagnosticClassSourceDisconnect, Actor: DiagnosticActorWebRTC, Reason: "source disconnected"})
	out.ReleaseSourceIfCurrent(token)
	out.mu.Lock()
	out.LastDataReceived = time.Now().Add(-time.Hour)
	out.Started = time.Now().Add(-time.Hour)
	out.mu.Unlock()
	health := NewHealthMonitor(r)
	health.check()
	diagnostic, _ := r.Diagnostics.Current("/ended")
	if diagnostic.Status != DiagnosticStatusStopped || diagnostic.Class != DiagnosticClassSourceDisconnect {
		t.Fatal("health polling overwrote ended source with stall diagnostic")
	}
	next, _ := out.ClaimSource("successor")
	if !out.IsCurrentSource(next) {
		t.Fatal("successor claim failed")
	}
	health.check()
	diagnostic, _ = r.Diagnostics.Current("/ended")
	if diagnostic.Status != DiagnosticStatusDead {
		t.Fatal("terminal exclusion masked stalled attached successor")
	}
}
