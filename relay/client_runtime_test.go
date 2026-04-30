package relay

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRelayManagerRegistersRelayRuntimeOnPullConnect(t *testing.T) {
	r := NewRelay(false, nil)
	rr := NewRuntimeRegistry(r)
	rm := NewRelayManager(r)
	rm.SetRuntimeRegistry(rr)

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Header().Set("Ice-Name", "Relay Source")
		w.WriteHeader(http.StatusOK)
		for i := 0; i < 10; i++ {
			_, _ = fmt.Fprint(w, "audio")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(10 * time.Millisecond)
		}
	}))
	defer source.Close()

	rm.StartRelay(source.URL, "/relay", "", 0, true)
	defer rm.StopRelay("/relay")

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if rt, ok := rr.Get("/relay"); ok && rt.Source == SourceRelay && rt.Stream == r.GetOrCreateStream("/relay") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("expected relay pull connection to register runtime source")
}
