package storage

import (
	"context"
	"strings"
	"sync"
)

// MemoryCache is a simple in-memory implementation of CacheLayer.
type MemoryCache struct {
	mu   sync.RWMutex
	data map[string][]byte
}

// NewMemoryCache creates a new empty MemoryCache.
func NewMemoryCache() *MemoryCache {
	return &MemoryCache{data: make(map[string][]byte)}
}

// Get returns cached data for the given key.
func (c *MemoryCache) Get(ctx context.Context, key string) ([]byte, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.data[key]
	return v, ok
}

// Set caches data for the given key.
func (c *MemoryCache) Set(ctx context.Context, key string, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] = data
	return nil
}

// Invalidate removes all cached entries whose key has the given prefix.
func (c *MemoryCache) Invalidate(ctx context.Context, prefix string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.data {
		if strings.HasPrefix(k, prefix) {
			delete(c.data, k)
		}
	}
	return nil
}
