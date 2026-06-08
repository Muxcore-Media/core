package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

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
	r.providers[id] = contracts.ModuleEntry{
		Info:   contracts.ModuleInfo{ID: id, Kinds: []contracts.ModuleKind{contracts.ModuleKindStorage}},
		Module: prov.(contracts.Module),
	}
}

func (r *mockRegistry) FindByKind(kind contracts.ModuleKind) []contracts.ModuleEntry {
	var result []contracts.ModuleEntry
	for _, e := range r.providers {
		result = append(result, e)
	}
	return result
}
func (r *mockRegistry) FindByCapability(cap string) []contracts.ModuleEntry { return nil }
func (r *mockRegistry) SupportsCapability(moduleID, cap string) bool        { return false }
func (r *mockRegistry) Resolve(id string) (contracts.ModuleEntry, error) {
	e, ok := r.providers[id]
	if !ok {
		return contracts.ModuleEntry{}, fmt.Errorf("not found")
	}
	return e, nil
}
func (r *mockRegistry) ListAll() []contracts.ModuleEntry {
	var result []contracts.ModuleEntry
	for _, e := range r.providers {
		result = append(result, e)
	}
	return result
}
func (r *mockRegistry) RegisterMediaSchema(schema contracts.MediaTypeSchema) error { return nil }
func (r *mockRegistry) MediaSchema(mt contracts.MediaType) (contracts.MediaTypeSchema, bool) {
	return contracts.MediaTypeSchema{}, false
}
func (r *mockRegistry) MediaSchemas() []contracts.MediaTypeSchema { return nil }

type mockModule struct {
	contracts.StorageProvider
	info contracts.ModuleInfo
}

func (m *mockModule) Info() contracts.ModuleInfo   { return m.info }
func (m *mockModule) Init(ctx context.Context) error  { return nil }
func (m *mockModule) Start(ctx context.Context) error { return nil }
func (m *mockModule) Stop(ctx context.Context) error  { return nil }
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
	t.Skip("Cache is now provided by cache-memory module; tested via module integration")
}

func TestOrchestrator_CacheMiss(t *testing.T) {
	t.Skip("Cache is now provided by cache-memory module; cache integration tested via module integration tests")
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
