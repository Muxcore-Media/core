//nolint:govet // struct field alignment
package grpcmesh

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"

	cryptorand "crypto/rand"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// RoundRobinStrategy distributes cross-node module calls across available
// candidates in sequential rotation. Safe for concurrent use.
type RoundRobinStrategy struct {
	counter atomic.Uint64
}

// NewRoundRobinStrategy creates a round-robin load balancing strategy.
func NewRoundRobinStrategy() *RoundRobinStrategy {
	return &RoundRobinStrategy{}
}

// Pick selects the next candidate in round-robin order.
func (s *RoundRobinStrategy) Pick(_ context.Context, candidates []contracts.NodeInfo) (int, error) {
	if len(candidates) == 0 {
		return 0, fmt.Errorf("round-robin: %w", contracts.ErrNoCandidates)
	}
	idx := s.counter.Add(1) % uint64(len(candidates))
	return int(idx), nil //nolint:gosec // len(candidates) is always small
}

// RandomStrategy picks a random candidate on each call.
type RandomStrategy struct {
	mu sync.Mutex
	r  *rand.Rand
}

func NewRandomStrategy() *RandomStrategy {
	var seed int64
	if err := binary.Read(cryptorand.Reader, binary.LittleEndian, &seed); err != nil {
		seed = 42 // fallback; should never happen on a healthy system
	}
	return &RandomStrategy{
		r: rand.New(rand.NewSource(seed)), //nolint:gosec // non-crypto use for load balancing randomization
	}
}

func (s *RandomStrategy) Pick(_ context.Context, candidates []contracts.NodeInfo) (int, error) {
	if len(candidates) == 0 {
		return 0, fmt.Errorf("random: %w", contracts.ErrNoCandidates)
	}
	s.mu.Lock()
	idx := s.r.Intn(len(candidates))
	s.mu.Unlock()
	return idx, nil
}

// LeastLoadedStrategy picks the node with the fewest active in-flight calls.
// Requires that SetCallTracker is used to feed it call start/completion events.
type LeastLoadedStrategy struct {
	mu       sync.Mutex
	inflight map[string]int64
}

func NewLeastLoadedStrategy() *LeastLoadedStrategy {
	return &LeastLoadedStrategy{
		inflight: make(map[string]int64),
	}
}

func (s *LeastLoadedStrategy) TrackStart(nodeID string) {
	s.mu.Lock()
	s.inflight[nodeID]++
	s.mu.Unlock()
}

func (s *LeastLoadedStrategy) TrackEnd(nodeID string) {
	s.mu.Lock()
	if s.inflight[nodeID] > 0 {
		s.inflight[nodeID]--
	}
	s.mu.Unlock()
}

func (s *LeastLoadedStrategy) Pick(_ context.Context, candidates []contracts.NodeInfo) (int, error) {
	if len(candidates) == 0 {
		return 0, fmt.Errorf("least-loaded: %w", contracts.ErrNoCandidates)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	bestIdx := 0
	bestLoad := int64(1<<63 - 1) // max int64
	for i, c := range candidates {
		load := s.inflight[c.ID]
		if load < bestLoad {
			bestLoad = load
			bestIdx = i
		}
	}
	return bestIdx, nil
}

// HealthyCandidates filters candidates to those where the target module's
// health status is empty (healthy). When no candidates are healthy, returns
// the full list as a best-effort fallback.
func HealthyCandidates(candidates []contracts.NodeInfo, targetModule string) []contracts.NodeInfo {
	var healthy []contracts.NodeInfo
	for _, c := range candidates {
		if status, ok := c.ModuleHealth[targetModule]; !ok || status == "" {
			healthy = append(healthy, c)
		}
	}
	if len(healthy) == 0 {
		return candidates // fallback: all candidates are degraded
	}
	return healthy
}

// compile-time interface checks
var (
	_ contracts.LBStrategy = (*RoundRobinStrategy)(nil)
	_ contracts.LBStrategy = (*RandomStrategy)(nil)
	_ contracts.LBStrategy = (*LeastLoadedStrategy)(nil)
)
