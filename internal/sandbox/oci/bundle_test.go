package oci

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteBundleAndValidate(t *testing.T) {
	t.Setenv("MUXCORE_SANDBOX_ROOTFS_AUTO", "")
	t.Setenv("MUXCORE_SANDBOX_ROOTFS_TEMPLATE", "")
	dir := t.TempDir()
	bin := filepath.Join(dir, "mod")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	bundleDir := filepath.Join(dir, "bundle")
	b, err := WriteBundle(bundleDir, Spec{
		ModuleID: "media-movies",
		BinPath:  bin,
		Args:     []string{"--grpc", ":9"},
		Env:      []string{"FOO=1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateBundle(b.Dir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(b.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{"ociVersion", "/muxcore-module", "FOO=1", "seccomp", "SCMP_ACT"} {
		if !strings.Contains(s, want) {
			t.Fatalf("config missing %q: %s", want, s)
		}
	}
}

func TestWriteBundleRootfsAuto(t *testing.T) {
	t.Setenv("MUXCORE_SANDBOX_ROOTFS_AUTO", "1")
	t.Setenv("MUXCORE_SANDBOX_ROOTFS_TEMPLATE", "")
	t.Setenv("MUXCORE_SANDBOX_ROOTFS_BUSYBOX", "")
	t.Setenv("MUXCORE_SANDBOX_ROOTFS_BUSYBOX_URL", "")
	dir := t.TempDir()
	bin := filepath.Join(dir, "mod")
	if err := os.WriteFile(bin, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	bundleDir := filepath.Join(dir, "bundle")
	b, err := WriteBundle(bundleDir, Spec{BinPath: bin, ModuleID: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateBundle(b.Dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(b.Rootfs, "bin", "sh")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(b.Rootfs, "etc", "passwd")); err != nil {
		t.Fatal(err)
	}
}

func TestLookPathRunscMock(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "runsc")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho mock\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/nonexistent")
	got, err := LookPathRunsc("runsc")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "runsc" {
		t.Fatalf("got %q", got)
	}
	_, err = LookPathRunsc(filepath.Join(dir, "missing"))
	if err == nil {
		t.Fatal("expected missing")
	}
}

func TestTryRunscValidateSkipsWhenMissing(t *testing.T) {
	t.Setenv("PATH", "/nonexistent")
	skipped, err := TryRunscValidate(filepath.Join(t.TempDir(), "no-runsc"), t.TempDir())
	if err != nil || !skipped {
		t.Fatalf("skipped=%v err=%v", skipped, err)
	}
}

func TestTryRunscValidateOptionalSmoke(t *testing.T) {
	if _, err := exec.LookPath("runsc"); err != nil {
		t.Skip("runsc not in PATH")
	}
	t.Setenv("MUXCORE_SANDBOX_ROOTFS_AUTO", "")
	t.Setenv("MUXCORE_SANDBOX_ROOTFS_TEMPLATE", "")
	dir := t.TempDir()
	bin := filepath.Join(dir, "mod")
	_ = os.WriteFile(bin, []byte("x"), 0o755)
	bundleDir := filepath.Join(dir, "b")
	if _, err := WriteBundle(bundleDir, Spec{BinPath: bin, ModuleID: "t"}); err != nil {
		t.Fatal(err)
	}
	_, _ = TryRunscValidate("runsc", bundleDir)
	if err := ValidateBundle(bundleDir); err != nil {
		t.Fatal(err)
	}
}

func TestRunscArgs(t *testing.T) {
	v := RunscValidateArgs("runsc", "/b")
	if len(v) < 4 || v[0] != "runsc" || v[len(v)-1] != "/b" {
		t.Fatalf("%v", v)
	}
	r := RunscRunArgs("", "/b", "cid")
	if r[0] != "runsc" || r[1] != "run" {
		t.Fatalf("%v", r)
	}
}
