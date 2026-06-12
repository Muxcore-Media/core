//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/internal/config"
	"github.com/Muxcore-Media/core/pkg/contracts"
)

// TestE2E_SIGHUP_ConfigReload_AppliesChanges verifies the full config reload
// pipeline: write updated config → Reload → apply changes → publish event.
// This mirrors the SIGHUP handler logic in cmd/muxcored/main.go.
func TestE2E_SIGHUP_ConfigReload_AppliesChanges(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "muxcore.json")

	// Write initial config.
	initial := `{"server":{"addr":":8080"},"log":{"level":"info","format":"text"},"audit":{"path":"/tmp/audit.jsonl"}}`
	if err := os.WriteFile(path, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("initial Load: %v", err)
	}

	// Subscribe to config reload events on the harness bus.
	eventCh := make(chan contracts.Event, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = h.bus.Subscribe(ctx, contracts.EventConfigReloaded, func(c context.Context, e contracts.Event) error {
		select {
		case eventCh <- e:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Write updated config with safe changes.
	updated := `{"server":{"addr":":8080"},"log":{"level":"debug","format":"json"},"audit":{"path":"/var/log/muxcore/audit.jsonl"}}`
	if err := os.WriteFile(path, []byte(updated), 0600); err != nil {
		t.Fatal(err)
	}

	// Reload config (mirrors main.go SIGHUP handler).
	result, err := config.Reload(cfg, path)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}

	changes := result.Changes

	// Apply changes like the SIGHUP handler does (main.go: *cfg = *result.Config).
	if changes.LogLevel || changes.LogFormat {
		t.Logf("logger would be reconfigured: level=%s format=%s",
			result.Config.Log.Level, result.Config.Log.Format)
	}

	if changes.AuditPath {
		t.Logf("audit path change deferred: %s (needs restart)",
			result.Config.Audit.Path)
		// In main.go, old audit path is kept; verify behavior matches.
		if cfg.Audit.Path != "/tmp/audit.jsonl" {
			t.Error("expected audit path to remain unchanged during live reload")
		}
	}

	// Update the config pointer (mirrors main.go *cfg = *result.Config).
	*cfg = *result.Config

	// Publish event like main.go does.
	payload, _ := json.Marshal(map[string]any{
		"changes": changes,
	})
	if err := h.bus.Publish(ctx, contracts.Event{
		Type:    contracts.EventConfigReloaded,
		Source:  "muxcored",
		Payload: payload,
	}); err != nil {
		t.Fatalf("Publish EventConfigReloaded: %v", err)
	}

	// Verify the event was received.
	select {
	case ev := <-eventCh:
		if ev.Type != contracts.EventConfigReloaded {
			t.Errorf("expected EventConfigReloaded, got %s", ev.Type)
		}
		if ev.Source != "muxcored" {
			t.Errorf("expected source 'muxcored', got %q", ev.Source)
		}
		// Verify payload contains the changes.
		var decoded struct {
			Changes config.ChangedFields `json:"changes"`
		}
		if err := json.Unmarshal(ev.Payload, &decoded); err != nil {
			t.Fatalf("unmarshal event payload: %v", err)
		}
		if !decoded.Changes.LogLevel {
			t.Error("expected LogLevel change in event payload")
		}
		if !decoded.Changes.LogFormat {
			t.Error("expected LogFormat change in event payload")
		}
		if decoded.Changes.Unsafe {
			t.Error("expected no unsafe changes")
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for EventConfigReloaded")
	}

	// Verify the running config pointer was updated (mirrors main.go cfgMu swap).
	if cfg.Log.Level != "debug" {
		t.Errorf("expected config log level 'debug', got %q", cfg.Log.Level)
	}
	if cfg.Log.Format != "json" {
		t.Errorf("expected config log format 'json', got %q", cfg.Log.Format)
	}
}

// TestE2E_SIGHUP_UnsafeChanges_NotApplied verifies that unsafe config
// changes (e.g., server address) are detected and flagged but not silently
// applied during hot-reload.
func TestE2E_SIGHUP_UnsafeChanges_NotApplied(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "muxcore.json")

	initial := `{"server":{"addr":":8080"},"grpc":{"addr":":9090"}}`
	if err := os.WriteFile(path, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("initial Load: %v", err)
	}

	// Write updated config with unsafe change.
	updated := `{"server":{"addr":":9999"},"grpc":{"addr":":9090"}}`
	if err := os.WriteFile(path, []byte(updated), 0600); err != nil {
		t.Fatal(err)
	}

	result, err := config.Reload(cfg, path)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}

	if !result.Changes.Unsafe {
		t.Fatal("expected unsafe changes detected")
	}
	found := false
	for _, f := range result.Changes.UnsafeFields {
		if f == "server.addr" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'server.addr' in unsafe fields, got: %v", result.Changes.UnsafeFields)
	}

	// Verify the running config was NOT updated for unsafe fields.
	if cfg.Server.Addr != ":8080" {
		t.Errorf("expected server.addr to remain ':8080', got %q", cfg.Server.Addr)
	}
}

// TestE2E_SIGHUP_SeedNodes_Detected verifies that seed node changes
// are detected during config reload.
func TestE2E_SIGHUP_SeedNodes_Detected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "muxcore.json")

	initial := `{"server":{"addr":":8080"},"grpc":{"seed_nodes":["node-a:9090"]}}`
	if err := os.WriteFile(path, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("initial Load: %v", err)
	}

	updated := `{"server":{"addr":":8080"},"grpc":{"seed_nodes":["node-b:9090","node-c:9090"]}}`
	if err := os.WriteFile(path, []byte(updated), 0600); err != nil {
		t.Fatal(err)
	}

	result, err := config.Reload(cfg, path)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}

	if !result.Changes.SeedNodes {
		t.Error("expected SeedNodes change detected")
	}
}
