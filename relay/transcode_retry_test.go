package relay

import (
	"testing"
	"time"
)

func TestNextTranscoderRetryBackoffDoublesUntilMaximum(t *testing.T) {
	backoff := 5 * time.Second

	if got := nextTranscoderRetryBackoff(&backoff); got != 5*time.Second {
		t.Fatalf("first retry = %v, want 5s", got)
	}
	if got := nextTranscoderRetryBackoff(&backoff); got != 10*time.Second {
		t.Fatalf("second retry = %v, want 10s", got)
	}

	backoff = 4 * time.Minute
	if got := nextTranscoderRetryBackoff(&backoff); got != 4*time.Minute {
		t.Fatalf("near max retry = %v, want 4m", got)
	}
	if got := nextTranscoderRetryBackoff(&backoff); got != 5*time.Minute {
		t.Fatalf("capped retry = %v, want 5m", got)
	}
	if got := nextTranscoderRetryBackoff(&backoff); got != 5*time.Minute {
		t.Fatalf("retry should stay capped, got %v", got)
	}
}
