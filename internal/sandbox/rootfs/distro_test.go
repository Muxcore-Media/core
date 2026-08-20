package rootfs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMakeDistroRootfsAlpineMinimal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MUXCORE_SANDBOX_ROOTFS_BUSYBOX", "")
	t.Setenv("MUXCORE_SANDBOX_ROOTFS_BUSYBOX_URL", "")
	if err := MakeDistroRootfs(dir, ProfileAlpineMinimal); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"bin/busybox", "etc/os-release", "etc/apk/repositories", "usr/lib"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("missing %s: %v", rel, err)
		}
	}
}

func TestProfileFromEnv(t *testing.T) {
	t.Setenv("MUXCORE_SANDBOX_ROOTFS_PROFILE", "alpine-minimal")
	if ProfileFromEnv() != ProfileAlpineMinimal {
		t.Fatal("expected alpine-minimal")
	}
}
