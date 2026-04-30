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
			<-ctx.Done()
			s.closeDone()
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
	if err != nil {
		s.closeDone()
		return 0, err
	}
	for i := 0; i < n/2; i++ {
		dst[i] = int16(buf[i*2]) | int16(buf[i*2+1])<<8
	}
	return n / 2, nil
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
	streamer    *Streamer
	stream      *Stream
	frameWriter PCMFrameWriter
	frameSize   int
	channels    int
	sampleRate  int

	mu     sync.RWMutex
	source *sourceBinding
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewAutoDJOutputSession(streamer *Streamer) (*AutoDJOutputSession, error) {
	if streamer == nil || streamer.relay == nil {
		return nil, fmt.Errorf("streamer relay is required")
	}

	stream := streamer.relay.GetOrCreateStream(streamer.OutputMount)
	if streamer.runtimeRegistry != nil {
		rt := streamer.runtimeRegistry.GetOrCreate(streamer.OutputMount)
		rt.Stream = stream
		streamer.runtimeRegistry.AttachSource(streamer.OutputMount, SourceAutoDJ, "default")
	}

	sampleRate := 44100
	frameSize := 1152
	contentType := "audio/mpeg"

	var writer PCMFrameWriter
	var err error
	if streamer.Format == "opus" {
		sampleRate = 48000
		frameSize = 960
		contentType = "audio/ogg"
		writer, err = NewOpusEncoderSession(stream, streamer.relay, streamer.Bitrate, sampleRate, 2, &streamer.BytesStreamed)
	} else {
		writer, err = NewMP3EncoderSession(stream, streamer.relay, streamer.Bitrate, sampleRate, &streamer.BytesStreamed)
	}
	if err != nil {
		return nil, err
	}

	stream.ConfigureAutoDJOutput(streamer.Name, fmt.Sprintf("%d", streamer.Bitrate), contentType, streamer.Visible)

	return &AutoDJOutputSession{
		streamer:    streamer,
		stream:      stream,
		frameWriter: writer,
		frameSize:   frameSize,
		channels:    2,
		sampleRate:  sampleRate,
		source:      &sourceBinding{source: silencePCMSource{}},
	}, nil
}

func (s *AutoDJOutputSession) Start(ctx context.Context) error {
	s.mu.Lock()
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
			select {
			case <-runCtx.Done():
				return
			default:
			}

			s.mu.RLock()
			binding := s.source
			s.mu.RUnlock()

			source := pcmFrameSource(silencePCMSource{})
			if binding != nil && binding.source != nil {
				source = binding.source
			}

			if _, err := source.ReadFrame(frame); err != nil {
				s.SetSource(silencePCMSource{})
				continue
			}
			if err := s.frameWriter.WriteFrame(frame); err != nil {
				return
			}
			s.markActivated(binding)

			nextTick = nextTick.Add(frameDuration)
			if sleepFor := time.Until(nextTick); sleepFor > 0 {
				time.Sleep(sleepFor)
			}
		}
	}()

	return nil
}

func (s *AutoDJOutputSession) SetSource(source pcmFrameSource) {
	s.SetSourceWithActivation(source, nil)
}

func (s *AutoDJOutputSession) SetSourceWithActivation(source pcmFrameSource, onActivate func()) {
	if source == nil {
		source = silencePCMSource{}
		onActivate = nil
	}

	s.mu.Lock()
	s.source = &sourceBinding{
		source:     source,
		onActivate: onActivate,
	}
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
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.mu.Unlock()

	if cancel != nil {
		cancel()
		s.wg.Wait()
	}

	if s.frameWriter != nil {
		_ = s.frameWriter.Close()
	}
}
