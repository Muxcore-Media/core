//go:build default
// +build default

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/internal/api"
	"github.com/Muxcore-Media/core/internal/config"
	"github.com/Muxcore-Media/core/internal/events"
	"github.com/Muxcore-Media/core/internal/module"
	_ "github.com/Muxcore-Media/core/internal/presets"
	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/internal/storage"
	"github.com/Muxcore-Media/core/pkg/contracts"
)

const testAddr = ":19876"
const testURL = "http://localhost" + testAddr

func TestBootstrapFullStack(t *testing.T) {
	cfg := config.Default()
	cfg.Server.Addr = testAddr

	bus := events.NewMemoryBus()
	reg := registry.New()
	mgr := module.NewManager(reg, bus)
	srv := api.NewServer(cfg.Server.Addr, "", "")

	store := storage.NewOrchestrator(reg)
	store.DiscoverStorage()
	// Cache is now module-discovered

	deps := contracts.Fabric{
		Registry: reg,
		EventBus: bus,
		Routes:   srv,
		Cluster:  nil,
		Storage:  store,
	}

	modules := contracts.LoadRegistered(deps)
	if len(modules) == 0 {
		t.Log("zero modules loaded — preset imports are commented out pending external module updates")
	} else {
		t.Logf("loaded %d modules", len(modules))
	}

	var cl contracts.Cluster
	var wp contracts.WorkerPool
	var al contracts.AuditLogger
	for _, mod := range modules {
		if c, ok := mod.(contracts.Cluster); ok {
			cl = c
		}
		if w, ok := mod.(contracts.WorkerPool); ok {
			wp = w
		}
		if a, ok := mod.(contracts.AuditLogger); ok {
			al = a
		}
	}
	deps.Cluster = cl
	deps.WorkerPool = wp
	deps.Audit = al

	for _, mod := range modules {
		if err := mgr.Register(mod, nil); err != nil {
			t.Fatalf("register %s: %v", mod.Info().ID, err)
		}
	}
	t.Logf("registered %d modules", reg.Count())

	authModules := reg.FindByRole("auth")
	if len(authModules) > 0 {
		provider, _ := authModules[0].Module.(contracts.AuthProvider)
		srv.SetAuthFunc(func(r *http.Request) (*contracts.Session, error) {
			return nil, fmt.Errorf("no token")
		})
		_ = provider
	}

	srv.SetHealthChecker(func() map[string]error {
		return mgr.HealthCheck(context.Background())
	})

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start() }()
	time.Sleep(200 * time.Millisecond)

	if cl != nil {
		if err := cl.Start(context.Background()); err != nil {
			t.Fatalf("cluster start: %v", err)
		}
	}

	if err := mgr.InitAll(context.Background()); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := mgr.StartAll(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	// Verify /health
	resp, err := http.Get(testURL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/health: expected 200, got %d", resp.StatusCode)
	}
	var healthResp struct {
		Status  string            `json:"status"`
		Modules map[string]string `json:"modules"`
	}
	json.NewDecoder(resp.Body).Decode(&healthResp)
	if healthResp.Status != "ok" {
		t.Errorf("health status: expected ok, got %q", healthResp.Status)
	}
	for id, state := range healthResp.Modules {
		if state != "ok" {
			t.Errorf("module %q: expected ok, got %q", id, state)
		}
	}

	// Verify REST API — 404 is acceptable when no modules register the route
	resp2, err := http.Get(testURL + "/api/v1/modules")
	if err != nil {
		t.Fatalf("GET /api/v1/modules: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode == http.StatusOK {
		var apiResp struct {
			Modules []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"modules"`
		}
		json.NewDecoder(resp2.Body).Decode(&apiResp)
		t.Logf("/api/v1/modules returned %d modules", len(apiResp.Modules))
	} else if resp2.StatusCode == http.StatusNotFound {
		t.Logf("/api/v1/modules: 404 (expected when no modules register routes)")
	} else {
		t.Errorf("/api/v1/modules: unexpected status %d", resp2.StatusCode)
	}

	// Shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	srv.Shutdown(ctx)
	mgr.StopAll(ctx)
	if cl != nil {
		cl.Stop(ctx)
	}
	<-errCh
}
