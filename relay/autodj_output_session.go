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

type silencePCMSource struct{}

func (s silencePCMSource) ReadFrame(dst []int16) (int, error) {
	for i := range dst {
		dst[i] = 0
	}
	return len(dst), nil
}

type AutoDJOutputSession struct {
	streamer    *Streamer
	stream      *Stream
	frameWriter PCMFrameWriter
	frameSize   int
	channels    int
	sampleRate  int

	mu     sync.RWMutex
	source pcmFrameSource
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewAutoDJOutputSession(streamer *Streamer) (*AutoDJOutputSession, error) {
	if streamer == nil || streamer.relay == nil {
		return nil, fmt.Errorf("streamer relay is required")
	}

	stream := streamer.relay.GetOrCreateStream(streamer.OutputMount)
	stream.Name = streamer.Name
	stream.Bitrate = fmt.Sprintf("%d", streamer.Bitrate)
	stream.Visible = streamer.Visible

	sampleRate := 44100
	frameSize := 1152
	stream.ContentType = "audio/mpeg"
	stream.IsOggStream = false

	var writer PCMFrameWriter
	var err error
	if streamer.Format == "opus" {
		sampleRate = 48000
		frameSize = 960
		stream.ContentType = "audio/ogg"
		stream.IsOggStream = true
		writer, err = NewOpusEncoderSession(stream, streamer.relay, streamer.Bitrate, sampleRate, 2, &streamer.BytesStreamed)
	} else {
		writer, err = NewMP3EncoderSession(stream, streamer.relay, streamer.Bitrate, sampleRate, &streamer.BytesStreamed)
	}
	if err != nil {
		return nil, err
	}

	return &AutoDJOutputSession{
		streamer:    streamer,
		stream:      stream,
		frameWriter: writer,
		frameSize:   frameSize,
		channels:    2,
		sampleRate:  sampleRate,
		source:      silencePCMSource{},
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
			source := s.source
			s.mu.RUnlock()
			if source == nil {
				source = silencePCMSource{}
			}

			if _, err := source.ReadFrame(frame); err != nil {
				if err != io.EOF {
					s.SetSource(silencePCMSource{})
				} else {
					s.SetSource(silencePCMSource{})
				}
				continue
			}
			if err := s.frameWriter.WriteFrame(frame); err != nil {
				return
			}

			nextTick = nextTick.Add(frameDuration)
			if sleepFor := time.Until(nextTick); sleepFor > 0 {
				time.Sleep(sleepFor)
			}
		}
	}()

	return nil
}

func (s *AutoDJOutputSession) SetSource(source pcmFrameSource) {
	if source == nil {
		source = silencePCMSource{}
	}

	s.mu.Lock()
	s.source = source
	s.mu.Unlock()
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
