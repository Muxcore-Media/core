package registry

import (
	"context"
	"io"
	"testing"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// ---------------------------------------------------------------------------
// Mock module implementations
// ---------------------------------------------------------------------------

type mockModule struct {
	info contracts.ModuleInfo
}

func (m *mockModule) Info() contracts.ModuleInfo     { return m.info }
func (m *mockModule) Init(_ context.Context) error   { return nil }
func (m *mockModule) Start(_ context.Context) error  { return nil }
func (m *mockModule) Stop(_ context.Context) error   { return nil }
func (m *mockModule) Health(_ context.Context) error { return nil }

// mockDownloader implements contracts.Module + contracts.Downloader
type mockDownloader struct{ mockModule }

func (m *mockDownloader) Add(_ context.Context, _ contracts.DownloadTask) (string, error) {
	return "", nil
}
func (m *mockDownloader) Remove(_ context.Context, _ string, _ bool) error { return nil }
func (m *mockDownloader) Pause(_ context.Context, _ string) error          { return nil }
func (m *mockDownloader) Resume(_ context.Context, _ string) error         { return nil }
func (m *mockDownloader) Status(_ context.Context, _ string) (contracts.DownloadInfo, error) {
	return contracts.DownloadInfo{}, nil
}
func (m *mockDownloader) List(_ context.Context) ([]contracts.DownloadInfo, error) { return nil, nil }

// mockIndexer implements contracts.Module + contracts.Indexer
type mockIndexer struct{ mockModule }

func (m *mockIndexer) Name() string { return "mock" }
func (m *mockIndexer) Search(_ context.Context, _ contracts.SearchQuery) ([]contracts.IndexerResult, error) {
	return nil, nil
}
func (m *mockIndexer) Capabilities(_ context.Context) ([]string, error) { return nil, nil }

// mockAuthProvider implements contracts.Module + contracts.AuthProvider
type mockAuthProvider struct{ mockModule }

func (m *mockAuthProvider) Authenticate(_ context.Context, _ any) (contracts.Session, error) {
	return contracts.Session{}, nil
}
func (m *mockAuthProvider) Validate(_ context.Context, _ string) (contracts.Session, error) {
	return contracts.Session{}, nil
}
func (m *mockAuthProvider) Revoke(_ context.Context, _ string) error { return nil }

// mockPlayback implements contracts.Module + contracts.Playback
type mockPlayback struct{ mockModule }

func (m *mockPlayback) Ping(_ context.Context) error { return nil }
func (m *mockPlayback) GetSessions(_ context.Context) ([]contracts.PlaybackSession, error) {
	return nil, nil
}
func (m *mockPlayback) RefreshLibrary(_ context.Context) error                   { return nil }
func (m *mockPlayback) GetStreamURL(_ context.Context, _ string) (string, error) { return "", nil }

// mockMediaLibrary implements contracts.Module + contracts.MediaLibrary
type mockMediaLibrary struct{ mockModule }

func (m *mockMediaLibrary) Add(_ context.Context, _ contracts.MediaObject) error { return nil }
func (m *mockMediaLibrary) Remove(_ context.Context, _ string) error             { return nil }
func (m *mockMediaLibrary) Get(_ context.Context, _ string) (contracts.MediaObject, error) {
	return contracts.MediaObject{}, nil
}
func (m *mockMediaLibrary) List(_ context.Context, _ contracts.MediaType, _, _ int) ([]contracts.MediaObject, error) {
	return nil, nil
}
func (m *mockMediaLibrary) Search(_ context.Context, _ string) ([]contracts.MediaObject, error) {
	return nil, nil
}

// mockWorkflowEngine implements contracts.Module + contracts.WorkflowEngine
type mockWorkflowEngine struct{ mockModule }

func (m *mockWorkflowEngine) Define(_ context.Context, _ contracts.WorkflowDefinition) error {
	return nil
}
func (m *mockWorkflowEngine) Run(_ context.Context, _ string, _ map[string]any) (string, error) {
	return "", nil
}
func (m *mockWorkflowEngine) Status(_ context.Context, _ string) (contracts.WorkflowRun, error) {
	return contracts.WorkflowRun{}, nil
}
func (m *mockWorkflowEngine) Cancel(_ context.Context, _ string) error { return nil }

// mockStorageProvider implements contracts.Module + contracts.StorageProvider
type mockStorageProvider struct{ mockModule }

func (m *mockStorageProvider) Put(_ context.Context, _ string, _ io.Reader, _ int64) error {
	return nil
}
func (m *mockStorageProvider) Get(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, nil
}
func (m *mockStorageProvider) Delete(_ context.Context, _ string) error         { return nil }
func (m *mockStorageProvider) Move(_ context.Context, _, _ string) error        { return nil }
func (m *mockStorageProvider) Exists(_ context.Context, _ string) (bool, error) { return false, nil }
func (m *mockStorageProvider) Stat(_ context.Context, _ string) (contracts.ObjectInfo, error) {
	return contracts.ObjectInfo{}, nil
}
func (m *mockStorageProvider) List(_ context.Context, _ string) ([]contracts.ObjectInfo, error) {
	return nil, nil
}

// mockScheduler implements contracts.Module + contracts.Scheduler
type mockScheduler struct{ mockModule }

func (m *mockScheduler) Schedule(_ context.Context, _ contracts.Task) (string, error) { return "", nil }
func (m *mockScheduler) Cancel(_ context.Context, _ string) error                     { return nil }
func (m *mockScheduler) Status(_ context.Context, _ string) (contracts.TaskStatus, error) {
	return "", nil
}

// ---------------------------------------------------------------------------
// Register tests
// ---------------------------------------------------------------------------

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
	if entry.Health != nil {
		t.Fatal("expected nil health on fresh registration")
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
	if !r.SupportsCapability("test", "cap2") {
		t.Fatal("module should support cap2")
	}
	if r.SupportsCapability("test", "cap-none") {
		t.Fatal("module should not support unregistered capability")
	}
	if r.SupportsCapability("nonexistent", "cap1") {
		t.Fatal("non-existent module should not support cap1")
	}

	entries := r.ListByCapability("cap1")
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry for cap1, got %d", len(entries))
	}
}

func TestRegisterDuplicateID(t *testing.T) {
	r := New()
	if err := r.Register(&mockModule{info: contracts.ModuleInfo{ID: "dup", Name: "First"}}, nil); err != nil {
		t.Fatal(err)
	}
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

// ---------------------------------------------------------------------------
// Kind interface validation tests
// ---------------------------------------------------------------------------

func TestRegisterKindInterfaceMissing(t *testing.T) {
	r := New()
	// mockModule does NOT implement contracts.Downloader
	m := &mockModule{info: contracts.ModuleInfo{
		ID: "bad", Name: "Bad",
		Kinds: []contracts.ModuleKind{contracts.ModuleKindDownloader},
	}}
	err := r.Register(m, nil)
	if err == nil {
		t.Fatal("expected error when module claims downloader kind but does not implement Downloader")
	}
}

func TestRegisterKindInterfaceValid(t *testing.T) {
	r := New()
	m := &mockDownloader{
		mockModule: mockModule{info: contracts.ModuleInfo{
			ID: "good", Name: "Good Downloader",
			Kinds: []contracts.ModuleKind{contracts.ModuleKindDownloader},
		}},
	}
	if err := r.Register(m, nil); err != nil {
		t.Fatalf("registration with valid implementation should succeed: %v", err)
	}
}

// Kinds for all required interfaces are validated successfully.
func TestRegisterAllKindInterfaces(t *testing.T) {
	tests := []struct {
		name string
		kind contracts.ModuleKind
		mod  contracts.Module
	}{
		{"downloader", contracts.ModuleKindDownloader, &mockDownloader{}},
		{"indexer", contracts.ModuleKindIndexer, &mockIndexer{}},
		{"auth", contracts.ModuleKindAuth, &mockAuthProvider{}},
		{"playback", contracts.ModuleKindPlayback, &mockPlayback{}},
		{"media_manager", contracts.ModuleKindMediaManager, &mockMediaLibrary{}},
		{"workflow", contracts.ModuleKindWorkflow, &mockWorkflowEngine{}},
		{"storage", contracts.ModuleKindStorage, &mockStorageProvider{}},
		{"scheduler", contracts.ModuleKindScheduler, &mockScheduler{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New()
			// embed the mock inside a module we control so we can set Info
			mm := &mockModule{info: contracts.ModuleInfo{
				ID: tt.name, Name: tt.name,
				Kinds: []contracts.ModuleKind{tt.kind},
			}}
			// We need to "attach" the kind methods to the mock. Since we can't
			// dynamically extend types, use the specific mock type directly.
			var mod contracts.Module
			switch tt.kind {
			case contracts.ModuleKindDownloader:
				mod = &mockDownloader{mockModule: *mm}
			case contracts.ModuleKindIndexer:
				mod = &mockIndexer{mockModule: *mm}
			case contracts.ModuleKindAuth:
				mod = &mockAuthProvider{mockModule: *mm}
			case contracts.ModuleKindPlayback:
				mod = &mockPlayback{mockModule: *mm}
			case contracts.ModuleKindMediaManager:
				mod = &mockMediaLibrary{mockModule: *mm}
			case contracts.ModuleKindWorkflow:
				mod = &mockWorkflowEngine{mockModule: *mm}
			case contracts.ModuleKindStorage:
				mod = &mockStorageProvider{mockModule: *mm}
			case contracts.ModuleKindScheduler:
				mod = &mockScheduler{mockModule: *mm}
			default:
				mod = mm
			}
			if err := r.Register(mod, nil); err != nil {
				t.Fatalf("kind %q should register successfully: %v", tt.kind, err)
			}
		})
	}
}

// Kinds that have no interface requirement should skip validation.
func TestRegisterUnknownKind(t *testing.T) {
	r := New()
	m := &mockModule{info: contracts.ModuleInfo{
		ID: "unknown-kind", Name: "Unknown Kind",
		Kinds: []contracts.ModuleKind{contracts.ModuleKindProvider},
	}}
	// ModuleKindProvider is not in kindInterfaceMap, so validation is skipped.
	if err := r.Register(m, nil); err != nil {
		t.Fatalf("kind not in interface map should skip validation: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Unregister tests
// ---------------------------------------------------------------------------

func TestUnregister(t *testing.T) {
	r := New()
	r.Register(&mockModule{info: contracts.ModuleInfo{
		ID: "test", Name: "Test", Capabilities: []string{"cap1"},
	}}, nil)

	if err := r.Unregister("test"); err != nil {
		t.Fatalf("Unregister should succeed: %v", err)
	}

	if _, err := r.Get("test"); err == nil {
		t.Fatal("expected error when getting unregistered module")
	}

	// Capability index should be cleaned up.
	if entries := r.ListByCapability("cap1"); len(entries) != 0 {
		t.Fatalf("expected 0 entries for cap1 after unregister, got %d", len(entries))
	}
}

func TestUnregisterNotFound(t *testing.T) {
	r := New()
	if err := r.Unregister("nonexistent"); err == nil {
		t.Fatal("expected error for unregistering nonexistent module")
	}
}

// ---------------------------------------------------------------------------
// Get / List tests
// ---------------------------------------------------------------------------

func TestGetNotFound(t *testing.T) {
	r := New()
	if _, err := r.Get("nonexistent"); err == nil {
		t.Fatal("expected error for nonexistent module")
	}
}

func TestList(t *testing.T) {
	r := New()
	r.Register(&mockModule{info: contracts.ModuleInfo{ID: "a", Name: "A"}}, nil)
	r.Register(&mockModule{info: contracts.ModuleInfo{ID: "b", Name: "B"}}, nil)

	entries := r.List()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
}

func TestListEmpty(t *testing.T) {
	r := New()
	if entries := r.List(); len(entries) != 0 {
		t.Fatalf("expected empty list, got %d entries", len(entries))
	}
}

// ---------------------------------------------------------------------------
// ListByKind / ListByCapability tests
// ---------------------------------------------------------------------------

func TestListByKind(t *testing.T) {
	r := New()
	// Use kinds NOT in kindInterfaceMap to avoid interface validation.
	r.Register(&mockModule{info: contracts.ModuleInfo{
		ID: "a", Name: "A", Kinds: []contracts.ModuleKind{contracts.ModuleKindProvider},
	}}, nil)
	r.Register(&mockModule{info: contracts.ModuleInfo{
		ID: "b", Name: "B", Kinds: []contracts.ModuleKind{contracts.ModuleKindUI},
	}}, nil)
	r.Register(&mockModule{info: contracts.ModuleInfo{
		ID: "c", Name: "C", Kinds: []contracts.ModuleKind{contracts.ModuleKindProvider},
	}}, nil)

	if entries := r.ListByKind(contracts.ModuleKindProvider); len(entries) != 2 {
		t.Fatalf("expected 2 entries for provider kind, got %d", len(entries))
	}
	if entries := r.ListByKind(contracts.ModuleKindUI); len(entries) != 1 {
		t.Fatalf("expected 1 entry for ui kind, got %d", len(entries))
	}
	if entries := r.ListByKind(contracts.ModuleKindDownloader); len(entries) != 0 {
		t.Fatalf("expected 0 entries for downloader kind, got %d", len(entries))
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
	if entries := r.ListByCapability("cap2"); len(entries) != 1 {
		t.Fatalf("expected 1 entry for cap2, got %d", len(entries))
	}
	if entries := r.ListByCapability("cap-none"); len(entries) != 0 {
		t.Fatalf("expected 0 entries for unknown capability, got %d", len(entries))
	}
}

// ---------------------------------------------------------------------------
// SetState / SetHealth tests
// ---------------------------------------------------------------------------

func TestSetState(t *testing.T) {
	r := New()
	r.Register(&mockModule{info: contracts.ModuleInfo{ID: "test", Name: "Test"}}, nil)

	if err := r.SetState("test", contracts.ModuleStateRunning); err != nil {
		t.Fatalf("SetState should succeed: %v", err)
	}

	entry, _ := r.Get("test")
	if entry.State != contracts.ModuleStateRunning {
		t.Fatalf("expected state %q, got %q", contracts.ModuleStateRunning, entry.State)
	}
}

func TestSetStateNotFound(t *testing.T) {
	r := New()
	if err := r.SetState("nonexistent", contracts.ModuleStateRunning); err == nil {
		t.Fatal("expected error for nonexistent module")
	}
}

func TestSetHealth(t *testing.T) {
	r := New()
	r.Register(&mockModule{info: contracts.ModuleInfo{ID: "test", Name: "Test"}}, nil)

	if err := r.SetHealth("test", nil); err != nil {
		t.Fatalf("SetHealth(nil) should succeed: %v", err)
	}

	entry, _ := r.Get("test")
	if entry.Health != nil {
		t.Fatal("expected nil health")
	}

	testErr := io.ErrUnexpectedEOF
	if err := r.SetHealth("test", testErr); err != nil {
		t.Fatalf("SetHealth(error) should succeed: %v", err)
	}

	entry, _ = r.Get("test")
	if entry.Health != testErr {
		t.Fatal("expected health to match the set error")
	}
}

func TestSetHealthNotFound(t *testing.T) {
	r := New()
	if err := r.SetHealth("nonexistent", nil); err == nil {
		t.Fatal("expected error for nonexistent module")
	}
}

// ---------------------------------------------------------------------------
// ResolveDeps tests
// ---------------------------------------------------------------------------

func TestResolveDeps(t *testing.T) {
	r := New()
	r.Register(&mockModule{info: contracts.ModuleInfo{ID: "B", Name: "B"}}, nil)
	r.Register(&mockModule{info: contracts.ModuleInfo{ID: "C", Name: "C"}}, nil)
	r.Register(&mockModule{info: contracts.ModuleInfo{ID: "A", Name: "A"}}, []string{"B", "C"})

	deps, err := r.ResolveDeps("A")
	if err != nil {
		t.Fatalf("ResolveDeps should succeed: %v", err)
	}

	if len(deps) != 2 {
		t.Fatalf("expected 2 deps, got %d: %v", len(deps), deps)
	}
	// Order depends on map iteration; check set membership.
	found := make(map[string]bool)
	for _, d := range deps {
		found[d] = true
	}
	if !found["B"] || !found["C"] {
		t.Fatalf("expected deps to contain B and C, got %v", deps)
	}
}

func TestResolveDepsTransitive(t *testing.T) {
	r := New()
	r.Register(&mockModule{info: contracts.ModuleInfo{ID: "D", Name: "D"}}, nil)
	r.Register(&mockModule{info: contracts.ModuleInfo{ID: "C", Name: "C"}}, []string{"D"})
	r.Register(&mockModule{info: contracts.ModuleInfo{ID: "B", Name: "B"}}, []string{"D"})
	r.Register(&mockModule{info: contracts.ModuleInfo{ID: "A", Name: "A"}}, []string{"B", "C"})

	deps, err := r.ResolveDeps("A")
	if err != nil {
		t.Fatalf("ResolveDeps should succeed: %v", err)
	}

	expected := map[string]bool{"B": true, "C": true, "D": true}
	for _, d := range deps {
		delete(expected, d)
	}
	if len(expected) != 0 {
		t.Fatalf("expected deps {B, C, D}, got %v; missing: %v", deps, expected)
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

func TestResolveDepsNotFound(t *testing.T) {
	r := New()
	_, err := r.ResolveDeps("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent module")
	}
}

func TestResolveDepsSelfDependency(t *testing.T) {
	r := New()
	r.Register(&mockModule{info: contracts.ModuleInfo{ID: "A", Name: "A"}}, []string{"A"})

	_, err := r.ResolveDeps("A")
	if err == nil {
		t.Fatal("expected error for self-referencing dependency")
	}
}

func TestResolveDepsNoDeps(t *testing.T) {
	r := New()
	r.Register(&mockModule{info: contracts.ModuleInfo{ID: "A", Name: "A"}}, nil)

	deps, err := r.ResolveDeps("A")
	if err != nil {
		t.Fatalf("ResolveDeps should succeed: %v", err)
	}
	if len(deps) != 0 {
		t.Fatalf("expected 0 deps for module with no dependencies, got %d", len(deps))
	}
}

// ---------------------------------------------------------------------------
// ServiceRegistry interface methods
// ---------------------------------------------------------------------------

func TestFindByKind(t *testing.T) {
	r := New()
	r.Register(&mockModule{info: contracts.ModuleInfo{
		ID: "a", Name: "A", Kinds: []contracts.ModuleKind{contracts.ModuleKindProvider},
	}}, nil)
	r.Register(&mockModule{info: contracts.ModuleInfo{
		ID: "b", Name: "B", Kinds: []contracts.ModuleKind{contracts.ModuleKindUI},
	}}, nil)

	entries := r.FindByKind(contracts.ModuleKindProvider)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Info.ID != "a" {
		t.Fatalf("expected module ID a, got %s", entries[0].Info.ID)
	}
	if entries[0].Module == nil {
		t.Fatal("expected non-nil Module in ModuleEntry")
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
	if entries[0].Info.ID != "a" {
		t.Fatalf("expected module ID a, got %s", entries[0].Info.ID)
	}
	if len(r.FindByCapability("cap-none")) != 0 {
		t.Fatal("expected 0 entries for unknown capability")
	}
}

func TestSupportsCapability(t *testing.T) {
	r := New()
	r.Register(&mockModule{info: contracts.ModuleInfo{
		ID: "a", Name: "A", Capabilities: []string{"cap1"},
	}}, nil)

	if !r.SupportsCapability("a", "cap1") {
		t.Fatal("module a should support cap1")
	}
	if r.SupportsCapability("a", "cap-none") {
		t.Fatal("module a should not support unregistered cap")
	}
	if r.SupportsCapability("b", "cap1") {
		t.Fatal("non-existent module should not support cap1")
	}
}

func TestResolve(t *testing.T) {
	r := New()
	r.Register(&mockModule{info: contracts.ModuleInfo{ID: "test", Name: "Test"}}, nil)

	entry, err := r.Resolve("test")
	if err != nil {
		t.Fatalf("Resolve should succeed: %v", err)
	}
	if entry.Info.ID != "test" {
		t.Fatalf("expected module ID test, got %s", entry.Info.ID)
	}
	if entry.Module == nil {
		t.Fatal("expected non-nil Module")
	}
}

func TestResolveNotFound(t *testing.T) {
	r := New()
	if _, err := r.Resolve("nonexistent"); err == nil {
		t.Fatal("expected error for nonexistent module")
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

	ids := make(map[string]bool)
	for _, e := range entries {
		ids[e.Info.ID] = true
	}
	if !ids["a"] || !ids["b"] {
		t.Fatal("ListAll should contain both modules")
	}
}

func TestListAllEmpty(t *testing.T) {
	r := New()
	if entries := r.ListAll(); len(entries) != 0 {
		t.Fatalf("expected empty list, got %d entries", len(entries))
	}
}

func TestCount(t *testing.T) {
	r := New()
	if c := r.Count(); c != 0 {
		t.Fatalf("expected count 0, got %d", c)
	}
	r.Register(&mockModule{info: contracts.ModuleInfo{ID: "a", Name: "A"}}, nil)
	if c := r.Count(); c != 1 {
		t.Fatalf("expected count 1, got %d", c)
	}
	r.Register(&mockModule{info: contracts.ModuleInfo{ID: "b", Name: "B"}}, nil)
	if c := r.Count(); c != 2 {
		t.Fatalf("expected count 2, got %d", c)
	}
}

func TestDiscover(t *testing.T) {
	r := New()
	r.Register(&mockModule{info: contracts.ModuleInfo{
		ID: "a", Name: "A", Kinds: []contracts.ModuleKind{contracts.ModuleKindProvider},
	}}, nil)
	r.Register(&mockModule{info: contracts.ModuleInfo{
		ID: "b", Name: "B", Kinds: []contracts.ModuleKind{contracts.ModuleKindProvider},
	}}, nil)

	infos := r.Discover(nil, contracts.ModuleKindProvider)
	if len(infos) != 2 {
		t.Fatalf("expected 2 infos, got %d", len(infos))
	}
	if len(r.Discover(nil, contracts.ModuleKindUI)) != 0 {
		t.Fatal("expected 0 infos for unmatched kind")
	}
}

// ---------------------------------------------------------------------------
// Media Schema tests
// ---------------------------------------------------------------------------

func TestRegisterMediaSchema(t *testing.T) {
	r := New()
	schema := contracts.MediaTypeSchema{
		MediaType: contracts.MediaTypeMovie,
		Fields: []contracts.MediaFieldSchema{
			{Key: "title", Type: contracts.FieldTypeString},
		},
		ModuleID: "test-module",
	}

	if err := r.RegisterMediaSchema(schema); err != nil {
		t.Fatalf("RegisterMediaSchema should succeed: %v", err)
	}

	got, ok := r.MediaSchema(contracts.MediaTypeMovie)
	if !ok {
		t.Fatal("expected to find schema for movie media type")
	}
	if got.ModuleID != "test-module" {
		t.Fatalf("expected module ID test-module, got %s", got.ModuleID)
	}
}

func TestRegisterMediaSchemaDuplicate(t *testing.T) {
	r := New()
	schema := contracts.MediaTypeSchema{
		MediaType: contracts.MediaTypeMovie,
		ModuleID:  "mod1",
	}
	if err := r.RegisterMediaSchema(schema); err != nil {
		t.Fatal(err)
	}

	schema2 := contracts.MediaTypeSchema{
		MediaType: contracts.MediaTypeMovie,
		ModuleID:  "mod2",
	}
	if err := r.RegisterMediaSchema(schema2); err == nil {
		t.Fatal("expected error for duplicate media type schema")
	}
}

func TestMediaSchema(t *testing.T) {
	r := New()
	_, ok := r.MediaSchema(contracts.MediaTypeMovie)
	if ok {
		t.Fatal("expected false for unregistered media type")
	}

	r.RegisterMediaSchema(contracts.MediaTypeSchema{
		MediaType: contracts.MediaTypeMovie,
		ModuleID:  "test",
	})

	_, ok = r.MediaSchema(contracts.MediaTypeMovie)
	if !ok {
		t.Fatal("expected true for registered media type")
	}
}

func TestMediaSchemas(t *testing.T) {
	r := New()

	schemas := r.MediaSchemas()
	if len(schemas) != 0 {
		t.Fatalf("expected 0 schemas initially, got %d", len(schemas))
	}

	r.RegisterMediaSchema(contracts.MediaTypeSchema{
		MediaType: contracts.MediaTypeMovie,
		ModuleID:  "movies",
	})
	r.RegisterMediaSchema(contracts.MediaTypeSchema{
		MediaType: contracts.MediaTypeTV,
		ModuleID:  "tv",
	})

	schemas = r.MediaSchemas()
	if len(schemas) != 2 {
		t.Fatalf("expected 2 schemas, got %d", len(schemas))
	}

	types := make(map[contracts.MediaType]bool)
	for _, s := range schemas {
		types[s.MediaType] = true
	}
	if !types[contracts.MediaTypeMovie] || !types[contracts.MediaTypeTV] {
		t.Fatal("MediaSchemas should contain both movie and tv schemas")
	}
}
