package module_test

import (
	"context"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/internal/events"
	"github.com/Muxcore-Media/core/internal/module"
	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/pkg/contracts"
)

type testModule struct {
	info    contracts.ModuleInfo
	initOK  bool
	startOK bool
	stopOK  bool
}

func (m *testModule) Info() contracts.ModuleInfo              { return m.info }
func (m *testModule) Init(ctx context.Context) error           { m.initOK = true; return nil }
func (m *testModule) Start(ctx context.Context) error          { m.startOK = true; return nil }
func (m *testModule) Stop(ctx context.Context) error           { m.stopOK = true; return nil }
func (m *testModule) Health(ctx context.Context) error         { return nil }

func TestManagerLifecycle(t *testing.T) {
	reg := registry.New()
	mgr := module.NewManager(reg)

	bus := events.NewMemoryBus()
	_ = bus

	mod := &testModule{
		info: contracts.ModuleInfo{
			ID:      "test-module",
			Name:    "Test Module",
			Version: "1.0.0",
			Kind:    contracts.ModuleKindProvider,
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
	if len(results) != 0 {
		t.Fatalf("expected healthy, got errors: %v", results)
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
	mgr := module.NewManager(reg)

	makeMod := func(id string) *testModule {
		return &testModule{
			info: contracts.ModuleInfo{
				ID:      id,
				Name:    id,
				Version: "1.0.0",
				Kind:    contracts.ModuleKindProvider,
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
	mgr := module.NewManager(reg)

	makeMod := func(id string) *testModule {
		return &testModule{
			info: contracts.ModuleInfo{
				ID:      id,
				Name:    id,
				Version: "1.0.0",
				Kind:    contracts.ModuleKindProvider,
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
