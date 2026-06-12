//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// controllableHealthModule is a stub whose Health() can fail on demand.
type controllableHealthModule struct {
	stubModule
	mu     sync.Mutex
	health error
}

func (m *controllableHealthModule) Health(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.health
}

func (m *controllableHealthModule) setHealth(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.health = err
}

// recordingRestarter records which modules were restarted.
type recordingRestarter struct {
	mu        sync.Mutex
	restarted []string
}

func (r *recordingRestarter) RestartModule(_ context.Context, moduleID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.restarted = append(r.restarted, moduleID)
	return nil
}

func (r *recordingRestarter) Restarted() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.restarted))
	copy(out, r.restarted)
	return out
}

// TestChaos_HealthCheckLoop_DetectsAndRemediates verifies that the health
// check loop detects an unhealthy module and triggers restart via the
// configured Restarter.
func TestChaos_HealthCheckLoop_DetectsAndRemediates(t *testing.T) {
	h := newHarness(t)

	mod := &controllableHealthModule{
		stubModule: stubModule{id: "chaos-health", name: "Chaos Health", version: "1.0.0"},
	}
	mod.setHealth(nil)
	if err := h.modMgr.Register(context.Background(), mod, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := h.modMgr.InitAll(h.ctx); err != nil {
		t.Fatalf("InitAll: %v", err)
	}
	if err := h.modMgr.StartAll(h.ctx); err != nil {
		t.Fatalf("StartAll: %v", err)
	}

	// Module should start healthy.
	results := h.modMgr.HealthCheck(h.ctx)
	if results["chaos-health"] != nil {
		t.Fatalf("expected healthy module, got error: %v", results["chaos-health"])
	}

	// Make the module unhealthy.
	expectedErr := errors.New("simulated crash")
	mod.setHealth(expectedErr)

	// HealthCheck should detect the failure.
	results = h.modMgr.HealthCheck(h.ctx)
	if results["chaos-health"] == nil {
		t.Fatal("expected health check to detect failure")
	}
	if !errors.Is(results["chaos-health"], expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, results["chaos-health"])
	}

	// Attach a restarter and start the health check loop.
	restarter := &recordingRestarter{}
	h.modMgr.SetRestarter(restarter)
	h.modMgr.StartHealthCheckLoop(h.ctx, 100*time.Millisecond)

	// Wait for the loop to run and remediate.
	time.Sleep(300 * time.Millisecond)

	restarted := restarter.Restarted()
	if len(restarted) < 1 {
		t.Fatal("expected at least 1 restart from health check loop")
	}
	if restarted[0] != "chaos-health" {
		t.Errorf("expected restart of 'chaos-health', got %q", restarted[0])
	}
}

// TestChaos_MultipleUnhealthyModules verifies that the health check loop
// handles multiple unhealthy modules concurrently. The loop runs multiple
// times; we verify each module gets restarted at least once.
func TestChaos_MultipleUnhealthyModules(t *testing.T) {
	h := newHarness(t)

	restarter := &recordingRestarter{}
	h.modMgr.SetRestarter(restarter)

	// Register 3 modules, all healthy initially.
	mods := make([]*controllableHealthModule, 3)
	for i := range mods {
		mod := &controllableHealthModule{
			stubModule: stubModule{
				id:      fmt.Sprintf("chaos-mod-%d", i),
				name:    fmt.Sprintf("Chaos Mod %d", i),
				version: "1.0.0",
			},
		}
		mods[i] = mod
		if err := h.modMgr.Register(context.Background(), mod, nil); err != nil {
			t.Fatalf("Register mod-%d: %v", i, err)
		}
	}
	h.modMgr.InitAll(h.ctx)
	h.modMgr.StartAll(h.ctx)

	// Make all 3 fail.
	for _, mod := range mods {
		mod.setHealth(errors.New("simulated failure"))
	}

	h.modMgr.StartHealthCheckLoop(h.ctx, 50*time.Millisecond)
	time.Sleep(300 * time.Millisecond)

	restarted := restarter.Restarted()
	seen := make(map[string]bool)
	for _, id := range restarted {
		seen[id] = true
	}
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("chaos-mod-%d", i)
		if !seen[id] {
			t.Errorf("module %q was never restarted", id)
		}
	}
}

// TestChaos_HealthLoop_NoRestarterIsNoop verifies that the health check loop
// does not panic or crash when no restarter is configured.
func TestChaos_HealthLoop_NoRestarterIsNoop(t *testing.T) {
	h := newHarness(t)

	mod := &controllableHealthModule{
		stubModule: stubModule{id: "chaos-noop", name: "No Restarter", version: "1.0.0"},
	}
	h.modMgr.Register(context.Background(), mod, nil)
	h.modMgr.InitAll(h.ctx)
	h.modMgr.StartAll(h.ctx)

	mod.setHealth(errors.New("failing"))

	// No restarter set — should not panic.
	h.modMgr.StartHealthCheckLoop(h.ctx, 50*time.Millisecond)
	time.Sleep(200 * time.Millisecond)

	// Test passed if we got here without panic.
	results := h.modMgr.HealthCheck(h.ctx)
	if results["chaos-noop"] == nil {
		t.Error("expected module to still be unhealthy")
	}
}
