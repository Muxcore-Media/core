package tenant

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestParseStorageSyncMap(t *testing.T) {
	m, err := ParseStorageSyncMap(`{"tenant-a":{"targets":["file:///mirror/a"],"mode":"mirror","interval_sec":3600}}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(m["tenant-a"].Targets) != 1 || m["tenant-a"].IntervalSec != 3600 {
		t.Fatalf("%v", m["tenant-a"])
	}
	if _, err := ParseStorageSyncMap("{"); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestMirrorTenantFileTarget(t *testing.T) {
	t.Setenv("TENANT_MODE", "1")
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "state.db"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	mirrorRoot := t.TempDir()
	policy := StorageSyncPolicy{
		Targets: []string{"file://" + mirrorRoot},
		Mode:    "mirror",
	}
	report, err := MirrorTenant(context.Background(), "tenant-a", src, policy)
	if err != nil {
		t.Fatal(err)
	}
	if report.Copied != 1 {
		t.Fatalf("copied=%d errors=%v", report.Copied, report.Errors)
	}
	dest := filepath.Join(mirrorRoot, SafeID("tenant-a"), "state.db")
	raw, err := os.ReadFile(dest)
	if err != nil || string(raw) != "hello" {
		t.Fatalf("mirror missing: %v %q", err, raw)
	}
}

func TestMirrorTenantSkipsS3(t *testing.T) {
	src := t.TempDir()
	report, err := MirrorTenant(context.Background(), "t", src, StorageSyncPolicy{
		Targets: []string{"s3://bucket/prefix"},
		Mode:    "mirror",
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Skipped != 1 || report.Copied != 0 {
		t.Fatalf("report=%+v", report)
	}
}

func TestStorageSyncMapFromEnv(t *testing.T) {
	t.Setenv("MUXCORE_TENANT_STORAGE_SYNC", `{"default":{"targets":["file:///tmp/x"]}}`)
	m, err := StorageSyncMapFromEnv()
	if err != nil || len(m["default"].Targets) != 1 {
		t.Fatalf("%v %v", m, err)
	}
}
