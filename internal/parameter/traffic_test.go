package parameter

import (
	"testing"
	"time"
)

func TestTrafficSamplesToRatePoints(t *testing.T) {
	t0 := time.Date(2026, 4, 24, 10, 0, 0, 0, time.UTC)
	samples := []TrafficSample{
		{RecordedAt: t0, BytesSent: 1000, BytesReceived: 5000},
		{RecordedAt: t0.Add(30 * time.Second), BytesSent: 4000, BytesReceived: 11000},
	}
	points := TrafficSamplesToRatePoints(samples)
	if len(points) != 1 {
		t.Fatalf("len=%d", len(points))
	}
	// Δtx = 3000 B / 30 s → 800 bps; Δrx = 6000 B / 30 s → 1600 bps
	if !points[0].Valid {
		t.Fatal("expected valid")
	}
	if got := points[0].TxBps; got < 799 || got > 801 {
		t.Errorf("TxBps=%v want ~800", got)
	}
	if got := points[0].RxBps; got < 1599 || got > 1601 {
		t.Errorf("RxBps=%v want ~1600", got)
	}
}

func TestTrafficSamplesToRatePointsCounterReset(t *testing.T) {
	t0 := time.Date(2026, 4, 24, 10, 0, 0, 0, time.UTC)
	samples := []TrafficSample{
		{RecordedAt: t0, BytesSent: 10000, BytesReceived: 20000},
		{RecordedAt: t0.Add(30 * time.Second), BytesSent: 100, BytesReceived: 50},
	}
	points := TrafficSamplesToRatePoints(samples)
	if len(points) != 1 || points[0].Valid {
		t.Fatalf("expected invalid segment, got %+v", points)
	}
	if points[0].TxBps != 0 || points[0].RxBps != 0 {
		t.Fatalf("expected zero bps on reset")
	}
}
