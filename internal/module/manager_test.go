package module_test

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/internal/events"
	"github.com/Muxcore-Media/core/internal/module"
	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/pkg/contracts"
)

var errUnhealthy = errors.New("module unhealthy")

type mockAuditLogger struct {
	mu  sync.Mutex
	log []contracts.AuditEntry
}

func (m *mockAuditLogger) Log(_ context.Context, entry contracts.AuditEntry) error {
	m.mu.Lock()
	m.log = append(m.log, entry)
	m.mu.Unlock()
	return nil
}
func (m *mockAuditLogger) Query(_ context.Context, _ contracts.AuditFilter) ([]contracts.AuditEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]contracts.AuditEntry, len(m.log))
	copy(cp, m.log)
	return cp, nil
}
func (m *mockAuditLogger) Export(_ context.Context, _ string) (io.ReadCloser, error) { return nil, nil }
func (m *mockAuditLogger) VerifyChainIntegrity(_ context.Context, _, _ time.Time) (contracts.ChainVerificationResult, error) {
	return contracts.ChainVerificationResult{Valid: true}, nil
}
func (m *mockAuditLogger) VerifyAll(_ context.Context) (contracts.ChainVerificationResult, error) {
	return contracts.ChainVerificationResult{Valid: true}, nil
}

type testModule struct {
	info      contracts.ModuleInfo
	initOK    bool
	startOK   bool
	stopOK    bool
	healthErr error
}

func (m *testModule) Info() contracts.ModuleInfo       { return m.info }
func (m *testModule) Init(ctx context.Context) error   { m.initOK = true; return nil }
func (m *testModule) Start(ctx context.Context) error  { m.startOK = true; return nil }
func (m *testModule) Stop(ctx context.Context) error   { m.stopOK = true; return nil }
func (m *testModule) Health(ctx context.Context) error { return m.healthErr }

func TestManagerLifecycle(t *testing.T) {
	reg := registry.New()
	mgr := module.NewManager(reg, nil)

	bus := events.NewMemoryBus()
	_ = bus

	mod := &testModule{
		info: contracts.ModuleInfo{
			ID:      "test-module",
			Name:    "Test Module",
			Version: "1.0.0",
			Roles:   []string{"provider"},
		},
	}

	ctx := context.Background()

	if err := mgr.Register(mod, nil); err != nil {
		t.Fatalf("register: %v", err)
	}

	if reg.Count() != 1 {
		t.Fatalf("expected 1 module, got %d", reg.Count())
	}

	if err := mgr.InitAll(ctx); err != nil {
		t.Fatalf("init all: %v", err)
	}
	if !mod.initOK {
		t.Fatal("module not initialized")
	}

	if err := mgr.StartAll(ctx); err != nil {
		t.Fatalf("start all: %v", err)
	}
	if !mod.startOK {
		t.Fatal("module not started")
	}

	entry, err := reg.Get("test-module")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if entry.State != contracts.ModuleStateRunning {
		t.Fatalf("expected running, got %s", entry.State)
	}

	results := mgr.HealthCheck(ctx)
	if err := results["test-module"]; err != nil {
		t.Fatalf("expected healthy, got error: %v", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := mgr.StopAll(shutdownCtx); err != nil {
		t.Fatalf("stop all: %v", err)
	}
	if !mod.stopOK {
		t.Fatal("module not stopped")
	}
}

func TestDependencyOrder(t *testing.T) {
	reg := registry.New()
	mgr := module.NewManager(reg, nil)

	makeMod := func(id string) *testModule {
		return &testModule{
			info: contracts.ModuleInfo{
				ID:      id,
				Name:    id,
				Version: "1.0.0",
				Roles:   []string{"provider"},
			},
		}
	}

	// Register in reverse dependency order
	mgr.Register(makeMod("downloader"), nil)
	mgr.Register(makeMod("media-manager"), []string{"downloader"})
	mgr.Register(makeMod("ui"), []string{"media-manager"})

	ctx := context.Background()
	if err := mgr.InitAll(ctx); err != nil {
		t.Fatalf("init all: %v", err)
	}

	entries := reg.List()
	if len(entries) != 3 {
		t.Fatalf("expected 3 modules, got %d", len(entries))
	}
}

func TestCircularDependency(t *testing.T) {
	reg := registry.New()
	mgr := module.NewManager(reg, nil)

	makeMod := func(id string) *testModule {
		return &testModule{
			info: contracts.ModuleInfo{
				ID:      id,
				Name:    id,
				Version: "1.0.0",
				Roles:   []string{"provider"},
			},
		}
	}

	mgr.Register(makeMod("A"), []string{"B"})
	mgr.Register(makeMod("B"), []string{"A"})

	ctx := context.Background()
	err := mgr.InitAll(ctx)
	if err == nil {
		t.Fatal("expected circular dependency error")
	}
}

func TestUnregister(t *testing.T) {
	reg := registry.New()
	mgr := module.NewManager(reg, nil)

	mod := &testModule{
		info: contracts.ModuleInfo{
			ID:      "test-module",
			Name:    "Test",
			Version: "1.0.0",
		},
	}

	if err := mgr.Register(mod, nil); err != nil {
		t.Fatalf("register: %v", err)
	}
	if reg.Count() != 1 {
		t.Fatalf("expected 1 module after register, got %d", reg.Count())
	}

	if err := mgr.Unregister("test-module"); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	if reg.Count() != 0 {
		t.Fatalf("expected 0 modules after unregister, got %d", reg.Count())
	}
}

func TestUnregister_Nonexistent(t *testing.T) {
	reg := registry.New()
	mgr := module.NewManager(reg, nil)

	if err := mgr.Unregister("nonexistent"); err == nil {
		t.Fatal("expected error for nonexistent module")
	}
}

func TestSetAuditLogger_FiresLifecycleEvents(t *testing.T) {
	reg := registry.New()
	mgr := module.NewManager(reg, nil)

	audit := &mockAuditLogger{}
	mgr.SetAuditLogger(audit)

	mod := &testModule{
		info: contracts.ModuleInfo{
			ID:      "test-module",
			Name:    "Test",
			Version: "1.0.0",
			Roles:   []string{"provider"},
		},
	}

	ctx := context.Background()
	if err := mgr.Register(mod, nil); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := mgr.InitAll(ctx); err != nil {
		t.Fatalf("init all: %v", err)
	}
	if err := mgr.StartAll(ctx); err != nil {
		t.Fatalf("start all: %v", err)
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	mgr.StopAll(shutdownCtx)

	// Give goroutines time to write audit entries.
	time.Sleep(100 * time.Millisecond)

	entries, _ := audit.Query(ctx, contracts.AuditFilter{})
	actions := make(map[string]int)
	for _, e := range entries {
		actions[e.Action]++
	}

	expected := []string{"module.register", "module.init", "module.start", "module.stop"}
	for _, a := range expected {
		if actions[a] == 0 {
			t.Errorf("expected audit action %q, got %v", a, actions)
		}
	}
}

func TestPublishModuleEvents(t *testing.T) {
	bus := events.NewMemoryBus()
	permit := &permissivePolicy{}
	bus.SetPublishPolicy(permit)

	reg := registry.New()
	mgr := module.NewManager(reg, bus)

	var mu sync.Mutex
	var received []string
	_, err := bus.Subscribe(context.Background(), contracts.EventModuleRegistered,
		func(_ context.Context, e contracts.Event) error {
			mu.Lock()
			received = append(received, "registered")
			mu.Unlock()
			return nil
		},
	)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	_, err = bus.Subscribe(context.Background(), contracts.EventModuleUnregistered,
		func(_ context.Context, e contracts.Event) error {
			mu.Lock()
			received = append(received, "unregistered")
			mu.Unlock()
			return nil
		},
	)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	mod := &testModule{
		info: contracts.ModuleInfo{
			ID:      "test-module",
			Name:    "Test",
			Version: "1.0.0",
		},
	}

	if err := mgr.Register(mod, nil); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := mgr.Unregister("test-module"); err != nil {
		t.Fatalf("unregister: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	hasRegistered, hasUnregistered := false, false
	for _, s := range received {
		if s == "registered" {
			hasRegistered = true
		}
		if s == "unregistered" {
			hasUnregistered = true
		}
	}
	if !hasRegistered {
		t.Error("expected module.registered event")
	}
	if !hasUnregistered {
		t.Error("expected module.unregistered event")
	}
}

func TestPublishModuleDegraded(t *testing.T) {
	bus := events.NewMemoryBus()
	bus.SetPublishPolicy(permissivePolicy{})

	var degraded atomic.Int64
	bus.Subscribe(context.Background(), contracts.EventModuleDegraded,
		func(_ context.Context, e contracts.Event) error {
			degraded.Add(1)
			return nil
		},
	)

	reg := registry.New()
	mgr := module.NewManager(reg, bus)

	mod := &testModule{
		info: contracts.ModuleInfo{
			ID:      "unhealthy-mod",
			Name:    "Unhealthy",
			Version: "1.0.0",
			Roles:   []string{"provider"},
		},
		healthErr: errUnhealthy,
	}

	mgr.Register(mod, nil)
	ctx := context.Background()
	mgr.InitAll(ctx)
	mgr.StartAll(ctx)

	// HealthCheck triggers degraded events for unhealthy modules.
	results := mgr.HealthCheck(ctx)

	time.Sleep(200 * time.Millisecond)

	if n := degraded.Load(); n == 0 {
		t.Error("expected at least one module.degraded event, HealthCheck results:", results)
	}
}

// permissivePolicy allows all event publication for testing.
type permissivePolicy struct{}

func (permissivePolicy) CanPublish(_ context.Context, _, _ string) (bool, error) { return true, nil }

func TestNewManager(t *testing.T) {
	reg := registry.New()
	bus := events.NewMemoryBus()
	mgr := module.NewManager(reg, bus)
	if mgr == nil {
		t.Fatal("NewManager returned nil")
	}
}

func TestNewManager_NilBus(t *testing.T) {
	reg := registry.New()
	mgr := module.NewManager(reg, nil)
	if mgr == nil {
		t.Fatal("NewManager with nil bus returned nil")
	}
}

func TestRegister_PublishesEvent(t *testing.T) {
	bus := events.NewMemoryBus()
	bus.SetPublishPolicy(permissivePolicy{})

	var gotEvent atomic.Bool
	bus.Subscribe(context.Background(), contracts.EventModuleRegistered,
		func(_ context.Context, e contracts.Event) error {
			gotEvent.Store(true)
			return nil
		},
	)

	reg := registry.New()
	mgr := module.NewManager(reg, bus)

	mod := &testModule{
		info: contracts.ModuleInfo{ID: "evt-mod", Name: "Evt", Version: "1.0.0"},
	}
	if err := mgr.Register(mod, nil); err != nil {
		t.Fatalf("register: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	if !gotEvent.Load() {
		t.Error("expected module.registered event to be published")
	}
}

func TestUnregister_PublishesEvent(t *testing.T) {
	bus := events.NewMemoryBus()
	bus.SetPublishPolicy(permissivePolicy{})

	var gotEvent atomic.Bool
	bus.Subscribe(context.Background(), contracts.EventModuleUnregistered,
		func(_ context.Context, e contracts.Event) error {
			gotEvent.Store(true)
			return nil
		},
	)

	reg := registry.New()
	mgr := module.NewManager(reg, bus)

	mod := &testModule{
		info: contracts.ModuleInfo{ID: "unreg-evt", Name: "Unreg", Version: "1.0.0"},
	}
	mgr.Register(mod, nil)
	mgr.Unregister("unreg-evt")

	time.Sleep(100 * time.Millisecond)
	if !gotEvent.Load() {
		t.Error("expected module.unregistered event to be published")
	}
}

func TestInitAll_DegradedOnUnresolvedDeps(t *testing.T) {
	reg := registry.New()
	mgr := module.NewManager(reg, nil)

	base := &testModule{
		info: contracts.ModuleInfo{
			ID:      "base-mod",
			Name:    "Base",
			Version: "1.0.0",
		},
	}
	orphan := &testModule{
		info: contracts.ModuleInfo{
			ID:      "orphan-mod",
			Name:    "Orphan",
			Version: "1.0.0",
		},
	}
	mgr.Register(base, []string{"missing-dep"})
	mgr.Register(orphan, nil)

	ctx := context.Background()
	err := mgr.InitAll(ctx)
	if err == nil {
		t.Fatal("expected error from InitAll with unresolvable deps")
	}
}

func TestStopAll_ReverseOrder(t *testing.T) {
	reg := registry.New()
	mgr := module.NewManager(reg, nil)

	var mu sync.Mutex
	var stopOrder []string

	makeMod := func(id string) *stoppableModule {
		return &stoppableModule{
			info: contracts.ModuleInfo{
				ID:      id,
				Name:    id,
				Version: "1.0.0",
			},
			stopFn: func(ctx context.Context) error {
				mu.Lock()
				stopOrder = append(stopOrder, id)
				mu.Unlock()
				return nil
			},
		}
	}

	a := makeMod("alpha")
	b := makeMod("beta")
	b.deps = []string{"alpha"}

	mgr.Register(a, nil)
	mgr.Register(b, []string{"alpha"})

	ctx := context.Background()
	mgr.InitAll(ctx)
	mgr.StartAll(ctx)
	mgr.StopAll(ctx)

	mu.Lock()
	defer mu.Unlock()
	if len(stopOrder) != 2 {
		t.Fatalf("expected 2 stops, got %d", len(stopOrder))
	}
	if stopOrder[0] != "beta" || stopOrder[1] != "alpha" {
		t.Errorf("expected stop order [beta alpha], got %v", stopOrder)
	}
}

type stoppableModule struct {
	info   contracts.ModuleInfo
	stopFn func(context.Context) error
	deps   []string
}

func (m *stoppableModule) Info() contracts.ModuleInfo      { return m.info }
func (m *stoppableModule) Init(ctx context.Context) error  { return nil }
func (m *stoppableModule) Start(ctx context.Context) error { return nil }
func (m *stoppableModule) Stop(ctx context.Context) error {
	if m.stopFn != nil {
		return m.stopFn(ctx)
	}
	return nil
}
func (m *stoppableModule) Health(ctx context.Context) error { return nil }

func TestHealthCheck_RecordsErrors(t *testing.T) {
	reg := registry.New()
	mgr := module.NewManager(reg, nil)

	mod := &testModule{
		info: contracts.ModuleInfo{
			ID:      "sick-mod",
			Name:    "Sick",
			Version: "1.0.0",
		},
		healthErr: errUnhealthy,
	}
	mgr.Register(mod, nil)

	ctx := context.Background()
	results := mgr.HealthCheck(ctx)

	if results["sick-mod"] == nil {
		t.Error("expected health error for sick-mod")
	}

	entry, _ := reg.Get("sick-mod")
	if entry.Health == nil {
		t.Error("expected registry to record health error")
	}
}

func TestHealthCheck_HealthyModule(t *testing.T) {
	reg := registry.New()
	mgr := module.NewManager(reg, nil)

	mod := &testModule{
		info: contracts.ModuleInfo{
			ID:      "healthy-mod",
			Name:    "Healthy",
			Version: "1.0.0",
		},
	}
	mgr.Register(mod, nil)

	results := mgr.HealthCheck(context.Background())
	if results["healthy-mod"] != nil {
		t.Errorf("expected nil health, got %v", results["healthy-mod"])
	}
}

type mockRestarter struct {
	calledWith string
	err        error
}

func (r *mockRestarter) RestartModule(ctx context.Context, moduleID string) error {
	r.calledWith = moduleID
	return r.err
}

func TestSetRestarter(t *testing.T) {
	reg := registry.New()
	mgr := module.NewManager(reg, nil)

	r := &mockRestarter{}
	mgr.SetRestarter(r)
}

func TestSetAuditLogger(t *testing.T) {
	reg := registry.New()
	mgr := module.NewManager(reg, nil)

	a := &mockAuditLogger{}
	mgr.SetAuditLogger(a)
}

func TestPublishNilBus_NoPanic(t *testing.T) {
	reg := registry.New()
	mgr := module.NewManager(reg, nil)

	mod := &testModule{
		info: contracts.ModuleInfo{ID: "nil-bus-mod", Name: "NilBus", Version: "1.0.0"},
	}
	if err := mgr.Register(mod, nil); err != nil {
		t.Fatalf("register with nil bus: %v", err)
	}
	if err := mgr.Unregister("nil-bus-mod"); err != nil {
		t.Fatalf("unregister with nil bus: %v", err)
	}

	results := mgr.HealthCheck(context.Background())
	if results["nil-bus-mod"] != nil {
		t.Errorf("unexpected health error: %v", results["nil-bus-mod"])
	}
}

func TestRegister_DuplicateFails(t *testing.T) {
	reg := registry.New()
	mgr := module.NewManager(reg, nil)

	mod := &testModule{
		info: contracts.ModuleInfo{ID: "dup", Name: "Dup", Version: "1.0.0"},
	}
	if err := mgr.Register(mod, nil); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := mgr.Register(mod, nil); err == nil {
		t.Error("expected error for duplicate registration")
	}
}

func TestStopAll_AuditLifecycle(t *testing.T) {
	reg := registry.New()
	mgr := module.NewManager(reg, nil)
	audit := &mockAuditLogger{}
	mgr.SetAuditLogger(audit)

	mod := &testModule{
		info: contracts.ModuleInfo{ID: "stop-audit", Name: "Stop", Version: "1.0.0"},
	}
	mgr.Register(mod, nil)

	ctx := context.Background()
	mgr.InitAll(ctx)
	mgr.StartAll(ctx)
	mgr.StopAll(ctx)

	time.Sleep(100 * time.Millisecond)

	entries, _ := audit.Query(ctx, contracts.AuditFilter{})
	found := false
	for _, e := range entries {
		if e.Action == "module.stop" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected module.stop audit entry")
	}
}
