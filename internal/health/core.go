// Package health provides core self-health checking.
// Each subsystem registers a probe function that returns nil when healthy
// or an error describing what's wrong. The CoreHealth aggregator runs all
// probes concurrently and caches results to avoid thundering-herd on
// health polling.
package health

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Probe is a function that checks the health of a subsystem.
// Returns nil if healthy, or an error describing the issue.
type Probe func(ctx context.Context) error

// CoreHealth aggregates health probes from core subsystems.
// Thread-safe. Results are cached for a configurable TTL.
type CoreHealth struct {
	mu       sync.RWMutex
	probes   map[string]Probe
	cache    map[string]error
	cachedAt time.Time
	ttl      time.Duration
}

// New creates a CoreHealth with the default 5-second cache TTL.
func New() *CoreHealth {
	return &CoreHealth{
		probes: make(map[string]Probe),
		cache:  make(map[string]error),
		ttl:    5 * time.Second,
	}
}

// RegisterProbe adds a named health probe.
// Panics if a probe with the same name is already registered.
func (ch *CoreHealth) RegisterProbe(name string, fn Probe) {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if _, exists := ch.probes[name]; exists {
		slog.Warn("duplicate health probe registration", "name", name)
		return
	}
	ch.probes[name] = fn
}

// Check runs all registered probes concurrently and returns a map of
// probe name → error (nil = healthy). Results are cached for the TTL
// duration; subsequent calls within the TTL return the cached result.
func (ch *CoreHealth) Check(ctx context.Context) map[string]error {
	ch.mu.RLock()
	if time.Since(ch.cachedAt) < ch.ttl && len(ch.cache) > 0 {
		cache := make(map[string]error, len(ch.cache))
		for k, v := range ch.cache {
			cache[k] = v
		}
		ch.mu.RUnlock()
		return cache
	}
	// Take a snapshot of probes under read lock.
	names := make([]string, 0, len(ch.probes))
	probes := make([]Probe, 0, len(ch.probes))
	for name, probe := range ch.probes {
		names = append(names, name)
		probes = append(probes, probe)
	}
	ch.mu.RUnlock()

	// Run all probes concurrently.
	type result struct {
		name string
		err  error
	}

	results := make(chan result, len(probes))
	for i, probe := range probes {
		go func(name string, p Probe) {
			probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			results <- result{name: name, err: p(probeCtx)}
		}(names[i], probe)
	}

	out := make(map[string]error, len(probes))
	for range probes {
		r := <-results
		out[r.name] = r.err
	}

	// Cache results.
	ch.mu.Lock()
	ch.cache = out
	ch.cachedAt = time.Now()
	ch.mu.Unlock()

	return out
}

// Healthy reports whether all probes pass (no errors).
func (ch *CoreHealth) Healthy(ctx context.Context) bool {
	for _, err := range ch.Check(ctx) {
		if err != nil {
			return false
		}
	}
	return true
}

// ProbeCount returns the number of registered probes.
func (ch *CoreHealth) ProbeCount() int {
	ch.mu.RLock()
	defer ch.mu.RUnlock()
	return len(ch.probes)
}
