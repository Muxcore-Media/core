package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefault(t *testing.T) {
	cfg := Default()
	if cfg.Server.Addr != ":8080" {
		t.Errorf("expected :8080, got %q", cfg.Server.Addr)
	}
	if cfg.Server.ReadTimeout != 15 {
		t.Errorf("expected ReadTimeout 15, got %d", cfg.Server.ReadTimeout)
	}
	if cfg.Log.Level != "info" {
		t.Errorf("expected info, got %q", cfg.Log.Level)
	}
	if cfg.Log.Format != "text" {
		t.Errorf("expected text, got %q", cfg.Log.Format)
	}
	if cfg.Modules == nil {
		t.Error("Modules map should be initialized")
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/config.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoad_EmptyPath_UsesDefaults(t *testing.T) {
	clearEnv(t)
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load empty path: %v", err)
	}
	if cfg.Server.Addr != ":8080" {
		t.Errorf("expected :8080, got %q", cfg.Server.Addr)
	}
}

func TestLoad_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{"server":{"addr":":9090","read_timeout":30,"write_timeout":30},"log":{"level":"debug","format":"json"}}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	clearEnv(t)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Addr != ":9090" {
		t.Errorf("expected :9090, got %q", cfg.Server.Addr)
	}
	if cfg.Server.ReadTimeout != 30 {
		t.Errorf("expected ReadTimeout 30, got %d", cfg.Server.ReadTimeout)
	}
	if cfg.Log.Level != "debug" {
		t.Errorf("expected debug, got %q", cfg.Log.Level)
	}
	if cfg.Log.Format != "json" {
		t.Errorf("expected json, got %q", cfg.Log.Format)
	}
}

func TestLoad_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	os.WriteFile(path, []byte("{bad json"), 0644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected parse error for malformed JSON")
	}
}

func TestLoad_EnvOverrides(t *testing.T) {
	clearEnv(t)
	os.Setenv("MUXCORE_ADDR", ":3000")
	os.Setenv("MUXCORE_LOG_LEVEL", "warn")
	os.Setenv("MUXCORE_LOG_FORMAT", "json")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Addr != ":3000" {
		t.Errorf("expected :3000 from env, got %q", cfg.Server.Addr)
	}
	if cfg.Log.Level != "warn" {
		t.Errorf("expected warn from env, got %q", cfg.Log.Level)
	}
	if cfg.Log.Format != "json" {
		t.Errorf("expected json from env, got %q", cfg.Log.Format)
	}
}

func TestLoad_DatabaseCacheEnv(t *testing.T) {
	clearEnv(t)
	os.Setenv("MUXCORE_DATABASE_DRIVER", "postgres")
	os.Setenv("MUXCORE_DATABASE_URL", "postgres://localhost/db")
	os.Setenv("MUXCORE_CACHE_DRIVER", "redis")
	os.Setenv("MUXCORE_CACHE_URL", "redis://localhost:6379")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Database.Driver != "postgres" {
		t.Errorf("expected postgres, got %q", cfg.Database.Driver)
	}
	if cfg.Cache.Driver != "redis" {
		t.Errorf("expected redis, got %q", cfg.Cache.Driver)
	}
}

func TestValidate_EmptyAddr_File(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "empty_addr.json")
	os.WriteFile(path, []byte(`{"server":{"addr":""}}`), 0644)
	
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for empty addr")
	}
}

func TestValidate_ZeroReadTimeout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	os.WriteFile(path, []byte(`{"server":{"read_timeout":0}}`), 0644)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for zero read timeout")
	}
}

func TestValidate_ZeroWriteTimeout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	os.WriteFile(path, []byte(`{"server":{"write_timeout":0}}`), 0644)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for zero write timeout")
	}
}

func TestValidate_InvalidLogLevel(t *testing.T) {
	clearEnv(t)
	os.Setenv("MUXCORE_LOG_LEVEL", "verbose")
	_, err := Load("")
	if err == nil {
		t.Fatal("expected validation error for invalid log level")
	}
}

func TestValidate_InvalidLogFormat(t *testing.T) {
	clearEnv(t)
	os.Setenv("MUXCORE_LOG_FORMAT", "xml")
	_, err := Load("")
	if err == nil {
		t.Fatal("expected validation error for invalid log format")
	}
}

func TestValidate_CaseNormalization(t *testing.T) {
	clearEnv(t)
	os.Setenv("MUXCORE_LOG_LEVEL", "DEBUG")
	os.Setenv("MUXCORE_LOG_FORMAT", "JSON")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Log.Level != "debug" {
		t.Errorf("expected lowercased debug, got %q", cfg.Log.Level)
	}
	if cfg.Log.Format != "json" {
		t.Errorf("expected lowercased json, got %q", cfg.Log.Format)
	}
}

func TestLoad_EnvOverridesFile(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{"server":{"addr":":9090"},"log":{"level":"debug"}}`
	os.WriteFile(path, []byte(content), 0644)

	os.Setenv("MUXCORE_ADDR", ":3000")
	os.Setenv("MUXCORE_LOG_LEVEL", "error")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Addr != ":3000" {
		t.Errorf("expected env override :3000, got %q", cfg.Server.Addr)
	}
	if cfg.Log.Level != "error" {
		t.Errorf("expected env override error, got %q", cfg.Log.Level)
	}
}

func TestLoad_EmptyFile_UsesDefaults(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.json")
	os.WriteFile(path, []byte{}, 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load empty file: %v", err)
	}
	if cfg.Server.Addr != ":8080" {
		t.Errorf("expected default :8080, got %q", cfg.Server.Addr)
	}
}

func TestLoad_PartialFile_MergesDefaults(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "partial.json")
	os.WriteFile(path, []byte(`{"server":{"addr":":7070"}}`), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Addr != ":7070" {
		t.Errorf("expected :7070, got %q", cfg.Server.Addr)
	}
	// Unspecified fields should use defaults
	if cfg.Server.ReadTimeout != 15 {
		t.Errorf("expected default ReadTimeout 15, got %d", cfg.Server.ReadTimeout)
	}
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"MUXCORE_ADDR", "MUXCORE_LOG_LEVEL", "MUXCORE_LOG_FORMAT",
		"MUXCORE_DATABASE_DRIVER", "MUXCORE_DATABASE_URL",
		"MUXCORE_CACHE_DRIVER", "MUXCORE_CACHE_URL",
	} {
		os.Unsetenv(key)
	}
}
