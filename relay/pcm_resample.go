package relay

import (
	"encoding/binary"
	"io"
)

// pcmFrameReader turns interleaved int16 PCM bytes from an arbitrary source
// sample rate into fixed-size frames at a target sample rate.
type pcmFrameReader struct {
	reader          io.Reader
	srcSampleRate   int
	dstSampleRate   int
	channels        int
	samplesPerFrame int

	srcPosNum    int64
	bufferStart  int64
	samples      []int16
	pendingBytes []byte
	eof          bool
	readErr      error
}

func newPCMFrameReader(reader io.Reader, srcSampleRate, dstSampleRate, channels, samplesPerFrame int) *pcmFrameReader {
	return &pcmFrameReader{
		reader:          reader,
		srcSampleRate:   srcSampleRate,
		dstSampleRate:   dstSampleRate,
		channels:        channels,
		samplesPerFrame: samplesPerFrame,
	}
}

func (r *pcmFrameReader) ReadFrame(dst []int16) (int, error) {
	if len(dst) < r.samplesPerFrame*r.channels {
		return 0, io.ErrShortBuffer
	}

	for i := 0; i < r.samplesPerFrame; i++ {
		left := r.srcPosNum / int64(r.dstSampleRate)
		fracNum := r.srcPosNum % int64(r.dstSampleRate)

		if !r.ensureFrame(left) {
			if r.readErr != nil {
				return 0, r.readErr
			}
			return 0, io.EOF
		}
		right := left + 1
		if !r.ensureFrame(right) {
			right = left
		}

		for ch := 0; ch < r.channels; ch++ {
			leftSample := int64(r.sampleAt(left, ch))
			rightSample := int64(r.sampleAt(right, ch))
			if fracNum == 0 || right == left {
				dst[i*r.channels+ch] = int16(leftSample)
				continue
			}
			dst[i*r.channels+ch] = int16((leftSample*int64(r.dstSampleRate-int(fracNum)) + rightSample*fracNum) / int64(r.dstSampleRate))
		}

		r.srcPosNum += int64(r.srcSampleRate)
	}

	r.trimBuffer()
	return r.samplesPerFrame * r.channels, nil
}

func (r *pcmFrameReader) sampleAt(frameIndex int64, channel int) int16 {
	relative := frameIndex - r.bufferStart
	return r.samples[int(relative)*r.channels+channel]
}

func (r *pcmFrameReader) trimBuffer() {
	minKeep := r.srcPosNum/int64(r.dstSampleRate) - 1
	if minKeep <= r.bufferStart {
		return
	}

	dropFrames := minKeep - r.bufferStart
	keepOffset := int(dropFrames) * r.channels
	if keepOffset >= len(r.samples) {
		r.samples = nil
		r.bufferStart = minKeep
		return
	}

	r.samples = r.samples[keepOffset:]
	r.bufferStart = minKeep
}

func (r *pcmFrameReader) ensureFrame(frameIndex int64) bool {
	for {
		available := int64(len(r.samples) / r.channels)
		if frameIndex < r.bufferStart+available {
			return true
		}
		if r.eof {
			return frameIndex < r.bufferStart+available
		}
		if !r.readMore() {
			available = int64(len(r.samples) / r.channels)
			return frameIndex < r.bufferStart+available
		}
	}
}

func (r *pcmFrameReader) readMore() bool {
	tmp := make([]byte, r.channels*2*2048)
	n, err := r.reader.Read(tmp)
	if n > 0 {
		raw := append(r.pendingBytes, tmp[:n]...)
		completeBytes := len(raw) / 2 * 2
		if completeBytes > 0 {
			samples := make([]int16, completeBytes/2)
			for i := range samples {
				samples[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
			}
			r.samples = append(r.samples, samples...)
		}
		r.pendingBytes = append(r.pendingBytes[:0], raw[completeBytes:]...)
	}
	if err == io.EOF {
		r.eof = true
	} else if err != nil {
		r.readErr = err
		r.eof = true
	}
	return n > 0
}
