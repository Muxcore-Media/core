package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFromEnvDefaultNoop(t *testing.T) {
	t.Setenv("MUXCORE_MODULE_SANDBOX", "")
	r := FromEnv()
	if r.Mode() != ModeNone {
		t.Fatalf("mode=%s", r.Mode())
	}
	res, err := r.Wrap(context.Background(), Spec{ModuleID: "m", BinPath: "/bin/true", Args: []string{"--x"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Path != "/bin/true" || len(res.Args) != 1 || res.Args[0] != "--x" {
		t.Fatalf("unexpected %#v", res)
	}
}

func TestGVisorFailClosedMissingBinary(t *testing.T) {
	t.Setenv("MUXCORE_SANDBOX_GVISOR_BIN", filepath.Join(t.TempDir(), "missing-runsc"))
	r := GVisorRunner{}
	_, err := r.Wrap(context.Background(), Spec{BinPath: "/bin/true"})
	if err == nil {
		t.Fatal("expected error when runsc missing")
	}
}

func TestGVisorWrapUsesBinary(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "runsc")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MUXCORE_SANDBOX_OCI_AUTO", "")
	r := GVisorRunner{Bin: bin}
	res, err := r.Wrap(context.Background(), Spec{ModuleID: "x", BinPath: "/mod", Args: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != ModeGVisor || res.Path != bin {
		t.Fatalf("%#v", res)
	}
	if len(res.Args) < 3 || res.Args[0] != "do" || res.Args[2] != "/mod" {
		t.Fatalf("args=%v", res.Args)
	}
}

func TestFirecrackerFailClosed(t *testing.T) {
	t.Setenv("MUXCORE_SANDBOX_FIRECRACKER_BIN", filepath.Join(t.TempDir(), "nope"))
	t.Setenv("MUXCORE_SANDBOX_FC_ROOTFS", filepath.Join(t.TempDir(), "rootfs.ext4"))
	_, err := (FirecrackerRunner{}).Wrap(context.Background(), Spec{BinPath: "/mod"})
	if err == nil {
		t.Fatal("expected missing binary error")
	}
}

func TestFirecrackerFailClosedMissingRootfs(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "firecracker-spawn")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MUXCORE_SANDBOX_FIRECRACKER_BIN", bin)
	t.Setenv("MUXCORE_SANDBOX_FC_ROOTFS", "")
	t.Setenv("MUXCORE_SANDBOX_ROOTFS_AUTO", "")
	_, err := (FirecrackerRunner{}).Wrap(context.Background(), Spec{ModuleID: "m", BinPath: "/mod"})
	if err == nil || !strings.Contains(err.Error(), "MUXCORE_SANDBOX_FC_ROOTFS") {
		t.Fatalf("expected missing rootfs error, got %v", err)
	}

	t.Setenv("MUXCORE_SANDBOX_FC_ROOTFS", filepath.Join(dir, "missing.ext4"))
	_, err = (FirecrackerRunner{}).Wrap(context.Background(), Spec{ModuleID: "m", BinPath: "/mod"})
	if err == nil {
		t.Fatal("expected missing rootfs file error")
	}
}

func TestFirecrackerAutoRootfs(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "firecracker-spawn")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MUXCORE_SANDBOX_FIRECRACKER_BIN", bin)
	t.Setenv("MUXCORE_SANDBOX_FC_ROOTFS", "")
	t.Setenv("MUXCORE_SANDBOX_ROOTFS_AUTO", "1")
	t.Setenv("MUXCORE_SANDBOX_ROOTFS_BUSYBOX", "")
	t.Setenv("MUXCORE_SANDBOX_ROOTFS_BUSYBOX_URL", "")
	res, err := (FirecrackerRunner{Bin: bin}).Wrap(context.Background(), Spec{ModuleID: "mod1", BinPath: "/mod"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(res.Args, " ")
	if !strings.Contains(joined, "--rootfs") {
		t.Fatalf("args=%v", res.Args)
	}
	// Find rootfs path after --rootfs
	var rf string
	for i, a := range res.Args {
		if a == "--rootfs" && i+1 < len(res.Args) {
			rf = res.Args[i+1]
			break
		}
	}
	if rf == "" {
		t.Fatal("no rootfs arg")
	}
	if _, err := os.Stat(filepath.Join(rf, "bin", "busybox")); err != nil {
		t.Fatalf("auto rootfs missing busybox: %v", err)
	}
}

func TestFirecrackerWrapUsesRootfs(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "firecracker-spawn")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	rootfs := filepath.Join(dir, "rootfs.ext4")
	if err := os.WriteFile(rootfs, []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MUXCORE_SANDBOX_FIRECRACKER_BIN", bin)
	t.Setenv("MUXCORE_SANDBOX_FC_ROOTFS", rootfs)
	res, err := (FirecrackerRunner{Bin: bin}).Wrap(context.Background(), Spec{ModuleID: "mod1", BinPath: "/mod", Args: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(res.Args, " ")
	if !strings.Contains(joined, "--rootfs") || !strings.Contains(joined, rootfs) || !strings.Contains(joined, "--module") {
		t.Fatalf("args=%v", res.Args)
	}
}

func TestFromEnvSelectsGVisor(t *testing.T) {
	t.Setenv("MUXCORE_MODULE_SANDBOX", "gvisor")
	if FromEnv().Mode() != ModeGVisor {
		t.Fatal("expected gvisor")
	}
}

func TestGVisorOCIAutoBundle(t *testing.T) {
	dir := t.TempDir()
	runsc := filepath.Join(dir, "runsc")
	if err := os.WriteFile(runsc, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	mod := filepath.Join(dir, "mod")
	if err := os.WriteFile(mod, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MUXCORE_SANDBOX_OCI_AUTO", "1")
	r := GVisorRunner{Bin: runsc}
	res, err := r.Wrap(context.Background(), Spec{ModuleID: "m1", BinPath: mod, Args: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != ModeGVisor || res.Path != runsc {
		t.Fatalf("%#v", res)
	}
	joined := strings.Join(res.Args, " ")
	if !strings.Contains(joined, "run") || !strings.Contains(joined, "--bundle") {
		t.Fatalf("args=%v", res.Args)
	}
}
