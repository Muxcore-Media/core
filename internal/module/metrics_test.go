package module

import (
	"errors"
	"testing"
	"time"
)

func TestRPCMetricsSnapshot(t *testing.T) {
	m := NewRPCMetrics()
	m.RecordCall("auth-local", "validate", 20*time.Millisecond, nil)
	m.RecordCall("auth-local", "validate", 30*time.Millisecond, errors.New("fail"))

	stats := m.Snapshot()["auth-local"]
	if stats.CallCount != 2 {
		t.Fatalf("calls = %d", stats.CallCount)
	}
	if stats.ErrorCount != 1 {
		t.Fatalf("errors = %d", stats.ErrorCount)
	}
	if stats.AvgLatencyMs != 25 {
		t.Fatalf("avg latency = %f", stats.AvgLatencyMs)
	}
	if stats.ErrorRate != 0.5 {
		t.Fatalf("error rate = %f", stats.ErrorRate)
	}
}
