package registry

import (
	"context"
	"testing"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// Mock module implementing contracts.Module + contracts.StorageProvider
// (storage is a fabric interface that stays in core)
type mockModule struct {
	info contracts.ModuleInfo
}

func (m *mockModule) Info() contracts.ModuleInfo     { return m.info }
func (m *mockModule) Init(_ context.Context) error   { return nil }
func (m *mockModule) Start(_ context.Context) error  { return nil }
func (m *mockModule) Stop(_ context.Context) error   { return nil }
func (m *mockModule) Health(_ context.Context) error { return nil }

//nolint:unused // used via interface type assertions in test tables
//nolint:unused // used via interface type assertions in test tables
// ---------- Register tests ----------

func TestRegister(t *testing.T) {
	r := New()
	m := &mockModule{info: contracts.ModuleInfo{ID: "test", Name: "Test Module"}}

	if err := r.Register(m, nil); err != nil {
		t.Fatalf("Register should succeed: %v", err)
	}

	entry, err := r.Get("test")
	if err != nil {
		t.Fatalf("Get should succeed after Register: %v", err)
	}
	if entry.Module == nil {
		t.Fatal("expected non-nil Module reference")
	}
	if entry.State != contracts.ModuleStateRegistered {
		t.Fatalf("expected initial state %q, got %q", contracts.ModuleStateRegistered, entry.State)
	}
}

func TestRegisterPopulatesCapabilityIndex(t *testing.T) {
	r := New()
	m := &mockModule{info: contracts.ModuleInfo{
		ID: "test", Name: "Test",
		Capabilities: []string{"cap1", "cap2"},
	}}
	if err := r.Register(m, nil); err != nil {
		t.Fatal(err)
	}

	if !r.SupportsCapability("test", "cap1") {
		t.Fatal("module should support cap1")
	}
	if r.SupportsCapability("test", "cap-none") {
		t.Fatal("module should not support unregistered capability")
	}
	entries := r.ListByCapability("cap1")
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry for cap1, got %d", len(entries))
	}
}

func TestRegisterDuplicateID(t *testing.T) {
	r := New()
	r.Register(&mockModule{info: contracts.ModuleInfo{ID: "dup", Name: "First"}}, nil)
	err := r.Register(&mockModule{info: contracts.ModuleInfo{ID: "dup", Name: "Second"}}, nil)
	if err == nil {
		t.Fatal("expected error for duplicate module ID")
	}
}

func TestRegisterEmptyID(t *testing.T) {
	r := New()
	err := r.Register(&mockModule{info: contracts.ModuleInfo{Name: "NoID"}}, nil)
	if err == nil {
		t.Fatal("expected error for empty module ID")
	}
}

func TestRegisterEmptyName(t *testing.T) {
	r := New()
	err := r.Register(&mockModule{info: contracts.ModuleInfo{ID: "noname"}}, nil)
	if err == nil {
		t.Fatal("expected error for empty module name")
	}
}

func TestRegisterWithContracts(t *testing.T) {
	r := New()
	m := &mockModule{info: contracts.ModuleInfo{
		ID: "test", Name: "Test",
		Contracts: []contracts.ContractDeclaration{
			{Repo: "github.com/Muxcore-Media/contracts-downloader", Version: "v1.0.0", Interface: "Downloader"},
		},
	}}
	if err := r.Register(m, nil); err != nil {
		t.Fatal(err)
	}
	entry, _ := r.Get("test")
	if len(entry.Info.Contracts) != 1 {
		t.Fatalf("expected 1 contract, got %d", len(entry.Info.Contracts))
	}
}

// ---------- Unregister tests ----------

func TestUnregister(t *testing.T) {
	r := New()
	r.Register(&mockModule{info: contracts.ModuleInfo{
		ID: "test", Name: "Test", Capabilities: []string{"cap1"},
	}}, nil)

	if err := r.Unregister("test"); err != nil {
		t.Fatalf("Unregister should succeed: %v", err)
	}
	if _, err := r.Get("test"); err == nil {
		t.Fatal("expected error for unregistered module")
	}
	if entries := r.ListByCapability("cap1"); len(entries) != 0 {
		t.Fatalf("expected 0 entries after unregister, got %d", len(entries))
	}
}

func TestUnregisterNotFound(t *testing.T) {
	r := New()
	if err := r.Unregister("nonexistent"); err == nil {
		t.Fatal("expected error for nonexistent module")
	}
}

// ---------- ListByRole (string kinds) ----------

func TestListByRole(t *testing.T) {
	r := New()
	r.Register(&mockModule{info: contracts.ModuleInfo{
		ID: "a", Name: "A", Roles: []string{"downloader"},
	}}, nil)
	r.Register(&mockModule{info: contracts.ModuleInfo{
		ID: "b", Name: "B", Roles: []string{"indexer"},
	}}, nil)
	r.Register(&mockModule{info: contracts.ModuleInfo{
		ID: "c", Name: "C", Roles: []string{"downloader", "indexer"},
	}}, nil)

	if entries := r.ListByRole("downloader"); len(entries) != 2 {
		t.Fatalf("expected 2 entries for downloader, got %d", len(entries))
	}
	if entries := r.ListByRole("nonexistent"); len(entries) != 0 {
		t.Fatalf("expected 0 entries for nonexistent kind, got %d", len(entries))
	}
}

func TestListByCapability(t *testing.T) {
	r := New()
	r.Register(&mockModule{info: contracts.ModuleInfo{
		ID: "a", Name: "A", Capabilities: []string{"cap1", "cap2"},
	}}, nil)
	r.Register(&mockModule{info: contracts.ModuleInfo{
		ID: "b", Name: "B", Capabilities: []string{"cap1"},
	}}, nil)

	if entries := r.ListByCapability("cap1"); len(entries) != 2 {
		t.Fatalf("expected 2 entries for cap1, got %d", len(entries))
	}
	if entries := r.ListByCapability("cap-none"); len(entries) != 0 {
		t.Fatal("expected 0 entries for unknown capability")
	}
}

// ---------- ResolveDeps ----------

func TestResolveDeps(t *testing.T) {
	r := New()
	r.Register(&mockModule{info: contracts.ModuleInfo{ID: "B", Name: "B"}}, nil)
	r.Register(&mockModule{info: contracts.ModuleInfo{ID: "C", Name: "C"}}, nil)
	r.Register(&mockModule{info: contracts.ModuleInfo{ID: "A", Name: "A"}}, []string{"B", "C"})

	deps, err := r.ResolveDeps("A")
	if err != nil {
		t.Fatalf("ResolveDeps: %v", err)
	}
	if len(deps) != 2 {
		t.Fatalf("expected 2 deps, got %d", len(deps))
	}
}

func TestResolveDepsCircular(t *testing.T) {
	r := New()
	r.Register(&mockModule{info: contracts.ModuleInfo{ID: "A", Name: "A"}}, []string{"B"})
	r.Register(&mockModule{info: contracts.ModuleInfo{ID: "B", Name: "B"}}, []string{"A"})

	_, err := r.ResolveDeps("A")
	if err == nil {
		t.Fatal("expected error for circular dependency")
	}
}

func TestResolveDepsMissing(t *testing.T) {
	r := New()
	r.Register(&mockModule{info: contracts.ModuleInfo{ID: "A", Name: "A"}}, []string{"B"})

	_, err := r.ResolveDeps("A")
	if err == nil {
		t.Fatal("expected error for missing dependency")
	}
}

// ---------- Registry interface ----------

func TestFindByRole(t *testing.T) {
	r := New()
	r.Register(&mockModule{info: contracts.ModuleInfo{
		ID: "a", Name: "A", Roles: []string{"downloader"},
	}}, nil)

	entries := r.FindByRole("downloader")
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
}

func TestFindByCapability(t *testing.T) {
	r := New()
	r.Register(&mockModule{info: contracts.ModuleInfo{
		ID: "a", Name: "A", Capabilities: []string{"cap1"},
	}}, nil)

	entries := r.FindByCapability("cap1")
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
}

func TestResolve(t *testing.T) {
	r := New()
	r.Register(&mockModule{info: contracts.ModuleInfo{ID: "test", Name: "Test"}}, nil)
	entry, err := r.Resolve("test")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if entry.Module == nil {
		t.Fatal("expected non-nil Module")
	}
}

func TestResolveNotFound(t *testing.T) {
	r := New()
	if _, err := r.Resolve("nonexistent"); err == nil {
		t.Fatal("expected error")
	}
}

func TestListAll(t *testing.T) {
	r := New()
	r.Register(&mockModule{info: contracts.ModuleInfo{ID: "a", Name: "A"}}, nil)
	r.Register(&mockModule{info: contracts.ModuleInfo{ID: "b", Name: "B"}}, nil)

	entries := r.ListAll()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
}

func TestCount(t *testing.T) {
	r := New()
	if c := r.Count(); c != 0 {
		t.Fatalf("expected 0, got %d", c)
	}
	r.Register(&mockModule{info: contracts.ModuleInfo{ID: "a", Name: "A"}}, nil)
	if c := r.Count(); c != 1 {
		t.Fatalf("expected 1, got %d", c)
	}
}

func TestDiscover(t *testing.T) {
	r := New()
	r.Register(&mockModule{info: contracts.ModuleInfo{
		ID: "a", Name: "A", Roles: []string{"downloader"},
	}}, nil)

	infos := r.Discover(context.TODO(), "downloader")
	if len(infos) != 1 {
		t.Fatalf("expected 1 info, got %d", len(infos))
	}
	if len(r.Discover(context.TODO(), "nonexistent")) != 0 {
		t.Fatal("expected 0 infos for unmatched kind")
	}
}
