package health

import (
	"sync"
	"time"
)

// HealthRecord is a single health check observation for a module.
type HealthRecord struct {
	Timestamp time.Time `json:"timestamp"`
	Healthy   bool      `json:"healthy"`
	Error     string    `json:"error,omitempty"`
	Duration  string    `json:"duration,omitempty"`
}

// History stores rolling health check results per module.
type History struct {
	mu       sync.RWMutex
	capacity int
	records  map[string][]HealthRecord
}

// NewHistory creates a health history store with the given per-module capacity.
func NewHistory(capacity int) *History {
	if capacity <= 0 {
		capacity = 60
	}
	return &History{
		capacity: capacity,
		records:  make(map[string][]HealthRecord),
	}
}

// Record appends a health check result for moduleID.
func (h *History) Record(moduleID string, healthy bool, checkErr error, duration time.Duration) {
	rec := HealthRecord{
		Timestamp: time.Now().UTC(),
		Healthy:   healthy,
		Duration:  duration.Round(time.Millisecond).String(),
	}
	if checkErr != nil {
		rec.Error = checkErr.Error()
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	list := h.records[moduleID]
	list = append(list, rec)
	if len(list) > h.capacity {
		list = list[len(list)-h.capacity:]
	}
	h.records[moduleID] = list
}

// Snapshot returns a copy of recent records for moduleID.
func (h *History) Snapshot(moduleID string) []HealthRecord {
	h.mu.RLock()
	defer h.mu.RUnlock()
	src := h.records[moduleID]
	if len(src) == 0 {
		return nil
	}
	out := make([]HealthRecord, len(src))
	copy(out, src)
	return out
}

// SnapshotAll returns recent records for every tracked module.
func (h *History) SnapshotAll() map[string][]HealthRecord {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make(map[string][]HealthRecord, len(h.records))
	for id, recs := range h.records {
		cp := make([]HealthRecord, len(recs))
		copy(cp, recs)
		out[id] = cp
	}
	return out
}
