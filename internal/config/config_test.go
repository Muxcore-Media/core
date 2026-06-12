package config

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
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
	os.WriteFile(path, []byte("{bad json"), 0600)

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
	os.WriteFile(path, []byte(`{"server":{"addr":""}}`), 0600)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for empty addr")
	}
}

func TestValidate_ZeroReadTimeout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	os.WriteFile(path, []byte(`{"server":{"read_timeout":0}}`), 0600)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for zero read timeout")
	}
}

func TestValidate_ZeroWriteTimeout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	os.WriteFile(path, []byte(`{"server":{"write_timeout":0}}`), 0600)
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
	os.WriteFile(path, []byte(content), 0600)

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
	os.WriteFile(path, []byte{}, 0600)

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
	os.WriteFile(path, []byte(`{"server":{"addr":":7070"}}`), 0600)

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

func TestRedactedJSON_HidesCredentials(t *testing.T) {
	cfg := &Config{
		Database: DatabaseConfig{Driver: "postgres", URL: "postgres://user:secret@localhost/db"},
		Cache:    CacheConfig{Driver: "redis", URL: "redis://user:pass@localhost:6379/0"},
		Server:   ServerConfig{CertFile: "/etc/certs/cert.pem", KeyFile: "/etc/certs/key.pem"},
		GRPC:     GRPCConfig{CertFile: "/etc/grpc/cert.pem", KeyFile: "/etc/grpc/key.pem", JoinToken: "supersecret"},
	}
	data, err := cfg.RedactedJSON()
	if err != nil {
		t.Fatalf("RedactedJSON: %v", err)
	}
	output := string(data)
	// Verify original credentials are NOT present in output.
	for _, secret := range []string{"user:secret", "user:pass", "supersecret", "/etc/certs/", "/etc/grpc/"} {
		if strings.Contains(output, secret) {
			t.Errorf("secret %q leaked in output: %s", secret, output)
		}
	}
	// Verify redaction markers are present.
	for _, marker := range []string{"%2A%2A%2A", "set"} {
		if !strings.Contains(output, marker) {
			t.Errorf("expected redaction marker %q in output: %s", marker, output)
		}
	}
}

func TestConfig_LogValue_RedactsCredentials(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	db := DatabaseConfig{Driver: "postgres", URL: "postgres://user:secret@localhost/db"}
	logger.Info("test", "db", db)
	output := buf.String()
	buf.Reset()
	if strings.Contains(output, "user:secret") {
		t.Errorf("DB credentials leaked in log output: %s", output)
	}

	cache := CacheConfig{Driver: "redis", URL: "redis://user:pass@localhost:6379/0"}
	logger.Info("test", "cache", cache)
	output = buf.String()
	buf.Reset()
	if strings.Contains(output, "user:pass") {
		t.Errorf("cache credentials leaked in log output: %s", output)
	}
}

func TestRedactURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		want     string
		wantSafe bool // if true, check that original secret is absent
	}{
		{"empty", "", "", false},
		{"postgres with creds", "postgres://user:secret@localhost/db", "", true},
		{"redis with password", "redis://:pass@host:6379", "", true},
		{"no credentials", "http://example.com/path", "http://example.com/path", false},
		{"invalid URL", ":invalid:", "<redacted>", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactURL(tt.input)
			if tt.want != "" && got != tt.want {
				t.Errorf("redactURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
			if tt.wantSafe {
				// Should not contain original credentials.
				if strings.Contains(got, "user:secret") || strings.Contains(got, ":pass") {
					t.Errorf("creds leaked: redactURL(%q) = %q", tt.input, got)
				}
				if !strings.Contains(got, "%2A%2A%2A") {
					t.Errorf("expected redacted userinfo in result: redactURL(%q) = %q", tt.input, got)
				}
			}
		})
	}
}

func TestParseInt(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"42", 42},
		{"0", 0},
		{"-5", -5},
		{"notanumber", 0},
		{"", 0},
	}
	for _, tt := range tests {
		got := parseInt(tt.input)
		if got != tt.expected {
			t.Errorf("parseInt(%q) = %d, want %d", tt.input, got, tt.expected)
		}
	}
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"MUXCORE_ADDR", "MUXCORE_LOG_LEVEL", "MUXCORE_LOG_FORMAT",
		"MUXCORE_DATABASE_DRIVER", "MUXCORE_DATABASE_URL",
		"MUXCORE_CACHE_DRIVER", "MUXCORE_CACHE_URL",
		"MUXCORE_SERVER_READ_TIMEOUT", "MUXCORE_SERVER_WRITE_TIMEOUT",
		"MUXCORE_GRPC_MAX_MESSAGE_SIZE_MB", "MUXCORE_GRPC_CA_CERT_DIR",
	} {
		os.Unsetenv(key)
	}
}

func TestLoad_ServerTimeoutsEnv(t *testing.T) {
	clearEnv(t)
	os.Setenv("MUXCORE_SERVER_READ_TIMEOUT", "30")
	os.Setenv("MUXCORE_SERVER_WRITE_TIMEOUT", "60")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.ReadTimeout != 30 {
		t.Errorf("expected ReadTimeout 30, got %d", cfg.Server.ReadTimeout)
	}
	if cfg.Server.WriteTimeout != 60 {
		t.Errorf("expected WriteTimeout 60, got %d", cfg.Server.WriteTimeout)
	}
}

func TestLoad_GRPCEnv(t *testing.T) {
	clearEnv(t)
	os.Setenv("MUXCORE_GRPC_MAX_MESSAGE_SIZE_MB", "64")
	os.Setenv("MUXCORE_GRPC_CA_CERT_DIR", "/custom/ca/path")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.GRPC.MaxMessageSizeMB != 64 {
		t.Errorf("expected MaxMessageSizeMB 64, got %d", cfg.GRPC.MaxMessageSizeMB)
	}
	if cfg.GRPC.CACertDir != "/custom/ca/path" {
		t.Errorf("expected CACertDir /custom/ca/path, got %q", cfg.GRPC.CACertDir)
	}
}

func TestLoad_InvalidGRPCMessageSize_File(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	os.WriteFile(path, []byte(`{"grpc":{"max_message_size_mb":600}}`), 0600)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for message size > 512")
	}
}
