package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/internal/events"
	"github.com/Muxcore-Media/core/pkg/contracts"
)

type mockProvider struct {
	id   string
	data map[string][]byte
	info map[string]contracts.ObjectInfo
}

func newMockProvider(id string) *mockProvider {
	return &mockProvider{
		id:   id,
		data: make(map[string][]byte),
		info: make(map[string]contracts.ObjectInfo),
	}
}

func (m *mockProvider) Put(ctx context.Context, key string, data io.Reader, size int64) error {
	buf, _ := io.ReadAll(data)
	m.data[key] = buf
	m.info[key] = contracts.ObjectInfo{Key: key, Size: int64(len(buf))}
	return nil
}

func (m *mockProvider) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	buf, ok := m.data[key]
	if !ok {
		return nil, fmt.Errorf("key %q not found", key)
	}
	return io.NopCloser(bytes.NewReader(buf)), nil
}

func (m *mockProvider) Delete(ctx context.Context, key string) error {
	delete(m.data, key)
	delete(m.info, key)
	return nil
}

func (m *mockProvider) Move(ctx context.Context, src, dst string) error {
	buf, ok := m.data[src]
	if !ok {
		return fmt.Errorf("src %q not found", src)
	}
	m.data[dst] = buf
	delete(m.data, src)
	return nil
}

func (m *mockProvider) Exists(ctx context.Context, key string) (bool, error) {
	_, ok := m.data[key]
	return ok, nil
}

func (m *mockProvider) Stat(ctx context.Context, key string) (contracts.ObjectInfo, error) {
	info, ok := m.info[key]
	if !ok {
		return contracts.ObjectInfo{}, fmt.Errorf("key %q not found", key)
	}
	return info, nil
}

func (m *mockProvider) List(ctx context.Context, prefix string) ([]contracts.ObjectInfo, error) {
	var result []contracts.ObjectInfo
	for k, v := range m.info {
		if strings.HasPrefix(k, prefix) {
			result = append(result, v)
		}
	}
	return result, nil
}

type mockRegistry struct {
	providers map[string]contracts.ModuleEntry
}

func newMockRegistry() *mockRegistry {
	return &mockRegistry{providers: make(map[string]contracts.ModuleEntry)}
}

func (r *mockRegistry) addProvider(prov contracts.StorageProvider, id string) {
	module, ok := prov.(contracts.Module)
	if !ok {
		return
	}
	r.providers[id] = contracts.ModuleEntry{
		Info:   contracts.ModuleInfo{ID: id, Roles: []string{"storage"}},
		Module: module,
	}
}

func (r *mockRegistry) FindByRole(role string) []contracts.ModuleEntry {
	result := make([]contracts.ModuleEntry, 0, len(r.providers))
	for _, e := range r.providers {
		result = append(result, e)
	}
	return result
}
func (r *mockRegistry) FindByCapability(capability string) []contracts.ModuleEntry { return nil }
func (r *mockRegistry) SupportsCapability(moduleID, capability string) bool        { return false }
func (r *mockRegistry) Resolve(id string) (contracts.ModuleEntry, error) {
	e, ok := r.providers[id]
	if !ok {
		return contracts.ModuleEntry{}, fmt.Errorf("not found")
	}
	return e, nil
}
func (r *mockRegistry) ListAll() []contracts.ModuleEntry {
	result := make([]contracts.ModuleEntry, 0, len(r.providers))
	for _, e := range r.providers {
		result = append(result, e)
	}
	return result
}

func (r *mockRegistry) StartupOrder() ([]string, error) {
	ids := make([]string, 0, len(r.providers))
	for id := range r.providers {
		ids = append(ids, id)
	}
	return ids, nil
}

func (r *mockRegistry) DependencyGraph(id string) ([]string, error) { return nil, nil }

type mockModule struct {
	contracts.StorageProvider
	info contracts.ModuleInfo
}

func (m *mockModule) Info() contracts.ModuleInfo       { return m.info }
func (m *mockModule) Init(ctx context.Context) error   { return nil }
func (m *mockModule) Start(ctx context.Context) error  { return nil }
func (m *mockModule) Stop(ctx context.Context) error   { return nil }
func (m *mockModule) Health(ctx context.Context) error { return nil }

func TestOrchestrator_NoProvider(t *testing.T) {
	reg := newMockRegistry()
	orch := NewOrchestrator(reg)
	orch.DiscoverStorage()
	_, err := orch.Get(context.Background(), "test-key")
	if err == nil {
		t.Fatal("expected error when no provider available")
	}
}

func TestOrchestrator_PutGet(t *testing.T) {
	reg := newMockRegistry()
	prov := newMockProvider("local")
	reg.addProvider(&mockModule{StorageProvider: prov, info: contracts.ModuleInfo{ID: "local"}}, "local")
	orch := NewOrchestrator(reg)
	orch.DiscoverStorage()

	data := []byte("hello world")
	err := orch.Put(context.Background(), "greeting", bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	rc, err := orch.Get(context.Background(), "greeting")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	buf, _ := io.ReadAll(rc)
	if string(buf) != "hello world" {
		t.Errorf("expected 'hello world', got %q", string(buf))
	}
}

func TestOrchestrator_Delete(t *testing.T) {
	reg := newMockRegistry()
	prov := newMockProvider("local")
	reg.addProvider(&mockModule{StorageProvider: prov, info: contracts.ModuleInfo{ID: "local"}}, "local")
	orch := NewOrchestrator(reg)
	orch.DiscoverStorage()
	ctx := context.Background()

	orch.Put(ctx, "temp", bytes.NewReader([]byte("x")), 1)
	if err := orch.Delete(ctx, "temp"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	exists, _ := orch.Exists(ctx, "temp")
	if exists {
		t.Error("expected key to not exist after delete")
	}
}

func TestOrchestrator_Exists(t *testing.T) {
	reg := newMockRegistry()
	prov := newMockProvider("local")
	reg.addProvider(&mockModule{StorageProvider: prov, info: contracts.ModuleInfo{ID: "local"}}, "local")
	orch := NewOrchestrator(reg)
	orch.DiscoverStorage()
	ctx := context.Background()

	exists, _ := orch.Exists(ctx, "missing")
	if exists {
		t.Error("expected missing key to not exist")
	}
	orch.Put(ctx, "present", bytes.NewReader([]byte("x")), 1)
	exists, _ = orch.Exists(ctx, "present")
	if !exists {
		t.Error("expected present key to exist")
	}
}

func TestOrchestrator_Move(t *testing.T) {
	reg := newMockRegistry()
	prov := newMockProvider("local")
	reg.addProvider(&mockModule{StorageProvider: prov, info: contracts.ModuleInfo{ID: "local"}}, "local")
	orch := NewOrchestrator(reg)
	orch.DiscoverStorage()
	ctx := context.Background()

	orch.Put(ctx, "src", bytes.NewReader([]byte("moved")), 5)
	if err := orch.Move(ctx, "src", "dst"); err != nil {
		t.Fatalf("Move: %v", err)
	}
	rc, err := orch.Get(ctx, "dst")
	if err != nil {
		t.Fatalf("Get dst: %v", err)
	}
	defer rc.Close()
	buf, _ := io.ReadAll(rc)
	if string(buf) != "moved" {
		t.Errorf("expected 'moved' at dst, got %q", string(buf))
	}
}

func TestOrchestrator_Stat(t *testing.T) {
	reg := newMockRegistry()
	prov := newMockProvider("local")
	reg.addProvider(&mockModule{StorageProvider: prov, info: contracts.ModuleInfo{ID: "local"}}, "local")
	orch := NewOrchestrator(reg)
	orch.DiscoverStorage()
	ctx := context.Background()

	data := []byte("stat-me")
	orch.Put(ctx, "stat-key", bytes.NewReader(data), int64(len(data)))
	info, err := orch.Stat(ctx, "stat-key")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size != int64(len(data)) {
		t.Errorf("expected size %d, got %d", len(data), info.Size)
	}
}

func TestOrchestrator_List(t *testing.T) {
	reg := newMockRegistry()
	prov := newMockProvider("local")
	reg.addProvider(&mockModule{StorageProvider: prov, info: contracts.ModuleInfo{ID: "local"}}, "local")
	orch := NewOrchestrator(reg)
	orch.DiscoverStorage()
	ctx := context.Background()

	orch.Put(ctx, "media/movie1", bytes.NewReader([]byte("a")), 1)
	orch.Put(ctx, "media/movie2", bytes.NewReader([]byte("b")), 1)
	orch.Put(ctx, "backups/db", bytes.NewReader([]byte("c")), 1)
	items, _ := orch.List(ctx, "media/")
	if len(items) != 2 {
		t.Errorf("expected 2 items under media/, got %d", len(items))
	}
}

func TestOrchestrator_PolicyRouting(t *testing.T) {
	reg := newMockRegistry()
	fastProv := newMockProvider("fast")
	slowProv := newMockProvider("slow")
	reg.addProvider(&mockModule{StorageProvider: fastProv, info: contracts.ModuleInfo{ID: "fast"}}, "fast")
	reg.addProvider(&mockModule{StorageProvider: slowProv, info: contracts.ModuleInfo{ID: "slow"}}, "slow")
	orch := NewOrchestrator(reg)
	orch.DiscoverStorage()
	orch.AddPolicy(RoutingPolicy{Name: "hot", Prefix: "hot/", Provider: "fast"})
	orch.AddPolicy(RoutingPolicy{Name: "cold", Prefix: "cold/", Provider: "slow"})
	ctx := context.Background()

	orch.Put(ctx, "hot/data", bytes.NewReader([]byte("fast-data")), 9)
	orch.Put(ctx, "cold/data", bytes.NewReader([]byte("slow-data")), 9)
	if _, ok := fastProv.data["hot/data"]; !ok {
		t.Error("expected hot/data in fast provider")
	}
	if _, ok := slowProv.data["cold/data"]; !ok {
		t.Error("expected cold/data in slow provider")
	}
}

func TestOrchestrator_CacheHit(t *testing.T) {
	reg := newMockRegistry()
	prov := newMockProvider("local")
	reg.addProvider(&mockModule{StorageProvider: prov, info: contracts.ModuleInfo{ID: "local"}}, "local")
	orch := NewOrchestrator(reg)
	orch.DiscoverStorage()

	// Set a mock cache that returns cached data.
	mockCache := &mockCacheLayer{data: map[string][]byte{}}
	orch.SetCache(mockCache)
	ctx := context.Background()

	// Put real data in storage first (write-through populates cache).
	orch.Put(ctx, "cached", bytes.NewReader([]byte("from-storage")), 12)

	// Override cache with different data to verify cache-hit path.
	mockCache.data["cached"] = []byte("from-cache")

	// Get should return cached data (cache hit, not storage).
	rc, err := orch.Get(ctx, "cached")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	buf, _ := io.ReadAll(rc)
	if string(buf) != "from-cache" {
		t.Errorf("expected 'from-cache' from cache hit, got %q", string(buf))
	}
}

// mockCacheLayer implements contracts.CacheLayer for testing.
type mockCacheLayer struct {
	data map[string][]byte
}

func (m *mockCacheLayer) Get(ctx context.Context, key string) ([]byte, bool) {
	d, ok := m.data[key]
	return d, ok
}
func (m *mockCacheLayer) Set(ctx context.Context, key string, data []byte) error {
	m.data[key] = data
	return nil
}
func (m *mockCacheLayer) Invalidate(ctx context.Context, prefix string) error {
	for k := range m.data {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(m.data, k)
		}
	}
	return nil
}

func TestOrchestrator_CapabilityCheck(t *testing.T) {
	reg := newMockRegistry()
	prov := newMockProvider("local")
	reg.addProvider(&mockModule{StorageProvider: prov, info: contracts.ModuleInfo{ID: "local"}}, "local")
	orch := NewOrchestrator(reg)
	orch.DiscoverStorage()
	ctx := context.Background()

	orch.Put(ctx, "check", bytes.NewReader([]byte("x")), 1)
	caps, err := orch.CapabilityCheck(ctx, "check")
	if err != nil {
		t.Fatalf("CapabilityCheck: %v", err)
	}
	if len(caps) != 0 {
		t.Errorf("expected 0 capabilities, got %v", caps)
	}
}

func TestOrchestrator_ProviderCount(t *testing.T) {
	reg := newMockRegistry()
	orch := NewOrchestrator(reg)
	if orch.ProviderCount() != 0 {
		t.Errorf("expected 0, got %d", orch.ProviderCount())
	}
	prov := newMockProvider("a")
	reg.addProvider(&mockModule{StorageProvider: prov, info: contracts.ModuleInfo{ID: "a"}}, "a")
	orch.DiscoverStorage()
	if orch.ProviderCount() != 1 {
		t.Errorf("expected 1, got %d", orch.ProviderCount())
	}
}

// streamableMockProvider extends mockProvider with Streamable support.
type streamableMockProvider struct {
	*mockProvider
}

func (s *streamableMockProvider) Stream(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	buf, ok := s.data[key]
	if !ok {
		return nil, fmt.Errorf("key %q not found", key)
	}
	start := offset
	end := offset + length
	if start < 0 {
		start = 0
	}
	if end > int64(len(buf)) || length <= 0 {
		end = int64(len(buf))
	}
	if start > int64(len(buf)) {
		start = int64(len(buf))
	}
	return io.NopCloser(bytes.NewReader(buf[start:end])), nil
}

func TestOrchestrator_Stream(t *testing.T) {
	reg := newMockRegistry()
	prov := newMockProvider("local")
	reg.addProvider(&mockModule{StorageProvider: prov, info: contracts.ModuleInfo{ID: "local"}}, "local")

	orch := NewOrchestrator(reg)
	orch.DiscoverStorage()
	ctx := context.Background()

	data := []byte("hello stream world")
	orch.Put(ctx, "test-key", bytes.NewReader(data), int64(len(data)))

	// Stream should work even without Streamable (fallback path).
	rc, err := orch.Stream(ctx, "test-key", 6, 6)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer rc.Close()
	result, _ := io.ReadAll(rc)
	if string(result) != "stream" {
		t.Errorf("expected 'stream', got %q", string(result))
	}
}

func TestOrchestrator_StreamWithStreamableProvider(t *testing.T) {
	reg := newMockRegistry()
	base := newMockProvider("local")
	prov := &streamableMockProvider{mockProvider: base}
	reg.addProvider(&mockModule{StorageProvider: prov, info: contracts.ModuleInfo{ID: "local"}}, "local")

	orch := NewOrchestrator(reg)
	orch.DiscoverStorage()
	ctx := context.Background()

	data := []byte("hello stream world")
	orch.Put(ctx, "test-key", bytes.NewReader(data), int64(len(data)))

	rc, err := orch.Stream(ctx, "test-key", 6, 6)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer rc.Close()
	result, _ := io.ReadAll(rc)
	if string(result) != "stream" {
		t.Errorf("expected 'stream', got %q", string(result))
	}
}

func TestOrchestrator_StreamOutOfBounds(t *testing.T) {
	reg := newMockRegistry()
	prov := newMockProvider("local")
	reg.addProvider(&mockModule{StorageProvider: prov, info: contracts.ModuleInfo{ID: "local"}}, "local")

	orch := NewOrchestrator(reg)
	orch.DiscoverStorage()
	ctx := context.Background()

	data := []byte("hello")
	orch.Put(ctx, "test-key", bytes.NewReader(data), int64(len(data)))

	// Offset beyond data length should return empty.
	rc, err := orch.Stream(ctx, "test-key", 100, 10)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer rc.Close()
	result, _ := io.ReadAll(rc)
	if len(result) != 0 {
		t.Errorf("expected empty result, got %q", string(result))
	}
}

func TestOrchestrator_StreamMissingKey(t *testing.T) {
	reg := newMockRegistry()
	prov := newMockProvider("local")
	reg.addProvider(&mockModule{StorageProvider: prov, info: contracts.ModuleInfo{ID: "local"}}, "local")

	orch := NewOrchestrator(reg)
	orch.DiscoverStorage()
	ctx := context.Background()

	_, err := orch.Stream(ctx, "missing", 0, 10)
	if err == nil {
		t.Error("expected error for missing key, got nil")
	}
}

// --- Extended mocks for tiered + cache tests ---

type tieredMockProvider struct {
	*mockProvider
	tier contracts.StorageTier
}

func (t *tieredMockProvider) Tier() contracts.StorageTier { return t.tier }
func (t *tieredMockProvider) Promote(ctx context.Context, key string) error {
	if _, ok := t.data[key]; !ok {
		return contracts.ErrNotFound
	}
	return nil
}
func (t *tieredMockProvider) Relegate(ctx context.Context, key string) error {
	if _, ok := t.data[key]; !ok {
		return contracts.ErrNotFound
	}
	return nil
}
func (t *tieredMockProvider) Info() contracts.ModuleInfo {
	return contracts.ModuleInfo{ID: t.id + "-mod", Roles: []string{"storage"}}
}
func (t *tieredMockProvider) Init(ctx context.Context) error   { return nil }
func (t *tieredMockProvider) Start(ctx context.Context) error  { return nil }
func (t *tieredMockProvider) Stop(ctx context.Context) error   { return nil }
func (t *tieredMockProvider) Health(ctx context.Context) error { return nil }

// cacheOnlyModule implements contracts.Module + contracts.CacheLayer
// without being a StorageProvider (avoids Get signature conflict).
type cacheOnlyModule struct {
	id    string
	cache map[string][]byte
}

func (c *cacheOnlyModule) Info() contracts.ModuleInfo {
	return contracts.ModuleInfo{ID: c.id, Roles: []string{"cache"}, Capabilities: []string{"cache.local"}}
}
func (c *cacheOnlyModule) Init(ctx context.Context) error   { return nil }
func (c *cacheOnlyModule) Start(ctx context.Context) error  { return nil }
func (c *cacheOnlyModule) Stop(ctx context.Context) error   { return nil }
func (c *cacheOnlyModule) Health(ctx context.Context) error { return nil }

func (c *cacheOnlyModule) Get(_ context.Context, key string) ([]byte, bool) {
	if c.cache == nil {
		return nil, false
	}
	v, ok := c.cache[key]
	return v, ok
}
func (c *cacheOnlyModule) Set(_ context.Context, key string, data []byte) error {
	if c.cache == nil {
		c.cache = make(map[string][]byte)
	}
	c.cache[key] = data
	return nil
}
func (c *cacheOnlyModule) Invalidate(_ context.Context, prefix string) error {
	for k := range c.cache {
		if strings.HasPrefix(k, prefix) {
			delete(c.cache, k)
		}
	}
	return nil
}

type moduleWithRoles struct {
	contracts.StorageProvider
	id    string
	roles []string
	caps  []string
}

func (m *moduleWithRoles) Info() contracts.ModuleInfo {
	return contracts.ModuleInfo{ID: m.id, Roles: m.roles, Capabilities: m.caps}
}
func (m *moduleWithRoles) Init(ctx context.Context) error   { return nil }
func (m *moduleWithRoles) Start(ctx context.Context) error  { return nil }
func (m *moduleWithRoles) Stop(ctx context.Context) error   { return nil }
func (m *moduleWithRoles) Health(ctx context.Context) error { return nil }

// mockRegistryWithCapabilities extends mockRegistry to support FindByCapability.
type mockRegistryWithCaps struct {
	providers map[string]contracts.ModuleEntry
}

func newMockRegistryWithCaps() *mockRegistryWithCaps {
	return &mockRegistryWithCaps{providers: make(map[string]contracts.ModuleEntry)}
}

func (r *mockRegistryWithCaps) addProvider(prov contracts.StorageProvider, id string, roles []string, caps []string) {
	entry := contracts.ModuleEntry{
		Info: contracts.ModuleInfo{ID: id, Roles: roles, Capabilities: caps},
	}
	if m, ok := prov.(contracts.Module); ok {
		entry.Module = m
	} else {
		entry.Module = &moduleWithRoles{StorageProvider: prov, id: id, roles: roles, caps: caps}
	}
	r.providers[id] = entry
}

func (r *mockRegistryWithCaps) addModule(mod contracts.Module, id string, roles []string, caps []string) {
	r.providers[id] = contracts.ModuleEntry{
		Info:   contracts.ModuleInfo{ID: id, Roles: roles, Capabilities: caps},
		Module: mod,
	}
}

func (r *mockRegistryWithCaps) FindByRole(role string) []contracts.ModuleEntry {
	var result []contracts.ModuleEntry
	for _, e := range r.providers {
		for _, r := range e.Info.Roles {
			if r == role {
				result = append(result, e)
			}
		}
	}
	return result
}

func (r *mockRegistryWithCaps) FindByCapability(capability string) []contracts.ModuleEntry {
	var result []contracts.ModuleEntry
	for _, e := range r.providers {
		for _, c := range e.Info.Capabilities {
			if c == capability {
				result = append(result, e)
			}
		}
	}
	return result
}
func (r *mockRegistryWithCaps) Resolve(id string) (contracts.ModuleEntry, error) {
	e, ok := r.providers[id]
	if !ok {
		return contracts.ModuleEntry{}, fmt.Errorf("not found")
	}
	return e, nil
}
func (r *mockRegistryWithCaps) SupportsCapability(moduleID, capability string) bool { return false }
func (r *mockRegistryWithCaps) ListAll() []contracts.ModuleEntry {
	result := make([]contracts.ModuleEntry, 0, len(r.providers))
	for _, e := range r.providers {
		result = append(result, e)
	}
	return result
}
func (r *mockRegistryWithCaps) StartupOrder() ([]string, error) {
	ids := make([]string, 0, len(r.providers))
	for id := range r.providers {
		ids = append(ids, id)
	}
	return ids, nil
}
func (r *mockRegistryWithCaps) DependencyGraph(id string) ([]string, error) { return nil, nil }

func TestOrchestrator_DiscoverStorage_SidecarDialer(t *testing.T) {
	reg := newMockRegistryWithCaps()
	// Proxy module: registered in the mesh but not an in-process StorageProvider.
	proxy := &cacheOnlyModule{id: "storage-s3"}
	reg.addModule(proxy, "storage-s3", []string{"storage"}, []string{"storage", "storage.s3"})
	e := reg.providers["storage-s3"]
	e.Info.HTTPAddr = "127.0.0.1:9610"
	reg.providers["storage-s3"] = e

	dialed := false
	orch := NewOrchestrator(reg)
	orch.SetSidecarDialer(func(moduleID, addr string) (contracts.StorageProvider, error) {
		if moduleID != "storage-s3" || addr != "127.0.0.1:9610" {
			t.Fatalf("unexpected dial %s @ %s", moduleID, addr)
		}
		dialed = true
		return newMockProvider("storage-s3"), nil
	})
	if err := orch.DiscoverStorage(); err != nil {
		t.Fatal(err)
	}
	if !dialed {
		t.Fatal("expected sidecar dialer to be invoked")
	}
	if n := orch.ProviderCount(); n != 1 {
		t.Fatalf("ProviderCount=%d, want 1", n)
	}

	data := []byte("from-s3")
	if err := orch.Put(context.Background(), "obj", bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatalf("Put via dialed provider: %v", err)
	}
}

func TestOrchestrator_DiscoverStorage_SidecarNoDialerSkipped(t *testing.T) {
	reg := newMockRegistryWithCaps()
	proxy := &cacheOnlyModule{id: "storage-s3"}
	reg.addModule(proxy, "storage-s3", []string{"storage"}, []string{"storage"})
	e := reg.providers["storage-s3"]
	e.Info.HTTPAddr = "127.0.0.1:9610"
	reg.providers["storage-s3"] = e

	orch := NewOrchestrator(reg)
	_ = orch.DiscoverStorage()
	if n := orch.ProviderCount(); n != 0 {
		t.Fatalf("ProviderCount=%d, want 0 without dialer", n)
	}
}

// Tests for previously uncovered orchestrator functions

func TestOrchestrator_DiscoverCache_Found(t *testing.T) {
	reg := newMockRegistryWithCaps()
	cacheMod := &cacheOnlyModule{id: "cache-prov"}
	reg.addModule(cacheMod, "cache-prov", []string{"cache"}, []string{"cache.local"})

	orch := NewOrchestrator(reg)
	orch.DiscoverCache()

	if orch.cache == nil {
		t.Fatal("expected cache layer to be set after DiscoverCache")
	}
}

func TestOrchestrator_DiscoverCache_FromStorageRole(t *testing.T) {
	reg := newMockRegistryWithCaps()

	// A storage provider that also implements CacheLayer via combined type.
	type storageWithCache struct {
		*mockProvider
	}
	prov := &storageWithCache{mockProvider: newMockProvider("hybrid")}
	reg.addProvider(prov, "hybrid", []string{"storage"}, nil)

	orch := NewOrchestrator(reg)
	orch.DiscoverStorage()
	orch.DiscoverCache()

	// The hybrid provider doesn't implement CacheLayer (Get signature mismatch),
	// so cache should remain nil.
	if orch.cache != nil {
		t.Fatal("expected no cache for hybrid provider without CacheLayer interface")
	}
}

func TestOrchestrator_DiscoverCache_NotFound(t *testing.T) {
	reg := newMockRegistryWithCaps()
	prov := newMockProvider("plain-prov")
	reg.addProvider(prov, "plain-prov", []string{"storage"}, nil)

	orch := NewOrchestrator(reg)
	orch.DiscoverStorage()
	orch.DiscoverCache()

	if orch.cache != nil {
		t.Fatal("expected no cache layer when no cache module is registered")
	}
}

func TestOrchestrator_DiscoverTiers(t *testing.T) {
	reg := newMockRegistryWithCaps()
	hot := &tieredMockProvider{mockProvider: newMockProvider("hot-prov"), tier: contracts.StorageTierHot}
	warm := &tieredMockProvider{mockProvider: newMockProvider("warm-prov"), tier: contracts.StorageTierWarm}
	reg.addProvider(hot, "hot-prov", []string{"storage"}, nil)
	reg.addProvider(warm, "warm-prov", []string{"storage"}, nil)

	orch := NewOrchestrator(reg)
	orch.DiscoverStorage()
	orch.DiscoverTiers()

	orch.mu.RLock()
	hotTier, hotOK := orch.tiers["hot-prov"]
	warmTier, warmOK := orch.tiers["warm-prov"]
	orch.mu.RUnlock()

	if !hotOK {
		t.Error("expected hot-prov in tiers map")
	} else if hotTier != contracts.StorageTierHot {
		t.Errorf("expected hot tier, got %s", hotTier)
	}
	if !warmOK {
		t.Error("expected warm-prov in tiers map")
	} else if warmTier != contracts.StorageTierWarm {
		t.Errorf("expected warm tier, got %s", warmTier)
	}
}

func TestOrchestrator_Promote(t *testing.T) {
	reg := newMockRegistryWithCaps()
	prov := &tieredMockProvider{mockProvider: newMockProvider("hot-prov"), tier: contracts.StorageTierHot}
	reg.addProvider(prov, "hot-prov", []string{"storage"}, nil)

	orch := NewOrchestrator(reg)
	orch.DiscoverStorage()

	ctx := context.Background()
	orch.Put(ctx, "test-key", bytes.NewReader([]byte("data")), 4)

	err := orch.Promote(ctx, "test-key")
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
}

func TestOrchestrator_Promote_NotFound(t *testing.T) {
	reg := newMockRegistryWithCaps()
	prov := &tieredMockProvider{mockProvider: newMockProvider("hot-prov"), tier: contracts.StorageTierHot}
	reg.addProvider(prov, "hot-prov", []string{"storage"}, nil)

	orch := NewOrchestrator(reg)
	orch.DiscoverStorage()

	err := orch.Promote(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent key")
	}
}

func TestOrchestrator_Relegate(t *testing.T) {
	reg := newMockRegistryWithCaps()
	prov := &tieredMockProvider{mockProvider: newMockProvider("cold-prov"), tier: contracts.StorageTierCold}
	reg.addProvider(prov, "cold-prov", []string{"storage"}, nil)

	orch := NewOrchestrator(reg)
	orch.DiscoverStorage()

	ctx := context.Background()
	orch.Put(ctx, "test-key", bytes.NewReader([]byte("data")), 4)

	err := orch.Relegate(ctx, "test-key")
	if err != nil {
		t.Fatalf("Relegate: %v", err)
	}
}

func TestOrchestrator_SetTimeouts(t *testing.T) {
	reg := newMockRegistryWithCaps()
	prov := newMockProvider("timeout-prov")
	reg.addProvider(prov, "timeout-prov", []string{"storage"}, nil)

	orch := NewOrchestrator(reg)
	orch.DiscoverStorage()

	timeouts := Timeouts{
		Read:   5 * time.Second,
		Write:  10 * time.Second,
		Delete: 3 * time.Second,
	}
	orch.SetTimeouts(timeouts)

	// Verify by doing an operation (no panic, timing enforced by context).
	ctx := context.Background()
	err := orch.Put(ctx, "key", bytes.NewReader([]byte("data")), 4)
	if err != nil {
		t.Fatalf("Put after SetTimeouts: %v", err)
	}
}

func TestOrchestrator_WatchModules_RegistersNewProvider(t *testing.T) {
	reg := newMockRegistryWithCaps()
	bus := events.NewMemoryBus()
	bus.SetPublishPolicy(permissivePolicy{})

	orch := NewOrchestrator(reg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_ = orch.WatchModules(ctx, bus)

	// Publish a module.registered event for a storage provider.
	payload, _ := json.Marshal(contracts.ModuleRegisteredPayload{ModuleID: "new-prov"})
	bus.Publish(ctx, contracts.Event{
		Type:    contracts.EventModuleRegistered,
		Source:  "new-prov",
		Payload: payload,
	})

	time.Sleep(200 * time.Millisecond)

	// The module was published but isn't in the mock registry, so it won't
	// be added to orch.providers. We're testing that WatchModules doesn't
	// panic, not that it resolves modules (that requires a real registry).
	// This at least exercises the subscription and handler path.
}

// permissivePolicy allows all event publication for testing.
type permissivePolicy struct{}

func (permissivePolicy) CanPublish(_ context.Context, _, _ string) (bool, error) { return true, nil }

func TestProviderTierRank(t *testing.T) {
	tests := []struct {
		tier contracts.StorageTier
		want int
	}{
		{contracts.StorageTierHot, 0},
		{contracts.StorageTierWarm, 1},
		{contracts.StorageTierCold, 2},
		{contracts.StorageTierArchive, 3},
		{"unknown", 1},
		{"", 1},
	}
	for _, tt := range tests {
		got := providerTierRank(tt.tier)
		if got != tt.want {
			t.Errorf("providerTierRank(%q) = %d, want %d", tt.tier, got, tt.want)
		}
	}
}

func TestIsNotFound(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{contracts.ErrNotFound, true},
		{fmt.Errorf("not found"), true},
		{fmt.Errorf("no such key"), true},
		{fmt.Errorf("does not exist"), true},
		{fmt.Errorf("key not found"), true},
		{fmt.Errorf("some other error"), false},
		{nil, false},
	}
	for _, tt := range tests {
		got := isNotFound(tt.err)
		if got != tt.want {
			t.Errorf("isNotFound(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

func TestSetProvider_AddsToPool(t *testing.T) {
	reg := newMockRegistryWithCaps()
	orch := NewOrchestrator(reg)

	prov := newMockProvider("direct")
	orch.SetProvider("direct-prov", prov)

	if orch.ProviderCount() != 1 {
		t.Errorf("expected 1 provider, got %d", orch.ProviderCount())
	}
}

func TestSetAuditLogger_NoPanic(t *testing.T) {
	reg := newMockRegistryWithCaps()
	orch := NewOrchestrator(reg)

	orch.SetAuditLogger(&storageAuditLogger{})
	orch.SetAuditLogger(nil)
}

type storageAuditLogger struct{}

func (s *storageAuditLogger) Log(ctx context.Context, _ contracts.AuditEntry) error { return nil }
func (s *storageAuditLogger) Query(_ context.Context, _ contracts.AuditFilter) ([]contracts.AuditEntry, error) {
	return nil, nil
}
func (s *storageAuditLogger) Export(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, nil
}
func (s *storageAuditLogger) VerifyChainIntegrity(_ context.Context, _, _ time.Time) (contracts.ChainVerificationResult, error) {
	return contracts.ChainVerificationResult{Valid: true}, nil
}
func (s *storageAuditLogger) VerifyAll(_ context.Context) (contracts.ChainVerificationResult, error) {
	return contracts.ChainVerificationResult{Valid: true}, nil
}

func TestMetricCounters_InitialZero(t *testing.T) {
	reg := newMockRegistryWithCaps()
	orch := NewOrchestrator(reg)

	if c := orch.PutCount(); c != 0 {
		t.Errorf("expected PutCount=0, got %d", c)
	}
	if c := orch.Count(); c != 0 {
		t.Errorf("expected Count=0, got %d", c)
	}
	if c := orch.DeleteCount(); c != 0 {
		t.Errorf("expected DeleteCount=0, got %d", c)
	}
	if c := orch.StatCount(); c != 0 {
		t.Errorf("expected StatCount=0, got %d", c)
	}
	if c := orch.ListCount(); c != 0 {
		t.Errorf("expected ListCount=0, got %d", c)
	}
	if c := orch.MoveCount(); c != 0 {
		t.Errorf("expected MoveCount=0, got %d", c)
	}
}

func TestMetricCounters_Increment(t *testing.T) {
	reg := newMockRegistryWithCaps()
	prov := newMockProvider("cnt")
	reg.addProvider(prov, "cnt", []string{"storage"}, nil)

	orch := NewOrchestrator(reg)
	orch.DiscoverStorage()

	ctx := context.Background()
	orch.Put(ctx, "k1", bytes.NewReader([]byte("a")), 1)
	orch.Put(ctx, "k2", bytes.NewReader([]byte("b")), 1)

	if c := orch.PutCount(); c != 2 {
		t.Errorf("expected PutCount=2 after 2 puts, got %d", c)
	}

	orch.Get(ctx, "k1")
	if c := orch.Count(); c != 1 {
		t.Errorf("expected Count=1, got %d", c)
	}

	orch.Delete(ctx, "k1")
	if c := orch.DeleteCount(); c != 1 {
		t.Errorf("expected DeleteCount=1, got %d", c)
	}

	orch.Stat(ctx, "k2")
	if c := orch.StatCount(); c != 1 {
		t.Errorf("expected StatCount=1, got %d", c)
	}

	orch.List(ctx, "")
	if c := orch.ListCount(); c != 1 {
		t.Errorf("expected ListCount=1, got %d", c)
	}

	orch.Move(ctx, "k2", "k3")
	if c := orch.MoveCount(); c != 1 {
		t.Errorf("expected MoveCount=1, got %d", c)
	}
}

func TestProviderInfo_Empty(t *testing.T) {
	reg := newMockRegistryWithCaps()
	orch := NewOrchestrator(reg)

	info := orch.ProviderInfo()
	if len(info) != 0 {
		t.Errorf("expected empty ProviderInfo, got %d items", len(info))
	}
}

func TestProviderInfo_WithProviders(t *testing.T) {
	reg := newMockRegistryWithCaps()
	prov := newMockProvider("p1")
	reg.addProvider(prov, "p1", []string{"storage"}, nil)

	orch := NewOrchestrator(reg)
	orch.DiscoverStorage()

	info := orch.ProviderInfo()
	if len(info) != 1 {
		t.Fatalf("expected 1 provider info, got %d", len(info))
	}
	if info[0].ID != "p1" {
		t.Errorf("expected ID p1, got %s", info[0].ID)
	}
}
