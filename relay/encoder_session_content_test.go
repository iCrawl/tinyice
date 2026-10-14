package relay

import (
	"bytes"
	"io"
	"math"
	"testing"

	gomp3 "github.com/hajimehoshi/go-mp3"
)

func TestPersistentMP3PreservesRealTone(t *testing.T) {
	r := NewRelay(false, nil)
	out := r.GetOrCreateStream("/audit-session")
	offset, _ := out.Subscribe("probe", 0)
	s, err := NewMP3EncoderSession(out, r, 128, 44100, nil, claimTestEncoder(t, out))
	if err != nil {
		t.Fatal(err)
	}
	for start := 0; start < 88200; start += 1152 {
		frame := make([]int16, 2304)
		for j := 0; j < 1152; j++ {
			v := int16(12000 * math.Sin(2*math.Pi*440*float64(start+j)/44100))
			frame[j*2], frame[j*2+1] = v, v
		}
		if err := s.WriteFrame(frame); err != nil {
			t.Fatal(err)
		}
	}
	var encoded []byte
	buf := make([]byte, 65536)
	for {
		n, next, _ := out.Buffer.ReadAt(offset, buf)
		if n == 0 {
			break
		}
		encoded = append(encoded, buf[:n]...)
		offset = next
	}
	dec, err := gomp3.NewDecoder(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(dec)
	if err != nil {
		t.Fatal(err)
	}
	start, end := 8192*4, len(raw)-8192*4
	if end-start < 44100*4 {
		t.Fatalf("decoded only %d bytes", len(raw))
	}
	for i := start; i+64*4 <= end; i += 64 * 4 {
		peak := 0
		for j := 0; j < 64; j++ {
			v := int(int16(uint16(raw[i+j*4]) | uint16(raw[i+j*4+1])<<8))
			if v < 0 {
				v = -v
			}
			if v > peak {
				peak = v
			}
		}
		if peak < 3000 {
			t.Fatalf("decoded audio drops out at byte %d (window peak %d, want >=3000)", i, peak)
		}
	}
}

func claimTestEncoder(t *testing.T, stream *Stream) int64 {
	t.Helper()
	token, ok := stream.ClaimSource("test encoder")
	if !ok {
		t.Fatal("fixture producer claim failed")
	}
	t.Cleanup(func() { stream.ReleaseSourceIfCurrent(token) })
	return token
}
