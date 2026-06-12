package api

import (
	"context"
	"sync"
	"time"
)

// DefaultRateLimiter is a simple per-IP token-bucket rate limiter used when no
// module-based RateLimiterProvider is registered. It implements the
// contracts.RateLimiterProvider interface so module-provided limiters
// seamlessly replace it at bootstrap.
//
// Configuration:
//   - Rate: tokens added per second (default 100)
//   - Burst: maximum accumulated tokens (default 200)
type DefaultRateLimiter struct {
	mu      sync.Mutex
	rate    float64
	burst   int
	buckets map[string]*tokenBucket
}

type tokenBucket struct {
	tokens    float64
	lastCheck time.Time
}

// NewDefaultRateLimiter creates a rate limiter with the given rate and burst.
func NewDefaultRateLimiter(rate float64, burst int) *DefaultRateLimiter {
	if rate <= 0 {
		rate = 100
	}
	if burst <= 0 {
		burst = 200
	}
	return &DefaultRateLimiter{
		rate:    rate,
		burst:   burst,
		buckets: make(map[string]*tokenBucket),
	}
}

// Allow checks whether a request from the given key (IP) is allowed.
func (rl *DefaultRateLimiter) Allow(ctx context.Context, key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	b, ok := rl.buckets[key]
	now := time.Now()
	if !ok {
		b = &tokenBucket{tokens: float64(rl.burst), lastCheck: now}
		rl.buckets[key] = b
	}

	// Refill tokens based on elapsed time.
	elapsed := now.Sub(b.lastCheck).Seconds()
	b.tokens += elapsed * rl.rate
	if b.tokens > float64(rl.burst) {
		b.tokens = float64(rl.burst)
	}
	b.lastCheck = now

	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// Enabled returns true — the default rate limiter is always active.
func (rl *DefaultRateLimiter) Enabled() bool {
	return true
}
