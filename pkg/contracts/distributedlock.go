package contracts

import (
	"context"
	"errors"
	"time"
)

// ErrLockHeld is returned by DistributedLockProvider.Acquire when the
// requested key is already locked by another client.
var ErrLockHeld = errors.New("distributed lock: key is already held")

// DistributedLockHandle represents an acquired distributed lock.
// Callers must call Unlock when done, or Renew to extend the lease
// for long-running operations.
type DistributedLockHandle interface {
	// Unlock releases the lock. Safe to call multiple times.
	Unlock(ctx context.Context) error

	// Renew extends the lock's TTL without releasing and re-acquiring.
	// Useful for long-running operations where the initial TTL would expire
	// before completion. Returns ErrLockHeld if the lock was lost (e.g.,
	// network partition caused the backend to expire it).
	Renew(ctx context.Context, ttl time.Duration) error
}

// DistributedLockProvider provides standalone distributed locking.
// This is distinct from CacheProvider.Lock which ties locking to a cache
// instance. Modules that need coordination (leader election, singleton
// workers, migration gating, split-brain fencing) use this contract
// without needing to stand up a cache.
//
// This contract is provider-agnostic: a module that calls Acquire works
// identically whether the backend is Redis, etcd, Consul, Postgres
// advisory locks, or a future coordination service.
//
// Acquire is non-blocking by design. If the lock is held, it returns
// ErrLockHeld immediately. Callers that want blocking behavior compose
// this with RetryProvider.Execute.
//
// If no DistributedLockProvider is registered, Acquire always succeeds
// (single-node mode) — modules degrade gracefully without nil checks.
type DistributedLockProvider interface {
	// Acquire attempts to acquire a lock for the given key with a TTL.
	// Returns (nil, ErrLockHeld) if the key is already locked.
	// Returns (nil, ctx.Err()) if the context is cancelled before acquisition.
	// The returned handle must be Unlock()ed when the critical section completes.
	Acquire(ctx context.Context, key string, ttl time.Duration) (DistributedLockHandle, error)
}
