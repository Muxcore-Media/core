//nolint:govet // struct field alignment
package grpcmesh

import (
	"sync"
	"time"
)

const (
	defaultCBThreshold = 5
	defaultCBCooldown  = 30 * time.Second
	defaultCBReset     = 60 * time.Second
)

type cbState int

const (
	cbClosed   cbState = iota // normal operation
	cbOpen                    // rejecting calls
	cbHalfOpen                // testing if recovered
)

type nodeBreaker struct {
	mu        sync.Mutex
	state     cbState
	failures  int
	threshold int
	cooldown  time.Duration
	reset     time.Duration
	lastError time.Time
	openedAt  time.Time
}

func newNodeBreaker() *nodeBreaker {
	return &nodeBreaker{
		threshold: defaultCBThreshold,
		cooldown:  defaultCBCooldown,
		reset:     defaultCBReset,
	}
}

func (b *nodeBreaker) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case cbClosed:
		return true
	case cbOpen:
		if time.Since(b.openedAt) > b.reset {
			b.state = cbHalfOpen
			return true
		}
		return false
	case cbHalfOpen:
		return true
	default:
		return true
	}
}

func (b *nodeBreaker) recordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.failures++
	b.lastError = time.Now()

	switch b.state {
	case cbClosed:
		if b.failures >= b.threshold {
			b.state = cbOpen
			b.openedAt = time.Now()
		}
	case cbHalfOpen:
		b.state = cbOpen
		b.openedAt = time.Now()
	}
}

func (b *nodeBreaker) recordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.failures = 0

	if b.state == cbHalfOpen {
		b.state = cbClosed
	}
}

// CircuitBreakerSet tracks per-node circuit breakers.
type CircuitBreakerSet struct {
	mu       sync.Mutex
	breakers map[string]*nodeBreaker
}

func NewCircuitBreakerSet() *CircuitBreakerSet {
	return &CircuitBreakerSet{
		breakers: make(map[string]*nodeBreaker),
	}
}

func (s *CircuitBreakerSet) Allow(nodeID string) bool {
	s.mu.Lock()
	b, ok := s.breakers[nodeID]
	if !ok {
		b = newNodeBreaker()
		s.breakers[nodeID] = b
	}
	s.mu.Unlock()
	return b.allow()
}

func (s *CircuitBreakerSet) RecordFailure(nodeID string) {
	s.mu.Lock()
	b, ok := s.breakers[nodeID]
	if !ok {
		b = newNodeBreaker()
		s.breakers[nodeID] = b
	}
	s.mu.Unlock()
	b.recordFailure()
}

func (s *CircuitBreakerSet) RecordSuccess(nodeID string) {
	s.mu.Lock()
	b, ok := s.breakers[nodeID]
	if !ok {
		b = newNodeBreaker()
		s.breakers[nodeID] = b
	}
	s.mu.Unlock()
	b.recordSuccess()
}
