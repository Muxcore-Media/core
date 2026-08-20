//nolint:govet // struct field alignment
package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Muxcore-Media/core/internal/callerid"
	"github.com/Muxcore-Media/core/pkg/contracts"
	"github.com/google/uuid"
)

// MaxObjectSize is the maximum size of a single object that can be stored.
// Sized for full media files (season packs / remuxes) on local disk backends.
const MaxObjectSize = 64 << 30 // 64 GiB

// Orchestrator routes storage operations to registered providers based on
// capability negotiation and user-defined policies.
//
// Per-operation timeouts wrap every provider call so a hung provider cannot
// block the caller indefinitely. Set via Timeouts, defaulting to
// 30s reads, 5m writes, 30s deletes.
// SidecarDialer opens a StorageProvider for a registered storage sidecar
// (module id + announce/HTTP addr). Used when the registry entry is a proxy
// rather than an in-process StorageProvider.
type SidecarDialer func(moduleID, addr string) (contracts.StorageProvider, error)

type Orchestrator struct {
	mu            sync.RWMutex
	registry      contracts.Registry
	providers     map[string]contracts.StorageProvider
	tiers         map[string]contracts.StorageTier // provider ID -> tier (empty = default)
	policies      []RoutingPolicy
	cache         contracts.CacheLayer
	audit         contracts.AuditLogger
	sidecarDialer SidecarDialer
	readTimeout   time.Duration
	writeTimeout  time.Duration
	deleteTimeout time.Duration
	// auditSem limits concurrent audit goroutines to prevent unbounded bursts.
	auditSem    chan struct{}
	putCount    atomic.Int64
	getCount    atomic.Int64
	deleteCount atomic.Int64
	statCount   atomic.Int64
	listCount   atomic.Int64
	moveCount   atomic.Int64
}

// Timeouts holds per-operation deadline durations for the orchestrator.
type Timeouts struct {
	Read   time.Duration // Get, Exists, Stat, List, Stream
	Write  time.Duration // Put
	Delete time.Duration // Delete, Move
}

// RoutingPolicy decides which provider handles a given key.
type RoutingPolicy struct {
	Name     string
	Prefix   string
	Provider string
}

// NewOrchestrator creates an Orchestrator that uses the given registry for
// provider discovery. Default timeouts: 30s read/delete, 5m write.
func NewOrchestrator(reg contracts.Registry) *Orchestrator {
	return &Orchestrator{
		registry:      reg,
		providers:     make(map[string]contracts.StorageProvider),
		tiers:         make(map[string]contracts.StorageTier),
		readTimeout:   30 * time.Second,
		writeTimeout:  2 * time.Hour,
		deleteTimeout: 30 * time.Second,
		auditSem:      make(chan struct{}, 100),
	}
}

// SetTimeouts configures per-operation deadlines. Zero values keep the current
// default. Call before any storage operations begin.
func (o *Orchestrator) SetTimeouts(t Timeouts) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if t.Read > 0 {
		o.readTimeout = t.Read
	}
	if t.Write > 0 {
		o.writeTimeout = t.Write
	}
	if t.Delete > 0 {
		o.deleteTimeout = t.Delete
	}
}

// SetSidecarDialer configures how storage sidecars are dialed. Optional;
// without a dialer only in-process StorageProvider modules are discovered.
func (o *Orchestrator) SetSidecarDialer(d SidecarDialer) {
	o.mu.Lock()
	o.sidecarDialer = d
	o.mu.Unlock()
}

// DiscoverStorage finds all registered storage modules and adds them to the pool.
// In-process modules that implement StorageProvider are preferred; otherwise a
// configured SidecarDialer dials modules advertising the storage capability
// (or role "storage") via their HTTP/gRPC announce address.
func (o *Orchestrator) DiscoverStorage() error {
	seen := map[string]struct{}{}
	byRole := o.registry.FindByRole("storage")
	byCap := o.registry.FindByCapability(contracts.CapabilityStorage)
	entries := make([]contracts.ModuleEntry, 0, len(byRole)+len(byCap))
	entries = append(entries, byRole...)
	entries = append(entries, byCap...)
	for _, entry := range entries {
		id := entry.Info.ID
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if err := o.registerEntry(entry); err != nil {
			slog.Warn("storage discover skip", "module", id, "error", err)
		}
	}
	return nil
}

func (o *Orchestrator) registerEntry(entry contracts.ModuleEntry) error {
	id := entry.Info.ID
	if provider, ok := entry.Module.(contracts.StorageProvider); ok {
		o.mu.Lock()
		o.providers[id] = provider
		o.mu.Unlock()
		return nil
	}
	o.mu.RLock()
	dialer := o.sidecarDialer
	o.mu.RUnlock()
	if dialer == nil {
		return nil
	}
	addr := entry.Info.HTTPAddr
	if addr == "" {
		return fmt.Errorf("no announce addr")
	}
	if !hasStorageCapability(entry.Info) && !hasStorageRole(entry.Info) {
		return nil
	}
	provider, err := dialer(id, addr)
	if err != nil {
		return err
	}
	o.mu.Lock()
	o.providers[id] = provider
	o.mu.Unlock()
	slog.Info("remote storage provider registered", "module", id, "addr", addr)
	return nil
}

func hasStorageCapability(info contracts.ModuleInfo) bool {
	for _, c := range info.Capabilities {
		if c == contracts.CapabilityStorage || c == "storage.s3" || strings.HasPrefix(c, "storage.") {
			return true
		}
	}
	return false
}

func hasStorageRole(info contracts.ModuleInfo) bool {
	for _, r := range info.Roles {
		if r == "storage" {
			return true
		}
	}
	return false
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

// DiscoverTiers scans registered storage providers and records which ones
// implement TieredProvider and at which tier. Call after DiscoverStorage
// to populate the tier map.
func (o *Orchestrator) DiscoverTiers() {
	o.mu.Lock()
	defer o.mu.Unlock()
	for id, prov := range o.providers {
		if tp, ok := prov.(contracts.TieredProvider); ok {
			o.tiers[id] = tp.Tier()
		} else {
			delete(o.tiers, id)
		}
	}
}

// Promote moves an object to a higher storage tier (e.g., cold -> hot).
// Returns ErrNotImplemented if the provider does not support tiering.
func (o *Orchestrator) Promote(ctx context.Context, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	key = namespaceKey(ctx, key)
	prov, err := o.route(key)
	if err != nil {
		return err
	}
	tp, ok := prov.(contracts.TieredProvider)
	if !ok {
		return fmt.Errorf("provider for %q does not support tiering", key)
	}
	tctx, cancel := o.withTimeout(ctx, o.writeTimeout)
	defer cancel()
	return tp.Promote(tctx, key)
}

// Relegate moves an object to a lower storage tier (e.g., hot -> cold).
// Returns ErrNotImplemented if the provider does not support tiering.
func (o *Orchestrator) Relegate(ctx context.Context, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	key = namespaceKey(ctx, key)
	prov, err := o.route(key)
	if err != nil {
		return err
	}
	tp, ok := prov.(contracts.TieredProvider)
	if !ok {
		return fmt.Errorf("provider for %q does not support tiering", key)
	}
	tctx, cancel := o.withTimeout(ctx, o.writeTimeout)
	defer cancel()
	return tp.Relegate(tctx, key)
}

// SetCache directly sets the cache layer (for testing or forced override).
func (o *Orchestrator) SetCache(c contracts.CacheLayer) {
	o.mu.Lock()
	o.cache = c
	o.mu.Unlock()
}

// SetProvider directly registers a storage provider by ID.
// Used by main.go to register built-in providers (e.g., local filesystem).
func (o *Orchestrator) SetProvider(id string, prov contracts.StorageProvider) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.providers[id] = prov
}

// SetAuditLogger attaches an audit logger for recording storage operations.
func (o *Orchestrator) SetAuditLogger(a contracts.AuditLogger) {
	o.mu.Lock()
	o.audit = a
	o.mu.Unlock()
}

// validateKey checks that a storage key does not contain path traversal sequences
// or other unsafe patterns. Returns an error if the key is invalid.
func validateKey(key string) error {
	if key == "" {
		return fmt.Errorf("storage key must not be empty")
	}
	return validateKeyOrPrefix(key)
}

// validatePrefix validates a prefix for List operations. Unlike validateKey,
// the empty string is allowed and means "match all objects".
func validatePrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	return validateKeyOrPrefix(prefix)
}

// namespaceKey prefixes a storage key with the caller's module ID when the
// caller is a registered module (extracted from context). This enforces
// per-module key isolation: modules can only access keys under their own
// namespace. System-level calls (no caller ID) use the key as-is.
//
// Keys under torrent/ are shared across modules (downloader writes pieces /
// assembled files; scanner imports them) and are not namespaced.
func namespaceKey(ctx context.Context, key string) string {
	if strings.HasPrefix(key, "torrent/") {
		return key
	}
	if cid := callerid.Get(ctx); cid != "" && !strings.HasPrefix(key, cid+"/") {
		return cid + "/" + key
	}
	return key
}

// legacyDownloaderTorrentKey is where downloader-native-torrent wrote objects
// before torrent/ became a shared (un-namespaced) prefix.
func legacyDownloaderTorrentKey(key string) string {
	if strings.HasPrefix(key, "torrent/") {
		return "downloader-native-torrent/" + key
	}
	return ""
}

// validateKeyOrPrefix applies the common validation rules shared by
// validateKey and validatePrefix.
func validateKeyOrPrefix(s string) error {
	if strings.Contains(s, "..") {
		return fmt.Errorf("storage key must not contain '..'")
	}
	if strings.HasPrefix(s, "/") {
		return fmt.Errorf("storage key must not start with '/': %q", s)
	}
	if strings.ContainsRune(s, '\x00') {
		return fmt.Errorf("storage key must not contain null bytes")
	}
	if len(s) > 1024 {
		return fmt.Errorf("storage key too long (%d bytes)", len(s))
	}
	return nil
}

// providerTierRank returns the tier preference for sorting. Lower rank = preferred.
func providerTierRank(tier contracts.StorageTier) int {
	switch tier {
	case contracts.StorageTierHot:
		return 0
	case contracts.StorageTierWarm:
		return 1
	case contracts.StorageTierCold:
		return 2
	case contracts.StorageTierArchive:
		return 3
	default:
		return 1 // warm-equivalent for providers without a declared tier
	}
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
	// Sort provider IDs for deterministic fallback selection, preferring
	// higher-tier (lower rank) providers.
	ids := make([]string, 0, len(o.providers))
	for id := range o.providers {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		ti := providerTierRank(o.tiers[ids[i]])
		tj := providerTierRank(o.tiers[ids[j]])
		if ti != tj {
			return ti < tj
		}
		return ids[i] < ids[j]
	})
	if len(ids) > 0 {
		return o.providers[ids[0]], nil
	}
	return nil, fmt.Errorf("no storage provider available for key %q", key)
}

// withTimeout returns a child context with o's timeout for the given operation,
// plus a cancel function the caller must defer. If the parent context already
// has a shorter deadline, the parent deadline wins (context.WithTimeout is a no-op).
func (o *Orchestrator) withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}

func (o *Orchestrator) Put(ctx context.Context, key string, data io.Reader, size int64) error {
	o.putCount.Add(1)
	if err := validateKey(key); err != nil {
		return err
	}
	if size > MaxObjectSize {
		return fmt.Errorf("object size %d exceeds maximum %d", size, MaxObjectSize)
	}
	key = namespaceKey(ctx, key)

	prov, err := o.route(key)
	if err != nil {
		return err
	}

	putCtx, cancel := o.withTimeout(ctx, o.writeTimeout)
	defer cancel()

	var actualSize int64
	var cacheBuf []byte

	if size > 0 {
		// Size is known — pass the reader directly without buffering.
		// The provider can stream the data incrementally.
		actualSize = size
		if err := prov.Put(putCtx, key, data, size); err != nil {
			return err
		}
	} else {
		// Size is unknown — read into memory to determine size, capped at MaxObjectSize+1.
		if data == nil {
			return fmt.Errorf("storage: data reader is nil")
		}
		buf, rerr := io.ReadAll(io.LimitReader(data, MaxObjectSize+1))
		if rerr != nil {
			return rerr
		}
		if int64(len(buf)) > MaxObjectSize {
			return fmt.Errorf("object exceeds maximum size %d", MaxObjectSize)
		}
		actualSize = int64(len(buf))
		cacheBuf = buf
		if err := prov.Put(putCtx, key, bytes.NewReader(buf), actualSize); err != nil {
			return err
		}
	}

	// Audit successful storage put.
	o.auditStorage(ctx, "storage.put", key, actualSize)

	// Write-through: update cache after successful Put.
	// Cache races with SetCache/DiscoverCache are acceptable — eventually consistent.
	o.mu.RLock()
	cache := o.cache
	o.mu.RUnlock()
	if cache != nil {
		if cacheBuf != nil {
			if err := cache.Set(ctx, key, cacheBuf); err != nil {
				slog.Warn("cache set failed", "key", key, "error", err)
			}
		} else {
			// When size was known we didn't buffer; fetch from provider for cache.
			if err := cache.Invalidate(ctx, key); err != nil {
				slog.Warn("cache invalidate failed", "key", key, "error", err)
			}
		}
	}

	return nil
}

func (o *Orchestrator) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	o.getCount.Add(1)
	if err := validateKey(key); err != nil {
		return nil, err
	}
	orig := key
	key = namespaceKey(ctx, key)
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

	try := func(k string) (io.ReadCloser, error) {
		getCtx, cancel := o.withTimeout(ctx, o.readTimeout)
		defer cancel()
		return prov.Get(getCtx, k)
	}
	rc, err := try(key)
	if err != nil && isNotFound(err) {
		if alt := legacyDownloaderTorrentKey(orig); alt != "" && alt != key {
			rc, err = try(alt)
		}
	}
	if err != nil && !errors.Is(err, contracts.ErrNotFound) {
		if isNotFound(err) {
			return nil, contracts.ErrNotFound
		}
	}
	return rc, err
}

func (o *Orchestrator) Delete(ctx context.Context, key string) error {
	o.deleteCount.Add(1)
	if err := validateKey(key); err != nil {
		return err
	}
	key = namespaceKey(ctx, key)
	prov, err := o.route(key)
	if err != nil {
		return err
	}

	delCtx, cancel := o.withTimeout(ctx, o.deleteTimeout)
	defer cancel()

	if err := prov.Delete(delCtx, key); err != nil {
		return err
	}

	// Audit successful storage delete.
	o.auditStorage(ctx, "storage.delete", key, 0)

	// Invalidate cache entry after successful Delete.
	o.mu.RLock()
	cache := o.cache
	o.mu.RUnlock()
	if cache != nil {
		if err := cache.Invalidate(ctx, key); err != nil {
			slog.Warn("cache invalidate failed", "key", key, "error", err)
		}
	}

	return nil
}

func (o *Orchestrator) Exists(ctx context.Context, key string) (bool, error) {
	if err := validateKey(key); err != nil {
		return false, err
	}
	orig := key
	key = namespaceKey(ctx, key)
	prov, err := o.route(key)
	if err != nil {
		return false, err
	}
	try := func(k string) (bool, error) {
		tctx, cancel := o.withTimeout(ctx, o.readTimeout)
		defer cancel()
		return prov.Exists(tctx, k)
	}
	ok, err := try(key)
	if err != nil || ok {
		return ok, err
	}
	if alt := legacyDownloaderTorrentKey(orig); alt != "" && alt != key {
		return try(alt)
	}
	return false, nil
}

func (o *Orchestrator) Stat(ctx context.Context, key string) (contracts.ObjectInfo, error) {
	o.statCount.Add(1)
	if err := validateKey(key); err != nil {
		return contracts.ObjectInfo{}, err
	}
	orig := key
	key = namespaceKey(ctx, key)
	prov, err := o.route(key)
	if err != nil {
		return contracts.ObjectInfo{}, err
	}
	try := func(k string) (contracts.ObjectInfo, error) {
		tctx, cancel := o.withTimeout(ctx, o.readTimeout)
		defer cancel()
		return prov.Stat(tctx, k)
	}
	info, err := try(key)
	if err != nil && isNotFound(err) {
		if alt := legacyDownloaderTorrentKey(orig); alt != "" && alt != key {
			info, err = try(alt)
			if err == nil {
				info.Key = orig
			}
		}
	}
	return info, err
}

func (o *Orchestrator) Move(ctx context.Context, src, dst string) error {
	o.moveCount.Add(1)
	if err := validateKey(src); err != nil {
		return err
	}
	if err := validateKey(dst); err != nil {
		return err
	}
	src = namespaceKey(ctx, src)
	dst = namespaceKey(ctx, dst)
	prov, err := o.route(src)
	if err != nil {
		return err
	}
	tctx, cancel := o.withTimeout(ctx, o.deleteTimeout)
	defer cancel()
	if err := prov.Move(tctx, src, dst); err != nil {
		return err
	}
	// Audit successful storage move.
	o.auditStorage(ctx, "storage.move", dst, 0)
	return nil
}

func (o *Orchestrator) List(ctx context.Context, prefix string) ([]contracts.ObjectInfo, error) {
	o.listCount.Add(1)
	if err := validatePrefix(prefix); err != nil {
		return nil, err
	}
	orig := prefix
	prefix = namespaceKey(ctx, prefix)
	prov, err := o.route(prefix)
	if err != nil {
		return nil, err
	}
	try := func(p string) ([]contracts.ObjectInfo, error) {
		tctx, cancel := o.withTimeout(ctx, o.readTimeout)
		defer cancel()
		return prov.List(tctx, p)
	}
	objs, err := try(prefix)
	if err != nil {
		return nil, err
	}
	if len(objs) == 0 {
		if alt := legacyDownloaderTorrentKey(orig); alt != "" && alt != prefix {
			objs, err = try(alt)
			if err != nil {
				return nil, err
			}
			const legacy = "downloader-native-torrent/"
			for i := range objs {
				objs[i].Key = strings.TrimPrefix(objs[i].Key, legacy)
			}
		}
	}
	return objs, nil
}

func (o *Orchestrator) ProviderCount() int {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return len(o.providers)
}

func (o *Orchestrator) PutCount() int64    { return o.putCount.Load() }
func (o *Orchestrator) Count() int64       { return o.getCount.Load() }
func (o *Orchestrator) DeleteCount() int64 { return o.deleteCount.Load() }
func (o *Orchestrator) StatCount() int64   { return o.statCount.Load() }
func (o *Orchestrator) ListCount() int64   { return o.listCount.Load() }
func (o *Orchestrator) MoveCount() int64   { return o.moveCount.Load() }

// ProviderInfo describes a registered storage provider for the admin API.
type ProviderInfo struct {
	ID           string                `json:"id"`
	Capabilities []string              `json:"capabilities"`
	IsCache      bool                  `json:"is_cache"`
	Tier         contracts.StorageTier `json:"tier,omitempty"`
}

// ProviderInfo returns metadata for all registered storage providers.
func (o *Orchestrator) ProviderInfo() []ProviderInfo {
	o.mu.RLock()
	defer o.mu.RUnlock()

	infos := make([]ProviderInfo, 0, len(o.providers))
	for id, prov := range o.providers {
		info := ProviderInfo{
			ID:           id,
			Capabilities: collectCapabilities(prov),
			Tier:         o.tiers[id],
		}
		infos = append(infos, info)
	}
	return infos
}

func (o *Orchestrator) CapabilityCheck(ctx context.Context, key string) ([]string, error) {
	if key != "" {
		if err := validateKey(key); err != nil {
			return nil, err
		}
	}
	key = namespaceKey(ctx, key)
	prov, err := o.route(key)
	if err != nil {
		return nil, err
	}
	return collectCapabilities(prov), nil
}

// collectCapabilities returns the list of optional capability interfaces
// that the given storage provider implements.
func collectCapabilities(prov contracts.StorageProvider) []string {
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
	return caps
}

// auditStorage records a storage operation via the audit logger.
// It uses the caller's context for cancellation and limits concurrent audit
// goroutines to 100 to prevent unbounded goroutine bursts.
func (o *Orchestrator) auditStorage(ctx context.Context, action, key string, size int64) {
	o.mu.RLock()
	auditLogger := o.audit
	o.mu.RUnlock()
	if auditLogger == nil {
		return
	}
	select {
	case o.auditSem <- struct{}{}:
		go func() {
			defer func() {
				<-o.auditSem
				if r := recover(); r != nil {
					slog.Error("audit storage panic recovered", "action", action, "key", key, "panic", r)
				}
			}()
			details := map[string]string{
				"key": key,
			}
			if size > 0 {
				details["size_bytes"] = fmt.Sprintf("%d", size)
			}
			entry := contracts.AuditEntry{
				ID:        uuid.New().String(),
				Timestamp: time.Now(),
				Actor:     "system",
				Action:    action,
				Resource:  key,
				Details:   details,
			}
			if err := auditLogger.Log(ctx, entry); err != nil {
				slog.Error("audit log write failed", "action", action, "error", err)
			}
		}()
	default:
		slog.Warn("audit storage: too many concurrent audit logs, dropping entry", "action", action, "key", key)
	}
}

// WatchModules subscribes to module.registered events and automatically
// re-discovers storage providers and cache layers when new modules appear.
// Call this after bootstrap to enable dynamic provider registration.
// ctx controls the subscription lifecycle — when ctx is cancelled the
// subscription is unsubscribed. The returned cancel function also stops watching.
func (o *Orchestrator) WatchModules(ctx context.Context, bus contracts.EventBus) (cancel func()) {
	if bus == nil {
		slog.Warn("WatchModules called with nil EventBus - module discovery watching disabled")
		return func() {}
	}

	handler := func(ctx context.Context, event contracts.Event) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		var payload contracts.ModuleRegisteredPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return nil //nolint:nilerr // skip malformed events in event handler
		}

		// Look up the newly registered module
		entry, err := o.registry.Resolve(payload.ModuleID)
		if err != nil {
			return nil //nolint:nilerr // module may have unregistered already, skip
		}

		if err := o.registerEntry(entry); err != nil {
			slog.Debug("WatchModules storage register", "module", payload.ModuleID, "error", err)
		}

		// Check if it implements CacheLayer
		if cache, ok := entry.Module.(contracts.CacheLayer); ok {
			o.mu.Lock()
			o.cache = cache
			o.mu.Unlock()
		}

		return nil
	}

	cancel, err := bus.Subscribe(ctx, contracts.EventModuleRegistered, handler)
	if err != nil {
		slog.Error("WatchModules: subscribe failed", "error", err)
		return func() {}
	}
	return cancel
}

// isNotFound checks if an error from a storage provider indicates that the
// requested key does not exist. Providers may return their own error types;
// this heuristic catches the most common patterns so orchestrator callers
// can use errors.Is(err, contracts.ErrNotFound).
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, contracts.ErrNotFound) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not found") ||
		strings.Contains(msg, "no such") ||
		strings.Contains(msg, "does not exist") ||
		strings.Contains(msg, "key not found")
}
