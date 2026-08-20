package rootfs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMakeRootfsStubModeOffline(t *testing.T) {
	t.Setenv("MUXCORE_SANDBOX_ROOTFS_BUSYBOX", "")
	t.Setenv("MUXCORE_SANDBOX_ROOTFS_BUSYBOX_URL", "")
	dir := t.TempDir()
	if err := MakeRootfs(dir); err != nil {
		t.Fatal(err)
	}
	if err := Validate(dir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "bin", "busybox"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "MuxCore stub busybox") && !strings.Contains(string(raw), "muxcore-stub-busybox") {
		t.Fatalf("expected stub busybox contents, got %q", string(raw)[:min(80, len(raw))])
	}
	passwd, err := os.ReadFile(filepath.Join(dir, "etc", "passwd"))
	if err != nil || !strings.Contains(string(passwd), "root:") {
		t.Fatalf("passwd: %v %s", err, passwd)
	}
}

func TestMakeRootfsLocalBusybox(t *testing.T) {
	srcDir := t.TempDir()
	fake := filepath.Join(srcDir, "busybox")
	if err := os.WriteFile(fake, []byte("FAKEBUSYBOX"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUXCORE_SANDBOX_ROOTFS_BUSYBOX", fake)
	t.Setenv("MUXCORE_SANDBOX_ROOTFS_BUSYBOX_URL", "")
	dir := t.TempDir()
	if err := MakeRootfs(dir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "bin", "busybox"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "FAKEBUSYBOX" {
		t.Fatalf("got %q", raw)
	}
}

func TestApplyToDirTemplateAndAuto(t *testing.T) {
	t.Setenv("MUXCORE_SANDBOX_ROOTFS_BUSYBOX", "")
	t.Setenv("MUXCORE_SANDBOX_ROOTFS_BUSYBOX_URL", "")

	tmpl := t.TempDir()
	if err := MakeRootfs(tmpl); err != nil {
		t.Fatal(err)
	}
	// Mark template uniquely.
	if err := os.WriteFile(filepath.Join(tmpl, "etc", "muxcore-template"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MUXCORE_SANDBOX_ROOTFS_TEMPLATE", tmpl)
	t.Setenv("MUXCORE_SANDBOX_ROOTFS_AUTO", "")
	dest := filepath.Join(t.TempDir(), "rootfs")
	applied, err := ApplyToDir(dest)
	if err != nil || !applied {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "etc", "muxcore-template")); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MUXCORE_SANDBOX_ROOTFS_TEMPLATE", "")
	t.Setenv("MUXCORE_SANDBOX_ROOTFS_AUTO", "1")
	dest2 := filepath.Join(t.TempDir(), "auto")
	applied, err = ApplyToDir(dest2)
	if err != nil || !applied {
		t.Fatalf("auto applied=%v err=%v", applied, err)
	}
	if err := Validate(dest2); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MUXCORE_SANDBOX_ROOTFS_AUTO", "")
	applied, err = ApplyToDir(t.TempDir())
	if err != nil || applied {
		t.Fatalf("expected no-op applied=false, got %v %v", applied, err)
	}
}

func TestValidateRejectsEmpty(t *testing.T) {
	if err := Validate(t.TempDir()); err == nil {
		t.Fatal("expected error")
	}
}
