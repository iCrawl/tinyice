package relay

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/logger"
	shine "github.com/braheezy/shine-mp3/pkg/mp3"
	"github.com/hajimehoshi/go-mp3"
	"github.com/kazzmir/opus-go/ogg"
	"github.com/kazzmir/opus-go/opus"
)

var mp3BitratesByVersion = map[int]map[int]int64{
	3: {
		32:  1,
		40:  2,
		48:  3,
		56:  4,
		64:  5,
		80:  6,
		96:  7,
		112: 8,
		128: 9,
		160: 10,
		192: 11,
		224: 12,
		256: 13,
		320: 14,
	},
	2: {
		8:   1,
		16:  2,
		24:  3,
		32:  4,
		40:  5,
		48:  6,
		56:  7,
		64:  8,
		80:  9,
		96:  10,
		112: 11,
		128: 12,
		144: 13,
		160: 14,
	},
	0: {
		8:   1,
		16:  2,
		24:  3,
		32:  4,
		40:  5,
		48:  6,
		56:  7,
		64:  8,
		80:  9,
		96:  10,
		112: 11,
		128: 12,
		144: 13,
		160: 14,
	},
}

func configureMP3EncoderBitrate(encoder *shine.Encoder, bitrate int) {
	if bitrate <= 0 {
		return
	}

	version := int(encoder.Mpeg.Version)
	bitrateIndex, ok := mp3BitratesByVersion[version][bitrate]
	if !ok {
		if logger.L != nil {
			logger.L.Warnf("MP3: unsupported bitrate %d kbps for sample rate %d; keeping default %d kbps", bitrate, encoder.Wave.SampleRate, encoder.Mpeg.Bitrate)
		}
		return
	}

	avgSlotsPerFrame := (float64(encoder.Mpeg.GranulesPerFrame) * shine.GRANULE_SIZE / float64(encoder.Wave.SampleRate)) *
		(float64(bitrate) * 1000 / float64(encoder.Mpeg.BitsPerSlot))

	encoder.Mpeg.Bitrate = int64(bitrate)
	encoder.Mpeg.BitrateIndex = bitrateIndex
	encoder.Mpeg.WholeSlotsPerFrame = int64(avgSlotsPerFrame)
	encoder.Mpeg.FracSlotsPerFrame = avgSlotsPerFrame - float64(encoder.Mpeg.WholeSlotsPerFrame)
	encoder.Mpeg.Slot_lag = -encoder.Mpeg.FracSlotsPerFrame
	if encoder.Mpeg.FracSlotsPerFrame == 0 {
		encoder.Mpeg.Padding = 0
	}
}

type TranscoderInstance struct {
	Config *config.TranscoderConfig
	cancel context.CancelFunc
	active bool
	mu     sync.Mutex

	// Stats
	FramesProcessed int64
	BytesEncoded    int64
	StartTime       time.Time
}

type TranscoderManager struct {
	instances map[string]*TranscoderInstance // key is OutputMount
	mu        sync.RWMutex
	relay     *Relay
}

func NewTranscoderManager(r *Relay) *TranscoderManager {
	return &TranscoderManager{
		instances: make(map[string]*TranscoderInstance),
		relay:     r,
	}
}

func (tm *TranscoderManager) StartTranscoder(cfg *config.TranscoderConfig) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if inst, ok := tm.instances[cfg.OutputMount]; ok {
		inst.Stop()
	}

	ctx, cancel := context.WithCancel(context.Background())
	inst := &TranscoderInstance{
		Config:    cfg,
		cancel:    cancel,
		active:    true,
		StartTime: time.Now(),
	}
	tm.instances[cfg.OutputMount] = inst

	go tm.runTranscoder(ctx, inst)
}

func (tm *TranscoderManager) StopTranscoder(outputMount string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if inst, ok := tm.instances[outputMount]; ok {
		inst.Stop()
		delete(tm.instances, outputMount)
	}
}

func (tm *TranscoderManager) StopAll() {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	for _, inst := range tm.instances {
		inst.Stop()
	}
	tm.instances = make(map[string]*TranscoderInstance)
}

func (inst *TranscoderInstance) Stop() {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	if inst.cancel != nil {
		inst.cancel()
		inst.active = false
	}
}

func (tm *TranscoderManager) runTranscoder(ctx context.Context, inst *TranscoderInstance) {
	logger.L.Infow("Starting transcoder",
		"name", inst.Config.Name,
		"input", inst.Config.InputMount,
		"output", inst.Config.OutputMount,
		"format", inst.Config.Format,
		"bitrate", inst.Config.Bitrate,
	)

	const (
		minBackoff = 5 * time.Second
		maxBackoff = 5 * time.Minute
	)
	backoff := minBackoff

	for {
		select {
		case <-ctx.Done():
			return
		default:
			start := time.Now()
			tm.safePerformTranscode(ctx, inst)
			// Reset backoff if we ran for a meaningful duration (not an immediate failure)
			if time.Since(start) > 30*time.Second {
				backoff = minBackoff
			}
			// Wait before retry with exponential backoff
			logger.L.Infow("Transcoder: retrying", "name", inst.Config.Name, "backoff", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

func (tm *TranscoderManager) safePerformTranscode(ctx context.Context, inst *TranscoderInstance) {
	defer func() {
		if r := recover(); r != nil {
			logger.L.Errorw("Transcoder: recovered from panic, will retry",
				"name", inst.Config.Name,
				"input", inst.Config.InputMount,
				"panic", fmt.Sprintf("%v", r),
				"stack", string(debug.Stack()),
			)
		}
	}()
	tm.performTranscode(ctx, inst)
}

func (tm *TranscoderManager) performTranscode(ctx context.Context, inst *TranscoderInstance) {
	var input *Stream
	var ok bool

	// 1. Wait for input stream to become available
	for {
		input, ok = tm.relay.GetStream(inst.Config.InputMount)
		if ok {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(1 * time.Second):
			// Keep waiting
		}
	}

	logger.L.Infow("Transcoder: Input stream found, initializing...", "name", inst.Config.Name, "input", inst.Config.InputMount)

	// 2. Subscribe to input
	id := fmt.Sprintf("transcoder-%s", inst.Config.Name)
	// We use a small burst to ensure the decoder gets enough data to start
	offset, signal := input.Subscribe(id, 32*1024)
	defer input.Unsubscribe(id)

	reader := NewStreamReader(input.Buffer, offset, signal, ctx, id).WithOggSync(input)

	// 3. Decode — pick decoder based on input content type
	var pcmReader io.Reader
	var sampleRate int

	inputIsOgg := strings.Contains(strings.ToLower(input.ContentType), "ogg") ||
		strings.Contains(strings.ToLower(input.ContentType), "opus")

	if inputIsOgg {
		// Opus input: scan for OggS page boundary before reading (same as webrtc.go)
		syncBuf := make([]byte, 16384)
		foundSync := false
		var searched int64
		for !foundSync && searched < 512*1024 {
			select {
			case <-ctx.Done():
				return
			case <-signal:
				n, next, _ := input.Buffer.ReadAt(offset, syncBuf)
				if n == 0 {
					continue
				}
				for i := 0; i <= n-4; i++ {
					if syncBuf[i] == 'O' && syncBuf[i+1] == 'g' && syncBuf[i+2] == 'g' && syncBuf[i+3] == 'S' {
						offset += int64(i)
						foundSync = true
						break
					}
				}
				if !foundSync {
					offset = next - 3
					searched += int64(n)
				}
			}
		}

		if !foundSync {
			logger.L.Errorw("Transcoder: Could not find Ogg sync", "name", inst.Config.Name, "input", inst.Config.InputMount)
			return
		}

		// Re-create reader at synced offset, then prepend Ogg headers
		reader = NewStreamReader(input.Buffer, offset, signal, ctx, id).WithOggSync(input)
		var finalReader io.Reader = reader
		if input.OggHead != nil {
			finalReader = io.MultiReader(bytes.NewReader(input.OggHead), reader)
		}

		opusReader, err := ogg.NewOpusReader(finalReader)
		if err != nil {
			logger.L.Errorw("Transcoder: Failed to initialize Opus decoder", "name", inst.Config.Name, "input", inst.Config.InputMount, "error", err)
			return
		}

		opusDecoder, err := opus.NewDecoderFromHead(opusReader.Head)
		if err != nil {
			logger.L.Errorw("Transcoder: Failed to create Opus decoder from head", "name", inst.Config.Name, "input", inst.Config.InputMount, "error", err)
			return
		}
		defer opusDecoder.Close()

		sampleRate = opusDecoder.SampleRate()
		pcmReader = newOpusPCMReader(opusReader, opusDecoder, opusDecoder.Channels())
	} else {
		// MP3 input: existing path
		mp3Decoder, err := mp3.NewDecoder(reader)
		if err != nil {
			logger.L.Errorw("Transcoder: Failed to initialize MP3 decoder", "name", inst.Config.Name, "input", inst.Config.InputMount, "error", err)
			return
		}
		sampleRate = mp3Decoder.SampleRate()
		pcmReader = mp3Decoder
	}

	// 4. Create Output Stream
	output := tm.relay.GetOrCreateStream(inst.Config.OutputMount)
	output.Name = fmt.Sprintf("%s (%s %dK)", input.Name, inst.Config.Format, inst.Config.Bitrate)
	output.Bitrate = fmt.Sprintf("%d", inst.Config.Bitrate)
	output.IsTranscoded = true
	output.Visible = tm.relay.GetStreamVisibility(inst.Config.InputMount) // Follow input visibility

	if inst.Config.Format == "mp3" {
		output.ContentType = "audio/mpeg"
		EncodeMP3(ctx, tm.relay, output, pcmReader, inst.Config.Bitrate, &inst.BytesEncoded, false, sampleRate)
	} else if inst.Config.Format == "opus" {
		output.ContentType = "audio/ogg"
		EncodeOpus(ctx, tm.relay, output, pcmReader, inst.Config.Bitrate, &inst.BytesEncoded, false, sampleRate, 2)
	}
}

func EncodeMP3(ctx context.Context, relay *Relay, output *Stream, decoder io.Reader, bitrate int, stats *int64, pace bool, sampleRate int) {
	if sampleRate <= 0 {
		sampleRate = 44100 // Fallback
	}
	// Shine MP3 initialization
	encoder := shine.NewEncoder(sampleRate, 2)
	configureMP3EncoderBitrate(encoder, bitrate)

	// Output buffer - shine Write uses int16 samples
	pcmBuf := make([]byte, 4608) // 1152 samples * 2 bytes * 2 channels
	samples := make([]int16, 2304)

	startTime := time.Now()
	totalSamples := int64(0)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			n, err := io.ReadFull(decoder, pcmBuf)
			if err != nil {
				return
			}

			// Convert PCM bytes to int16 for Shine
			for i := 0; i < n/2; i++ {
				samples[i] = int16(pcmBuf[i*2]) | int16(pcmBuf[i*2+1])<<8
			}

			// Encode and broadcast
			// Shine writes directly to an io.Writer
			// We can wrap our broadcast in an io.Writer
			writer := &streamWriter{stream: output, relay: relay, stats: stats}
			err = encoder.Write(writer, samples[:n/2])
			if err != nil {
				return
			}

			if pace {
				totalSamples += int64(n / 4) // 2 channels, 2 bytes per sample
				elapsed := time.Since(startTime)
				expected := time.Duration(totalSamples) * time.Second / time.Duration(sampleRate)
				if expected > elapsed {
					time.Sleep(expected - elapsed)
				}
			}
		}
	}
}

func EncodeOpus(ctx context.Context, relay *Relay, output *Stream, decoder io.Reader, bitrate int, stats *int64, pace bool, srcSampleRate int, channels int) {
	// 48kHz is standard for Opus
	const sampleRate = 48000
	const frameMS = 20
	const frameSize = sampleRate * frameMS / 1000

	if srcSampleRate <= 0 {
		srcSampleRate = sampleRate
	}
	if channels <= 0 {
		channels = 2
	}

	enc, err := opus.NewEncoder(sampleRate, channels, opus.ApplicationAudio)
	if err != nil {
		logger.L.Errorf("Failed to create Opus encoder: %v", err)
		return
	}
	defer enc.Close()

	if bitrate > 0 {
		enc.SetBitrate(bitrate * 1000)
	}

	// Ogg encapsulation
	writer := &streamWriter{stream: output, relay: relay, stats: stats, capture: true}
	serial := uint32(time.Now().UnixNano())
	pw := ogg.NewPacketWriter(writer, serial)

	// 1. ID Header
	head := ogg.OpusHead{
		Version:         1,
		Channels:        uint8(channels),
		InputSampleRate: uint32(srcSampleRate),
	}
	headPacket, _ := ogg.BuildOpusHeadPacket(head)
	pw.WritePacket(headPacket, 0, true, false)
	pw.Flush()

	// 2. Tags Header
	tags := ogg.OpusTags{Vendor: "tinyice-opus"}
	tagsPacket, _ := ogg.BuildOpusTagsPacket(tags)
	pw.WritePacket(tagsPacket, 0, false, false)
	pw.Flush()

	// Store for mid-stream listeners
	output.OggHead = writer.headerBuf.Bytes()
	output.OggHeaderOffset = output.Buffer.Head
	writer.capture = false // Stop capturing headers

	pcmSamples := make([]int16, frameSize*channels)
	opusPacket := make([]byte, 4000) // Max opus packet size
	frameReader := newPCMFrameReader(decoder, srcSampleRate, sampleRate, channels, frameSize)

	var granulePos uint64
	var sentCount int64 = 0
	startTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		default:
			_, rerr := frameReader.ReadFrame(pcmSamples)
			if rerr != nil {
				return
			}

			en, eerr := enc.Encode(pcmSamples, frameSize, opusPacket)
			if eerr != nil {
				logger.L.Errorf("Opus encode error: %v", eerr)
				return
			}

			granulePos += uint64(frameSize)
			if err := pw.WritePacket(opusPacket[:en], granulePos, false, false); err != nil {
				return
			}
			pw.Flush()

			if pace {
				sentCount++
				elapsed := time.Since(startTime)
				expected := time.Duration(sentCount*frameMS) * time.Millisecond
				if expected > elapsed {
					time.Sleep(expected - elapsed)
				}
			}
		}
	}
}

// opusPCMReader wraps an Ogg/Opus stream and decodes it into interleaved int16 PCM bytes,
// implementing io.Reader so it can be fed directly into the MP3/Opus encoders.
type opusPCMReader struct {
	oggReader *ogg.OpusReader
	decoder   *opus.Decoder
	channels  int
	buf       []byte // leftover PCM bytes from previous decode
}

func newOpusPCMReader(oggReader *ogg.OpusReader, decoder *opus.Decoder, channels int) *opusPCMReader {
	return &opusPCMReader{
		oggReader: oggReader,
		decoder:   decoder,
		channels:  channels,
	}
}

func (r *opusPCMReader) Read(p []byte) (int, error) {
	// Drain buffered PCM first
	if len(r.buf) > 0 {
		n := copy(p, r.buf)
		r.buf = r.buf[n:]
		return n, nil
	}

	// Read and decode the next Opus packet
	pkt, err := r.oggReader.ReadAudioPacket()
	if err != nil {
		return 0, err
	}

	// 5760 = max Opus frame size at 48kHz (120ms)
	pcmSamples := make([]int16, 5760*r.channels)
	samplesPerChannel, err := r.decoder.Decode(pkt.Data, pcmSamples, 5760, false)
	if err != nil {
		return 0, fmt.Errorf("opus decode: %w", err)
	}

	// Convert int16 PCM samples to little-endian bytes
	totalSamples := samplesPerChannel * r.channels
	pcmBytes := make([]byte, totalSamples*2)
	for i := 0; i < totalSamples; i++ {
		binary.LittleEndian.PutUint16(pcmBytes[i*2:], uint16(pcmSamples[i]))
	}

	n := copy(p, pcmBytes)
	if n < len(pcmBytes) {
		r.buf = pcmBytes[n:]
	}
	return n, nil
}

type streamWriter struct {
	stream    *Stream
	relay     *Relay
	stats     *int64
	headerBuf bytes.Buffer
	capture   bool
}

func (w *streamWriter) Write(p []byte) (n int, err error) {
	if w.capture {
		w.headerBuf.Write(p)
	}
	w.stream.Broadcast(p, w.relay)
	if w.stats != nil {
		atomic.AddInt64(w.stats, int64(len(p)))
	}
	return len(p), nil
}

func (tm *TranscoderManager) GetInstance(outputMount string) *TranscoderInstance {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.instances[outputMount]
}

type TranscoderStats struct {
	Name            string `json:"name"`
	Input           string `json:"input"`
	Output          string `json:"output"`
	Format          string `json:"format"`
	Bitrate         int    `json:"bitrate"`
	Active          bool   `json:"active"`
	FramesProcessed int64  `json:"frames"`
	BytesEncoded    int64  `json:"bytes"`
	Uptime          string `json:"uptime"`
}
