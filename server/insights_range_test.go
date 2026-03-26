package server

import (
	"testing"
	"time"
)

func TestParseInsightsDurationDefaultsTo24Hours(t *testing.T) {
	duration, err := parseInsightsDuration("")
	if err != nil {
		t.Fatalf("expected empty range to default, got error: %v", err)
	}
	if duration != 24*time.Hour {
		t.Fatalf("expected default 24h duration, got %s", duration)
	}
}

func TestParseInsightsDurationSupportsDashboardRanges(t *testing.T) {
	tests := map[string]time.Duration{
		"1H": 1 * time.Hour,
		"24H": 24 * time.Hour,
		"7D": 7 * 24 * time.Hour,
	}

	for input, expected := range tests {
		duration, err := parseInsightsDuration(input)
		if err != nil {
			t.Fatalf("expected %s to parse, got error: %v", input, err)
		}
		if duration != expected {
			t.Fatalf("expected %s to map to %s, got %s", input, expected, duration)
		}
	}
}

func TestParseInsightsDurationRejectsUnknownRanges(t *testing.T) {
	if _, err := parseInsightsDuration("2H"); err == nil {
		t.Fatal("expected invalid range to return an error")
	}
}
