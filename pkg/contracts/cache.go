package contracts

import (
	"context"
	"time"
)

// CacheProvider is implemented by cache modules (cache-redis, cache-valkey, etc.)
// to provide ephemeral state, distributed locks, and pub/sub. Core defines the contract;
// modules provide the driver.
type CacheProvider interface {
	// Get retrieves a value by key. Returns nil, nil if the key does not exist.
	Get(ctx context.Context, key string) ([]byte, error)
	// Set stores a value with an optional TTL. Zero TTL means no expiration.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// Delete removes one or more keys.
	Delete(ctx context.Context, keys ...string) error
	// Exists checks whether a key exists.
	Exists(ctx context.Context, key string) (bool, error)
	// Incr atomically increments an integer key by delta and returns the new value.
	Incr(ctx context.Context, key string, delta int64) (int64, error)
	// Lock acquires a distributed lock on a key. Returns a Lock handle.
	// If the key is already locked, returns an error immediately (non-blocking).
	Lock(ctx context.Context, key string, ttl time.Duration) (LockHandle, error)
	// Publish sends a message on a pub/sub channel.
	Publish(ctx context.Context, channel string, msg []byte) error
	// Subscribe returns a channel that receives messages published on the given channel.
	Subscribe(ctx context.Context, channel string) (<-chan []byte, error)
}

// LockHandle represents an acquired distributed lock.
type LockHandle interface {
	// Unlock releases the lock.
	Unlock(ctx context.Context) error
}
