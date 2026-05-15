package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// Orchestrator routes storage operations to registered providers based on
// capability negotiation and user-defined policies.
type Orchestrator struct {
	mu        sync.RWMutex
	registry  contracts.ServiceRegistry
	providers map[string]contracts.StorageProvider
	policies  []RoutingPolicy
	cache     CacheLayer
}

// RoutingPolicy decides which provider handles a given key.
type RoutingPolicy struct {
	Name     string // human-readable name for the policy
	Prefix   string // key prefix this policy handles, e.g. "media/" or "backups/"
	Provider string // module ID of the preferred provider
}

// CacheLayer is an optional read-through cache.
type CacheLayer interface {
	Get(ctx context.Context, key string) ([]byte, bool)
	Set(ctx context.Context, key string, data []byte) error
	Invalidate(ctx context.Context, prefix string) error
}

// NewOrchestrator creates an Orchestrator that uses the given registry for
// provider discovery.
func NewOrchestrator(reg contracts.ServiceRegistry) *Orchestrator {
	return &Orchestrator{
		registry:  reg,
		providers: make(map[string]contracts.StorageProvider),
	}
}

// Discover finds all registered storage modules and adds them to the pool.
func (o *Orchestrator) Discover() error {
	entries := o.registry.FindByKind(contracts.ModuleKindStorage)
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

// AddPolicy registers a routing policy.
func (o *Orchestrator) AddPolicy(p RoutingPolicy) {
	o.mu.Lock()
	o.policies = append(o.policies, p)
	o.mu.Unlock()
}

// SetCache sets the cache layer for read-through caching.
func (o *Orchestrator) SetCache(c CacheLayer) {
	o.mu.Lock()
	o.cache = c
	o.mu.Unlock()
}

// route returns the provider for a given key based on routing policies.
func (o *Orchestrator) route(key string) (contracts.StorageProvider, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	// Check routing policies first
	for _, p := range o.policies {
		if strings.HasPrefix(key, p.Prefix) {
			if prov, ok := o.providers[p.Provider]; ok {
				return prov, nil
			}
		}
	}
	// Fall back to first available provider
	for _, prov := range o.providers {
		return prov, nil
	}
	return nil, fmt.Errorf("no storage provider available for key %q", key)
}

// Put stores data using the routed provider.
func (o *Orchestrator) Put(ctx context.Context, key string, data io.Reader, size int64) error {
	prov, err := o.route(key)
	if err != nil {
		return err
	}
	return prov.Put(ctx, key, data, size)
}

// Get retrieves data, checking cache first.
func (o *Orchestrator) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	// Check cache
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

// Delete removes data.
func (o *Orchestrator) Delete(ctx context.Context, key string) error {
	prov, err := o.route(key)
	if err != nil {
		return err
	}
	return prov.Delete(ctx, key)
}

// Exists checks whether data exists at the given key.
func (o *Orchestrator) Exists(ctx context.Context, key string) (bool, error) {
	prov, err := o.route(key)
	if err != nil {
		return false, err
	}
	return prov.Exists(ctx, key)
}

// Stat returns metadata for the given key.
func (o *Orchestrator) Stat(ctx context.Context, key string) (contracts.ObjectInfo, error) {
	prov, err := o.route(key)
	if err != nil {
		return contracts.ObjectInfo{}, err
	}
	return prov.Stat(ctx, key)
}

// Move moves data from src to dst, using atomic move if the provider supports it.
func (o *Orchestrator) Move(ctx context.Context, src, dst string) error {
	prov, err := o.route(src)
	if err != nil {
		return err
	}
	return prov.Move(ctx, src, dst)
}

// List returns all objects under the given prefix.
func (o *Orchestrator) List(ctx context.Context, prefix string) ([]contracts.ObjectInfo, error) {
	prov, err := o.route(prefix)
	if err != nil {
		return nil, err
	}
	return prov.List(ctx, prefix)
}

// CapabilityCheck returns which capability a provider supports for a key.
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
