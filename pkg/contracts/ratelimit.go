package contracts

import "context"

// RateLimiterProvider is implemented by rate limiter modules (ratelimit-tokenbucket, etc.)
// to provide request rate limiting to the API server middleware chain.
// Core discovers a RateLimiterProvider at bootstrap and injects it into the middleware.
// If no module implements this contract, rate limiting is disabled (open mode).
type RateLimiterProvider interface {
	// Allow checks whether a request from the given key (typically IP) is allowed.
	// Returns true if the request should proceed, false if it should be rate-limited.
	Allow(ctx context.Context, key string) bool

	// Enabled returns whether rate limiting is active.
	Enabled() bool
}
