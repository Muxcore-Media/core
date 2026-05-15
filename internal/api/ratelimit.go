package api

import (
	"net/http"
	"sync"
	"time"
)

// RateLimiter implements a per-IP token bucket rate limiter.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
	rate    int           // requests per window
	window  time.Duration // time window
	enabled bool
}

type tokenBucket struct {
	tokens   int
	lastFill time.Time
}

// NewRateLimiter creates a rate limiter allowing `rate` requests per `window`.
func NewRateLimiter(rate int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		buckets: make(map[string]*tokenBucket),
		rate:    rate,
		window:  window,
		enabled: true,
	}
}

// Enabled returns whether rate limiting is active.
func (rl *RateLimiter) Enabled() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return rl.enabled
}

// SetEnabled enables or disables rate limiting.
func (rl *RateLimiter) SetEnabled(enabled bool) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.enabled = enabled
}

// Allow checks whether a request from the given key (typically IP) is allowed.
func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if !rl.enabled {
		return true
	}

	now := time.Now()
	bucket, exists := rl.buckets[key]
	if !exists {
		bucket = &tokenBucket{tokens: rl.rate - 1, lastFill: now}
		rl.buckets[key] = bucket
		return true
	}

	// Refill tokens based on elapsed time
	elapsed := now.Sub(bucket.lastFill)
	refill := int(elapsed / rl.window * time.Duration(rl.rate))
	if refill > 0 {
		bucket.tokens += refill
		if bucket.tokens > rl.rate {
			bucket.tokens = rl.rate
		}
		bucket.lastFill = now
	}

	if bucket.tokens > 0 {
		bucket.tokens--
		return true
	}
	return false
}

// Cleanup removes expired buckets. Call periodically.
func (rl *RateLimiter) Cleanup(maxAge time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	cutoff := time.Now().Add(-maxAge)
	for k, b := range rl.buckets {
		if b.lastFill.Before(cutoff) {
			delete(rl.buckets, k)
		}
	}
}

// extractClientIP extracts the client IP from a request.
func extractClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	// Strip port from RemoteAddr
	host := r.RemoteAddr
	for i := len(host) - 1; i >= 0; i-- {
		if host[i] == ':' {
			return host[:i]
		}
	}
	return host
}

// rateLimitMiddleware returns a middleware that rate-limits requests by IP.
func rateLimitMiddleware(limiter *RateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/health" {
				next.ServeHTTP(w, r)
				return
			}
			ip := extractClientIP(r)
			if !limiter.Allow(ip) {
				w.Header().Set("Retry-After", "60")
				writeJSON(w, http.StatusTooManyRequests, map[string]string{
					"error":   "rate_limited",
					"message": "too many requests, try again later",
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
