package config

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testKey = "0123456789abcdef0123456789abcdef"

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

func clearAuditEnv(t *testing.T) {
	t.Helper()
	t.Setenv("MUXCORE_AUDIT_HMAC_KEY", "")
	t.Setenv("MUXCORE_AUDIT_HMAC_KEY_FILE", "")
}

func TestResolveAuditHMACKey_None(t *testing.T) {
	clearAuditEnv(t)
	k, err := ResolveAuditHMACKey(Default())
	if err != nil || k != nil {
		t.Fatalf("got %v, %v; want nil, nil", k, err)
	}
}

func TestResolveAuditHMACKey_Env(t *testing.T) {
	clearAuditEnv(t)
	t.Setenv("MUXCORE_AUDIT_HMAC_KEY", testKey)
	k, err := ResolveAuditHMACKey(Default())
	if err != nil || string(k) != testKey {
		t.Fatalf("got %q, %v", k, err)
	}
}

func TestResolveAuditHMACKey_FileSecureNoWarning(t *testing.T) {
	clearAuditEnv(t)
	logs := captureLogs(t)
	p := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(p, []byte(testKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUXCORE_AUDIT_HMAC_KEY_FILE", p)
	k, err := ResolveAuditHMACKey(Default())
	if err != nil || string(k) != testKey {
		t.Fatalf("got %q, %v", k, err)
	}
	if strings.Contains(logs.String(), "world-accessible") {
		t.Errorf("unexpected warning: %s", logs)
	}
}

func TestResolveAuditHMACKey_WorldReadableWarnsButSucceeds(t *testing.T) {
	clearAuditEnv(t)
	logs := captureLogs(t)
	p := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(p, []byte(testKey), 0o644); err != nil { //nolint:gosec // deliberate for test
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o644); err != nil { //nolint:gosec // deliberate for test
		t.Fatal(err)
	}
	t.Setenv("MUXCORE_AUDIT_HMAC_KEY_FILE", p)
	k, err := ResolveAuditHMACKey(Default())
	if err != nil || string(k) != testKey {
		t.Fatalf("got %q, %v", k, err)
	}
	out := logs.String()
	if !strings.Contains(out, "world-accessible") {
		t.Errorf("expected world-readable warning, got: %s", out)
	}
	if strings.Contains(out, testKey) {
		t.Error("key leaked into logs")
	}
}

func TestResolveAuditHMACKey_ConfigFileField(t *testing.T) {
	clearAuditEnv(t)
	p := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(p, []byte(testKey), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	cfg.Audit.HMACKeyFile = p
	k, err := ResolveAuditHMACKey(cfg)
	if err != nil || string(k) != testKey {
		t.Fatalf("got %q, %v", k, err)
	}
}

func TestResolveAuditHMACKey_MissingOrEmptyFileFailsClosed(t *testing.T) {
	clearAuditEnv(t)
	t.Setenv("MUXCORE_AUDIT_HMAC_KEY_FILE", filepath.Join(t.TempDir(), "nope"))
	if _, err := ResolveAuditHMACKey(Default()); err == nil {
		t.Fatal("expected error for missing key file")
	}
	p := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(p, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUXCORE_AUDIT_HMAC_KEY_FILE", p)
	if _, err := ResolveAuditHMACKey(Default()); err == nil {
		t.Fatal("expected error for empty key file")
	}
}
