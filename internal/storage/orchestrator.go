package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sort"
	"sync"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// Orchestrator routes storage operations to registered providers based on
// capability negotiation and user-defined policies.
type Orchestrator struct {
	mu        sync.RWMutex
	registry  contracts.Registry
	providers map[string]contracts.StorageProvider
	policies  []RoutingPolicy
	cache     contracts.CacheLayer
}

// RoutingPolicy decides which provider handles a given key.
type RoutingPolicy struct {
	Name     string
	Prefix   string
	Provider string
}

// NewOrchestrator creates an Orchestrator that uses the given registry for
// provider discovery.
func NewOrchestrator(reg contracts.Registry) *Orchestrator {
	return &Orchestrator{
		registry:  reg,
		providers: make(map[string]contracts.StorageProvider),
	}
}

// DiscoverStorage finds all registered storage modules and adds them to the pool.
func (o *Orchestrator) DiscoverStorage() error {
	entries := o.registry.FindByRole("storage")
	for _, entry := range entries {
		provider, ok := entry.Module.(contracts.StorageProvider)
		if !ok {
			continue
		}
		o.mu.Lock()
		o.providers[entry.Info.ID] = provider
		o.mu.Unlock()
	}
	return nil
}

// DiscoverCache finds a cache module from the registry and sets it as the read-through cache.
// If no cache module is registered, the orchestrator operates without a cache.
func (o *Orchestrator) DiscoverCache() {
	// Check for dedicated cache module by capability
	entries := o.registry.FindByCapability("cache.local")
	for _, entry := range entries {
		if cache, ok := entry.Module.(contracts.CacheLayer); ok {
			o.mu.Lock()
			o.cache = cache
			o.mu.Unlock()
			return
		}
	}
	// Also check storage modules that implement CacheLayer
	entries = o.registry.FindByRole("storage")
	for _, entry := range entries {
		if cache, ok := entry.Module.(contracts.CacheLayer); ok {
			o.mu.Lock()
			o.cache = cache
			o.mu.Unlock()
			return
		}
	}
}

// AddPolicy registers a routing policy.
func (o *Orchestrator) AddPolicy(p RoutingPolicy) {
	o.mu.Lock()
	o.policies = append(o.policies, p)
	o.mu.Unlock()
}

// SetCache directly sets the cache layer (for testing or forced override).
func (o *Orchestrator) SetCache(c contracts.CacheLayer) {
	o.mu.Lock()
	o.cache = c
	o.mu.Unlock()
}

func (o *Orchestrator) route(key string) (contracts.StorageProvider, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	for _, p := range o.policies {
		if strings.HasPrefix(key, p.Prefix) {
			if prov, ok := o.providers[p.Provider]; ok {
				return prov, nil
			}
		}
	}
	// Sort provider IDs for deterministic fallback selection
	ids := make([]string, 0, len(o.providers))
	for id := range o.providers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) > 0 {
		return o.providers[ids[0]], nil
	}
	return nil, fmt.Errorf("no storage provider available for key %q", key)
}

func (o *Orchestrator) Put(ctx context.Context, key string, data io.Reader, size int64) error {
	prov, err := o.route(key)
	if err != nil {
		return err
	}

	// Read data into buffer for write-through cache.
	buf, err := io.ReadAll(data)
	if err != nil {
		return err
	}

	if err := prov.Put(ctx, key, bytes.NewReader(buf), size); err != nil {
		return err
	}

	// Write-through: update cache after successful Put.
	o.mu.RLock()
	cache := o.cache
	o.mu.RUnlock()
	if cache != nil {
		_ = cache.Set(ctx, key, buf)
	}

	return nil
}

func (o *Orchestrator) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	o.mu.RLock()
	cache := o.cache
	o.mu.RUnlock()
	if cache != nil {
		if data, ok := cache.Get(ctx, key); ok {
			return io.NopCloser(bytes.NewReader(data)), nil
		}
	}

	prov, err := o.route(key)
	if err != nil {
		return nil, err
	}
	return prov.Get(ctx, key)
}

func (o *Orchestrator) Delete(ctx context.Context, key string) error {
	prov, err := o.route(key)
	if err != nil {
		return err
	}

	if err := prov.Delete(ctx, key); err != nil {
		return err
	}

	// Invalidate cache entry after successful Delete.
	o.mu.RLock()
	cache := o.cache
	o.mu.RUnlock()
	if cache != nil {
		_ = cache.Invalidate(ctx, key)
	}

	return nil
}

func (o *Orchestrator) Exists(ctx context.Context, key string) (bool, error) {
	prov, err := o.route(key)
	if err != nil {
		return false, err
	}
	return prov.Exists(ctx, key)
}

func (o *Orchestrator) Stat(ctx context.Context, key string) (contracts.ObjectInfo, error) {
	prov, err := o.route(key)
	if err != nil {
		return contracts.ObjectInfo{}, err
	}
	return prov.Stat(ctx, key)
}

func (o *Orchestrator) Move(ctx context.Context, src, dst string) error {
	prov, err := o.route(src)
	if err != nil {
		return err
	}
	return prov.Move(ctx, src, dst)
}

func (o *Orchestrator) List(ctx context.Context, prefix string) ([]contracts.ObjectInfo, error) {
	prov, err := o.route(prefix)
	if err != nil {
		return nil, err
	}
	return prov.List(ctx, prefix)
}

func (o *Orchestrator) ProviderCount() int {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return len(o.providers)
}

func (o *Orchestrator) CapabilityCheck(ctx context.Context, key string) ([]string, error) {
	prov, err := o.route(key)
	if err != nil {
		return nil, err
	}
	var caps []string
	if _, ok := prov.(contracts.Streamable); ok {
		caps = append(caps, "streamable")
	}
	if _, ok := prov.(contracts.Seekable); ok {
		caps = append(caps, "seekable")
	}
	if _, ok := prov.(contracts.Watchable); ok {
		caps = append(caps, "watchable")
	}
	if _, ok := prov.(contracts.AtomicMovable); ok {
		caps = append(caps, "atomic_movable")
	}
	if _, ok := prov.(contracts.Hardlinkable); ok {
		caps = append(caps, "hardlinkable")
	}
	return caps, nil
}
