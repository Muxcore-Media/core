package module

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// RPCStats holds aggregated RPC metrics for one module.
type RPCStats struct {
	CallCount    int64   `json:"call_count"`
	ErrorCount   int64   `json:"error_count"`
	TotalLatency int64   `json:"total_latency_ns"`
	AvgLatencyMs float64 `json:"avg_latency_ms"`
	ErrorRate    float64 `json:"error_rate"`
}

type moduleRPCStats struct {
	callCount    atomic.Int64
	errorCount   atomic.Int64
	totalLatency atomic.Int64
}

// RPCMetrics tracks per-module gRPC mesh call latency and error rates.
type RPCMetrics struct {
	mu      sync.RWMutex
	modules map[string]*moduleRPCStats
}

// NewRPCMetrics creates an empty RPC metrics collector.
func NewRPCMetrics() *RPCMetrics {
	return &RPCMetrics{modules: make(map[string]*moduleRPCStats)}
}

func (m *RPCMetrics) statsFor(moduleID string) *moduleRPCStats {
	m.mu.RLock()
	s, ok := m.modules[moduleID]
	m.mu.RUnlock()
	if ok {
		return s
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok = m.modules[moduleID]; ok {
		return s
	}
	s = &moduleRPCStats{}
	m.modules[moduleID] = s
	return s
}

// RecordCall records one mesh RPC to targetModule.
func (m *RPCMetrics) RecordCall(targetModule, _ string, duration time.Duration, err error) {
	s := m.statsFor(targetModule)
	s.callCount.Add(1)
	s.totalLatency.Add(duration.Nanoseconds())
	if err != nil {
		s.errorCount.Add(1)
	}
}

// Snapshot returns aggregated stats for all modules.
func (m *RPCMetrics) Snapshot() map[string]RPCStats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]RPCStats, len(m.modules))
	for id, s := range m.modules {
		calls := s.callCount.Load()
		errors := s.errorCount.Load()
		totalNs := s.totalLatency.Load()
		stats := RPCStats{
			CallCount:    calls,
			ErrorCount:   errors,
			TotalLatency: totalNs,
		}
		if calls > 0 {
			stats.AvgLatencyMs = float64(totalNs) / float64(calls) / 1e6
			stats.ErrorRate = float64(errors) / float64(calls)
		}
		out[id] = stats
	}
	return out
}

// SortedModuleIDs returns module IDs sorted alphabetically for stable export.
func (m *RPCMetrics) SortedModuleIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.modules))
	for id := range m.modules {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
