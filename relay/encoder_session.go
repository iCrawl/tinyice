package relay

import (
	"time"

	shine "github.com/braheezy/shine-mp3/pkg/mp3"
	"github.com/kazzmir/opus-go/ogg"
	"github.com/kazzmir/opus-go/opus"
)

type PCMFrameWriter interface {
	WriteFrame(frame []int16) error
	Close() error
}

type MP3EncoderSession struct {
	stream     *Stream
	relay      *Relay
	stats      *int64
	encoder    *shine.Encoder
	sampleRate int
}

func NewMP3EncoderSession(stream *Stream, relay *Relay, bitrate, sampleRate int, stats *int64) (*MP3EncoderSession, error) {
	if sampleRate <= 0 {
		sampleRate = 44100
	}
	encoder := shine.NewEncoder(sampleRate, 2)
	if bitrate > 0 && bitrate != 128 {
		applyShineBitrate(encoder, sampleRate, bitrate)
	}
	return &MP3EncoderSession{
		stream:     stream,
		relay:      relay,
		stats:      stats,
		encoder:    encoder,
		sampleRate: sampleRate,
	}, nil
}

func (s *MP3EncoderSession) WriteFrame(frame []int16) error {
	writer := &streamWriter{stream: s.stream, relay: s.relay, stats: s.stats}
	return s.encoder.Write(writer, frame)
}

func (s *MP3EncoderSession) Close() error { return nil }

type OpusEncoderSession struct {
	stream       *Stream
	relay        *Relay
	stats        *int64
	encoder      *opus.Encoder
	packetWriter *ogg.PacketWriter
	frameSize    int
	granulePos   uint64
}

func NewOpusEncoderSession(stream *Stream, relay *Relay, bitrate, sampleRate, channels int, stats *int64) (*OpusEncoderSession, error) {
	const encodeSampleRate = 48000
	const frameMS = 20
	const frameSize = encodeSampleRate * frameMS / 1000

	if sampleRate <= 0 {
		sampleRate = encodeSampleRate
	}
	if channels <= 0 {
		channels = 2
	}

	enc, err := opus.NewEncoder(encodeSampleRate, channels, opus.ApplicationAudio)
	if err != nil {
		return nil, err
	}
	if bitrate > 0 {
		enc.SetBitrate(bitrate * 1000)
	}

	writer := &streamWriter{stream: stream, relay: relay, stats: stats, capture: true}
	pw := ogg.NewPacketWriter(writer, uint32(time.Now().UnixNano()))

	head := ogg.OpusHead{
		Version:         1,
		Channels:        uint8(channels),
		InputSampleRate: uint32(sampleRate),
	}
	headPacket, _ := ogg.BuildOpusHeadPacket(head)
	if err := pw.WritePacket(headPacket, 0, true, false); err != nil {
		enc.Close()
		return nil, err
	}
	pw.Flush()

	tags := ogg.OpusTags{Vendor: "tinyice-opus"}
	tagsPacket, _ := ogg.BuildOpusTagsPacket(tags)
	if err := pw.WritePacket(tagsPacket, 0, false, false); err != nil {
		enc.Close()
		return nil, err
	}
	pw.Flush()

	stream.StoreOggHead(writer.headerBuf.Bytes(), stream.Buffer.HeadOffset())
	writer.capture = false

	return &OpusEncoderSession{
		stream:       stream,
		relay:        relay,
		stats:        stats,
		encoder:      enc,
		packetWriter: pw,
		frameSize:    frameSize,
	}, nil
}

func (s *OpusEncoderSession) WriteFrame(frame []int16) error {
	packet := make([]byte, 4000)
	n, err := s.encoder.Encode(frame, s.frameSize, packet)
	if err != nil {
		return err
	}

	s.granulePos += uint64(s.frameSize)
	if err := s.packetWriter.WritePacket(packet[:n], s.granulePos, false, false); err != nil {
		return err
	}
	s.packetWriter.Flush()
	return nil
}

func (s *OpusEncoderSession) Close() error {
	if s.encoder != nil {
		s.encoder.Close()
	}
	return nil
}
