package relay

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

type pcmFrameSource interface {
	ReadFrame(dst []int16) (int, error)
}

type sourceBinding struct {
	source     pcmFrameSource
	onActivate func()
	activated  bool
}

type silencePCMSource struct{}

func (s silencePCMSource) ReadFrame(dst []int16) (int, error) {
	for i := range dst {
		dst[i] = 0
	}
	return len(dst), nil
}

type readerPCMFrameSource struct {
	ctx    context.Context
	reader io.Reader
	closer io.Closer
	done   chan struct{}
	once   sync.Once
}

func newReaderPCMFrameSource(ctx context.Context, reader io.Reader, closer io.Closer) *readerPCMFrameSource {
	if ctx == nil {
		ctx = context.Background()
	}
	s := &readerPCMFrameSource{
		ctx:    ctx,
		reader: reader,
		closer: closer,
		done:   make(chan struct{}),
	}
	if closer != nil {
		go func() {
			select {
			case <-ctx.Done():
				s.closeDone()
			case <-s.done:
			}
		}()
	}
	return s
}

func (s *readerPCMFrameSource) ReadFrame(dst []int16) (int, error) {
	select {
	case <-s.ctx.Done():
		s.closeDone()
		return 0, s.ctx.Err()
	default:
	}

	buf := make([]byte, len(dst)*2)
	n, err := io.ReadFull(s.reader, buf)
	if n == 0 && err != nil {
		return 0, err
	}
	for i := 0; i < n/2; i++ {
		dst[i] = int16(buf[i*2]) | int16(buf[i*2+1])<<8
	}
	return n / 2, err
}

func (s *readerPCMFrameSource) Done() <-chan struct{} {
	return s.done
}

func (s *readerPCMFrameSource) closeDone() {
	s.once.Do(func() {
		if s.closer != nil {
			_ = s.closer.Close()
		}
		close(s.done)
	})
}

type AutoDJOutputSession struct {
	closed      bool
	streamer    *Streamer
	stream      *Stream
	frameWriter PCMFrameWriter
	frameSize   int
	channels    int
	sampleRate  int

	token    int64
	gapUntil time.Time
	wake     chan struct{}
	stopOnce sync.Once
	mu       sync.RWMutex
	source   *sourceBinding
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

func NewAutoDJOutputSession(streamer *Streamer) (*AutoDJOutputSession, error) {
	if streamer == nil || streamer.relay == nil {
		return nil, fmt.Errorf("streamer relay is required")
	}
	streamer.mu.RLock()
	defer streamer.mu.RUnlock()
	return newAutoDJOutputSessionLocked(streamer)
}

func newAutoDJOutputSessionLocked(streamer *Streamer) (*AutoDJOutputSession, error) {
	stream := streamer.relay.GetOrCreateStream(streamer.OutputMount)
	token, ok := stream.ClaimSource(autoDJSourceLabel)
	if !ok {
		return nil, fmt.Errorf("mount %s already has a live source", streamer.OutputMount)
	}
	session := &AutoDJOutputSession{streamer: streamer, stream: stream, token: token, wake: make(chan struct{}, 1)}
	stream.SetSourceCloserIfCurrent(token, func() { streamer.Stop(); session.Stop() })
	stream.BeginSession()

	sampleRate := 44100
	frameSize := 1152
	contentType := "audio/mpeg"

	if streamer.Format == "opus" {
		sampleRate = 48000
		frameSize = 960
		contentType = "audio/ogg"
	}
	stream.ConfigureAutoDJOutput(streamer.Name, fmt.Sprintf("%d", streamer.Bitrate), contentType, streamer.Visible)

	var writer PCMFrameWriter
	var err error
	if streamer.Format == "opus" {
		writer, err = NewOpusEncoderSession(stream, streamer.relay, streamer.Bitrate, sampleRate, 2, &streamer.BytesStreamed, token)
	} else {
		writer, err = NewMP3EncoderSession(stream, streamer.relay, streamer.Bitrate, sampleRate, &streamer.BytesStreamed, token)
	}
	if err != nil {
		stream.ReleaseSourceIfCurrent(token)
		return nil, err
	}
	if streamer.runtimeRegistry != nil {
		streamer.runtimeRegistry.AttachClaim(stream, token, SourceAutoDJ, "default")
	}

	session.mu.Lock()
	if session.closed {
		session.mu.Unlock()
		_ = writer.Close()
		return nil, ErrSourceOwnershipLost
	}
	session.frameWriter = writer
	session.frameSize = frameSize
	session.channels = 2
	session.sampleRate = sampleRate
	session.mu.Unlock()
	return session, nil

}

func (s *AutoDJOutputSession) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrSourceOwnershipLost
	}
	if s.cancel != nil {
		s.mu.Unlock()
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.wg.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.wg.Done()
		frame := make([]int16, s.frameSize*s.channels)
		frameDuration := time.Second * time.Duration(s.frameSize) / time.Duration(s.sampleRate)
		nextTick := time.Now()
		for {
			if runCtx.Err() != nil {
				return
			}
			s.streamer.mu.RLock()
			paused := s.streamer.State == StatePaused
			s.streamer.mu.RUnlock()
			s.mu.RLock()
			binding := s.source
			gap := s.gapUntil
			s.mu.RUnlock()
			if paused || (binding == nil && !time.Now().Before(gap)) {
				nextTick = time.Now()
				select {
				case <-runCtx.Done():
					return
				case <-s.wake:
				case <-time.After(50 * time.Millisecond):
				}
				continue
			}
			clear(frame)
			n := len(frame)
			var err error
			if binding != nil {
				n, err = binding.source.ReadFrame(frame)
			}
			if runCtx.Err() != nil {
				return
			}
			if n > 0 {
				if writeErr := s.frameWriter.WriteFrame(frame); writeErr != nil {
					return
				}
				if binding != nil {
					s.stream.RecordProducerProgress()
					if diag, ok := s.streamer.relay.Diagnostics.Current(s.stream.MountName); ok && diag.Status == DiagnosticStatusPaused {
						s.streamer.relay.Diagnostics.Record(DiagnosticUpdate{Mount: s.stream.MountName, Status: DiagnosticStatusRunning, Class: DiagnosticClassPlaybackStarted, Reason: "AutoDJ resumed", Actor: DiagnosticActorAutoDJ, Timestamp: time.Now()})
					}
					s.markActivated(binding)
				}
				now := time.Now()
				if nextTick.Before(now) {
					nextTick = now
				}
				nextTick = nextTick.Add(frameDuration)
				timer := time.NewTimer(time.Until(nextTick))
				select {
				case <-runCtx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
			if err != nil {
				s.mu.Lock()
				if s.source == binding {
					s.source = nil
					if binding.activated {
						s.gapUntil = time.Now().Add(10 * time.Second)
					}
				}
				s.mu.Unlock()
				if src, ok := binding.source.(*readerPCMFrameSource); ok {
					src.closeDone()
				}
			}
		}
	}()

	return nil
}

func (s *AutoDJOutputSession) SetSource(source pcmFrameSource) {
	s.SetSourceWithActivation(source, nil)
}

func (s *AutoDJOutputSession) SetSourceWithActivation(source pcmFrameSource, onActivate func()) {
	s.mu.Lock()
	if _, silence := source.(silencePCMSource); silence {
		source = nil
	}
	if source == nil {
		if s.source != nil && s.source.activated {
			s.gapUntil = time.Now().Add(10 * time.Second)
		}
		s.source = nil
	} else {
		s.source = &sourceBinding{source: source, onActivate: onActivate}
		s.gapUntil = time.Time{}
	}
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *AutoDJOutputSession) Park() {
	s.mu.Lock()
	s.source = nil
	s.gapUntil = time.Time{}
	s.mu.Unlock()
}

func (s *AutoDJOutputSession) markActivated(binding *sourceBinding) {
	if binding == nil {
		return
	}

	var callback func()
	s.mu.Lock()
	if s.source == binding && !binding.activated {
		binding.activated = true
		callback = binding.onActivate
		binding.onActivate = nil
	}
	s.mu.Unlock()

	if callback != nil {
		callback()
	}
}

func (s *AutoDJOutputSession) Stop() {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		cancel := s.cancel
		binding := s.source
		s.cancel = nil
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if binding != nil {
			if src, ok := binding.source.(*readerPCMFrameSource); ok {
				src.closeDone()
			}
		}
		s.wg.Wait()
		s.mu.RLock()
		writer := s.frameWriter
		s.mu.RUnlock()
		if writer != nil {
			_ = writer.Close()
		}
		if s.stream.IsCurrentSource(s.token) {
			s.stream.SetCurrentSong("", s.streamer.relay)
		}
		if s.streamer.runtimeRegistry != nil {
			s.streamer.runtimeRegistry.DetachClaim(s.stream, s.token)
		}
		s.stream.ReleaseSourceIfCurrent(s.token)
	})
}
