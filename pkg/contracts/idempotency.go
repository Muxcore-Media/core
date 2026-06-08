package contracts

import (
	"context"
	"time"
)

// IdempotencyProvider prevents duplicate processing of requests.
// When a module receives a request that must not be processed more
// than once (e.g., payment capture, order creation), it generates
// an idempotency key and checks this provider before acting.
//
// This contract is provider-agnostic: a module that calls Store/Exists
// works identically whether the backend is in-memory, Redis, a database
// table, or a future storage mechanism.
//
// If no IdempotencyProvider is registered, Store always returns true
// (treated as first occurrence) and Exists always returns false —
// modules degrade gracefully without needing nil checks.
//
// This contract pairs with RetryProvider. Retries may deliver the
// same logical request multiple times; IdempotencyProvider ensures
// the side effects only happen once.
type IdempotencyProvider interface {
	// Store records that a key has been processed and caches the result.
	// Returns (true, nil) if the key was new — this is the first occurrence.
	// Returns (false, nil) if the key was already stored — this is a duplicate.
	// The result is cached for ttl; after expiry the key may be evicted.
	Store(ctx context.Context, key string, result []byte, ttl time.Duration) (bool, error)

	// Exists checks whether a key has been previously stored and not yet expired.
	// Returns true if the key exists, false otherwise.
	Exists(ctx context.Context, key string) (bool, error)

	// Result retrieves the cached result for a previously stored key.
	// Returns (nil, nil) if the key does not exist or has expired.
	// This allows a duplicate request to receive the same response
	// as the original without re-executing the operation.
	Result(ctx context.Context, key string) ([]byte, error)
}
