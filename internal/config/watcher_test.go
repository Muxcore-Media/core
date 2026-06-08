package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestReload_NoPath(t *testing.T) {
	_, err := Reload(Default(), "")
	if err == nil {
		t.Error("expected error for empty path")
	}
}

func TestReload_MissingFile(t *testing.T) {
	_, err := Reload(Default(), "/nonexistent/path/config.json")
	if err == nil {
		t.Error("expected error for missing config file")
	}
}

func TestReload_SafeChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "muxcore.json")

	// Write new config with safe changes only.
	newCfg := map[string]any{
		"server": map[string]any{"addr": ":8080", "read_timeout": 15, "write_timeout": 15},
		"grpc":   map[string]any{"addr": ":9090"},
		"log":    map[string]any{"level": "debug", "format": "json"},
		"audit":  map[string]any{"path": "/tmp/audit.jsonl"},
	}
	data, _ := json.Marshal(newCfg)
	os.WriteFile(path, data, 0644)

	prev := Default()
	result, err := Reload(prev, path)
	if err != nil {
		t.Fatalf("Reload failed: %v", err)
	}

	if !result.Changes.LogLevel {
		t.Error("expected LogLevel change detected")
	}
	if !result.Changes.LogFormat {
		t.Error("expected LogFormat change detected")
	}
	if !result.Changes.AuditPath {
		t.Error("expected AuditPath change detected")
	}
	if result.Changes.Unsafe {
		t.Errorf("expected no unsafe changes, got: %v", result.Changes.UnsafeFields)
	}
}

func TestReload_UnsafeChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "muxcore.json")

	newCfg := map[string]any{
		"server": map[string]any{
			"addr":          ":9999", // changed — unsafe
			"read_timeout":  15,
			"write_timeout": 15,
		},
		"grpc": map[string]any{"addr": ":9090"},
		"log":  map[string]any{"level": "info", "format": "text"},
	}
	data, _ := json.Marshal(newCfg)
	os.WriteFile(path, data, 0644)

	prev := Default()
	result, err := Reload(prev, path)
	if err != nil {
		t.Fatalf("Reload failed: %v", err)
	}

	if !result.Changes.Unsafe {
		t.Error("expected unsafe change detected (server.addr changed)")
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
}

func TestDiffConfigs_SeedNodes(t *testing.T) {
	old := Default()
	old.GRPC.SeedNodes = []string{"a:9090"}

	new := Default()
	new.GRPC.SeedNodes = []string{"b:9090", "c:9090"}

	ch := diffConfigs(old, new)
	if !ch.SeedNodes {
		t.Error("expected SeedNodes change detected")
	}
}

func TestDiffConfigs_NoChanges(t *testing.T) {
	cfg := Default()
	ch := diffConfigs(cfg, cfg)
	if ch.LogLevel || ch.LogFormat || ch.AuditPath || ch.SeedNodes || ch.Unsafe {
		t.Error("expected no changes when diffing identical configs")
	}
}

func TestStringSlicesEqual(t *testing.T) {
	if !stringSlicesEqual(nil, nil) {
		t.Error("nil slices should be equal")
	}
	if !stringSlicesEqual([]string{"a", "b"}, []string{"a", "b"}) {
		t.Error("identical slices should be equal")
	}
	if stringSlicesEqual([]string{"a"}, []string{"b"}) {
		t.Error("different slices should not be equal")
	}
	if stringSlicesEqual([]string{"a"}, []string{"a", "b"}) {
		t.Error("different lengths should not be equal")
	}
}
