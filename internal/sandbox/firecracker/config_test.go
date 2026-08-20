package firecracker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAndValidateConfig(t *testing.T) {
	dir := t.TempDir()
	kernel := filepath.Join(dir, "vmlinux")
	rootfs := filepath.Join(dir, "rootfs.ext4")
	for _, p := range []string{kernel, rootfs} {
		if err := os.WriteFile(p, []byte("stub"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path, err := WriteConfig(dir, Options{
		ModuleID: "mod1", RootfsPath: rootfs, KernelPath: kernel,
		VsockCID: 5, VsockUDS: filepath.Join(dir, "vs.sock"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfig(path); err != nil {
		t.Fatal(err)
	}
}

func TestWriteConfigRequiresKernel(t *testing.T) {
	dir := t.TempDir()
	rootfs := filepath.Join(dir, "rootfs.ext4")
	_ = os.WriteFile(rootfs, []byte("x"), 0o644)
	_, err := WriteConfig(dir, Options{RootfsPath: rootfs})
	if err == nil {
		t.Fatal("expected kernel error")
	}
}
