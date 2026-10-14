//go:build !race

package relay

import (
	"errors"
	"testing"

	"github.com/kazzmir/opus-go/ogg"
)

type removeBeforeAudioPage struct {
	relay  *Relay
	stream *Stream
	token  int64
}

func (w *removeBeforeAudioPage) Write(data []byte) (int, error) {
	w.relay.RemoveStream(w.stream.MountName)
	return (&streamWriter{stream: w.stream, relay: w.relay, token: w.token}).Write(data)
}

func TestOpusFrameRejectsRemovedSourceAtPageFlush(t *testing.T) {
	r := NewRelay(false, nil)
	stream := r.GetOrCreateStream("/opus-publication")
	token, claimed := stream.ClaimSource("autodj")
	if !claimed {
		t.Fatal("source claim failed")
	}
	encoder, err := NewOpusEncoderSession(stream, r, 128, 48000, 2, nil, token)
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	encoder.packetWriter = ogg.NewPacketWriter(&removeBeforeAudioPage{relay: r, stream: stream, token: token}, 12)
	err = encoder.WriteFrame(make([]int16, 1920))
	if !errors.Is(err, ErrSourceOwnershipLost) {
		t.Fatalf("removed source audio page reported success: %v", err)
	}
	if _, exists := r.GetStream(stream.MountName); exists {
		t.Fatal("removed source reappeared after rejected audio publication")
	}
}
