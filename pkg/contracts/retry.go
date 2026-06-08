package contracts

import (
	"context"
	"time"
)

// RetryPolicy configures how a RetryProvider retries failed operations.
// Zero values mean "use the provider's default."
type RetryPolicy struct {
	// MaxAttempts is the maximum number of attempts, including the first.
	// A value of 0 means the provider's default (typically 3).
	MaxAttempts int

	// InitialDelay is the delay before the first retry.
	// A value of 0 means the provider's default (typically 100ms).
	InitialDelay time.Duration

	// MaxDelay is the maximum delay between retries. The backoff caps at this.
	// A value of 0 means no cap (provider default).
	MaxDelay time.Duration

	// BackoffFactor is multiplied against the delay on each retry.
	// Typical values: 2.0 (exponential), 1.0 (fixed delay).
	// A value of 0 means the provider's default (typically 2.0).
	BackoffFactor float64

	// Jitter, when true, adds random jitter to the delay to avoid thundering herd.
	Jitter bool

	// Extra carries provider-specific extensions.
	Extra map[string]any
}

// RetryProvider executes operations with configurable retry semantics.
// This contract is provider-agnostic: a module that wraps a call in
// RetryProvider.Execute works identically whether the backend uses
// simple backoff, exponential backoff with jitter, or a future retry
// strategy.
//
// RetryProvider is complementary to CircuitBreaker. CircuitBreaker
// prevents cascading failures by opening the circuit when errors
// exceed a threshold. RetryProvider handles transient failures
// (network blips, temporary unavailability) by retrying individual
// calls with backoff. A robust module uses both.
//
// If no RetryProvider is registered, Execute runs fn exactly once
// with no retries — modules don't need nil checks.
type RetryProvider interface {
	// Execute runs fn with retry semantics based on the given policy.
	// Returns nil if fn succeeds on any attempt. Returns the last error
	// if all attempts are exhausted. The context is passed to each attempt;
	// if ctx is cancelled, retries stop immediately.
	Execute(ctx context.Context, policy RetryPolicy, fn func(ctx context.Context) error) error
}
