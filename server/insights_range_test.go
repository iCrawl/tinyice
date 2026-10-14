package server

import (
	"testing"
	"time"

	"github.com/DatanoiseTV/tinyice/relay"
)

func TestDownsampleHistoricalPassesThroughSmallSeries(t *testing.T) {
	series := []relay.HistoricalStat{
		{Timestamp: time.Unix(1, 0), Listeners: 2, BytesIn: 10, BytesOut: 20},
		{Timestamp: time.Unix(2, 0), Listeners: 4, BytesIn: 30, BytesOut: 40},
	}

	got := downsampleHistorical(series, 10)

	if len(got) != len(series) {
		t.Fatalf("expected pass-through series length %d, got %d", len(series), len(got))
	}
	if got[0] != series[0] || got[1] != series[1] {
		t.Fatalf("expected pass-through series, got %#v", got)
	}
}

func TestDownsampleHistoricalBucketsListenersAndTraffic(t *testing.T) {
	series := []relay.HistoricalStat{
		{Timestamp: time.Unix(1, 0), Listeners: 2, BytesIn: 10, BytesOut: 100},
		{Timestamp: time.Unix(2, 0), Listeners: 4, BytesIn: 20, BytesOut: 200},
		{Timestamp: time.Unix(3, 0), Listeners: 6, BytesIn: 30, BytesOut: 300},
		{Timestamp: time.Unix(4, 0), Listeners: 8, BytesIn: 40, BytesOut: 400},
	}

	got := downsampleHistorical(series, 2)

	if len(got) != 2 {
		t.Fatalf("expected 2 buckets, got %d", len(got))
	}
	if got[0].Listeners != 3 || got[1].Listeners != 7 {
		t.Fatalf("expected averaged listeners [3 7], got [%d %d]", got[0].Listeners, got[1].Listeners)
	}
	if got[0].BytesIn != 30 || got[0].BytesOut != 300 {
		t.Fatalf("unexpected first bucket traffic: in=%d out=%d", got[0].BytesIn, got[0].BytesOut)
	}
	if got[1].BytesIn != 70 || got[1].BytesOut != 700 {
		t.Fatalf("unexpected second bucket traffic: in=%d out=%d", got[1].BytesIn, got[1].BytesOut)
	}
}
