package health

import (
	"errors"
	"testing"
	"time"
)

func TestHistoryRecordAndSnapshot(t *testing.T) {
	h := NewHistory(2)
	err := errors.New("boom")
	h.Record("mod-a", false, err, 10*time.Millisecond)
	h.Record("mod-a", true, nil, 5*time.Millisecond)
	h.Record("mod-a", true, nil, 2*time.Millisecond)

	snap := h.Snapshot("mod-a")
	if len(snap) != 2 {
		t.Fatalf("capacity trim failed: got %d records", len(snap))
	}
	if !snap[0].Healthy || snap[0].Duration != "5ms" {
		t.Fatalf("oldest retained record = %+v", snap[0])
	}
	if !snap[1].Healthy || snap[1].Duration != "2ms" {
		t.Fatalf("newest record = %+v", snap[1])
	}
}
