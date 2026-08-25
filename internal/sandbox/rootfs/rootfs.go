// Package rootfs builds a minimal Linux root filesystem for module sandboxing
// (gVisor/runsc OCI bundles and Firecracker helper root paths).
//
// This is a BusyBox-oriented baseline — not a full distro image. Operators who
// need package managers, glibc userspace, or vsock networking still supply
// their own images.
//
// Modes:
//
//	A (fixture/CI): no busybox source set → embed a stub /bin/busybox + /bin/sh
//	B (operator): MUXCORE_SANDBOX_ROOTFS_BUSYBOX (local static binary) or
//	  MUXCORE_SANDBOX_ROOTFS_BUSYBOX_URL (HTTP(S) download) → real busybox
//
// Env:
//
//	MUXCORE_SANDBOX_ROOTFS_AUTO=1 — callers (oci.WriteBundle, FirecrackerRunner)
//	  invoke MakeRootfs when building sandbox trees
//	MUXCORE_SANDBOX_ROOTFS_TEMPLATE — copy an existing rootfs tree instead of MakeRootfs
//	MUXCORE_SANDBOX_ROOTFS_BUSYBOX — path to a static busybox binary
//	MUXCORE_SANDBOX_ROOTFS_BUSYBOX_URL — URL of a static busybox binary
package rootfs

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StubBusybox is the Mode A fixture binary (shell script). Exported for tests.
const StubBusybox = `#!/bin/sh
# MuxCore stub busybox — fixture/CI only. Not a real multi-call BusyBox.
# Operator mode: set MUXCORE_SANDBOX_ROOTFS_BUSYBOX or MUXCORE_SANDBOX_ROOTFS_BUSYBOX_URL.
prog=$(basename "$0")
if [ "$prog" = "busybox" ] && [ "$#" -gt 0 ]; then
  prog=$1
  shift
fi
case "$prog" in
  sh|ash|busybox|"")
    if [ "$1" = "-c" ] && [ -n "$2" ]; then
      eval "$2"
      exit $?
    fi
    echo "muxcore-stub-busybox"
    exit 0
    ;;
  true) exit 0 ;;
  false) exit 1 ;;
  echo) echo "$@"; exit 0 ;;
  *)
    echo "muxcore stub busybox: applet '$prog' not implemented" >&2
    exit 127
    ;;
esac
`

//nolint:gosec // fixture /etc/passwd stub for sandbox rootfs
const passwdContents = `root:x:0:0:root:/root:/bin/sh
`

// Layout directories created under the rootfs.
var layoutDirs = []string{
	"bin",
	"dev",
	"etc",
	"proc",
	"sys",
	"tmp",
	"root",
	"usr/bin",
}

// MakeRootfs writes a minimal BusyBox-style rootfs into dir (created if needed).
// Mode A synthesizes a stub busybox; Mode B copies/downloads a real static binary.
func MakeRootfs(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return fmt.Errorf("rootfs: dir required")
	}
	for _, d := range layoutDirs {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil { //nolint:gosec // world-readable dirs for container rootfs
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "etc", "passwd"), []byte(passwdContents), 0o644); err != nil { //nolint:gosec // fixture passwd for sandbox
		return err
	}
	busyboxPath := filepath.Join(dir, "bin", "busybox")
	if err := installBusybox(busyboxPath); err != nil {
		return err
	}
	shPath := filepath.Join(dir, "bin", "sh")
	_ = os.Remove(shPath)
	if err := os.Symlink("busybox", shPath); err != nil {
		// Fallback when symlink is unsupported: copy the busybox file.
		if err := copyFile(busyboxPath, shPath, 0o755); err != nil {
			return fmt.Errorf("rootfs: install /bin/sh: %w", err)
		}
	}
	return Validate(dir)
}

// AutoEnabled reports MUXCORE_SANDBOX_ROOTFS_AUTO=1.
func AutoEnabled() bool {
	return os.Getenv("MUXCORE_SANDBOX_ROOTFS_AUTO") == "1"
}

// TemplatePath returns MUXCORE_SANDBOX_ROOTFS_TEMPLATE (trimmed), or empty.
func TemplatePath() string {
	return strings.TrimSpace(os.Getenv("MUXCORE_SANDBOX_ROOTFS_TEMPLATE"))
}

// ApplyToDir populates dest using TEMPLATE (copy) or MakeRootfs when AUTO=1.
// When neither is set, dest is left unchanged (caller may only place a module binary).
// Returns true when a rootfs layout was applied.
func ApplyToDir(dest string) (applied bool, err error) {
	if tmpl := TemplatePath(); tmpl != "" {
		if err := CopyTree(tmpl, dest); err != nil {
			return false, fmt.Errorf("rootfs template %q: %w", tmpl, err)
		}
		return true, Validate(dest)
	}
	if AutoEnabled() {
		if err := MakeDistroRootfs(dest, ProfileFromEnv()); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// Validate checks the minimal BusyBox layout exists under dir.
func Validate(dir string) error {
	required := []string{
		filepath.Join("bin", "busybox"),
		filepath.Join("bin", "sh"),
		filepath.Join("etc", "passwd"),
		"proc",
		"sys",
		"dev",
		"tmp",
	}
	for _, rel := range required {
		p := filepath.Join(dir, rel)
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("rootfs incomplete: %s: %w", rel, err)
		}
	}
	return nil
}

// CopyTree copies src directory tree into dest (dest is created).
func CopyTree(src, dest string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", src)
	}
	return filepath.Walk(src, func(path string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if fi.IsDir() {
			return os.MkdirAll(target, fi.Mode().Perm()|0o755)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			_ = os.Remove(target)
			return os.Symlink(link, target) //nolint:gosec // preserve symlinks when copying template tree
		}
		return copyFile(path, target, fi.Mode().Perm())
	})
}

func installBusybox(dest string) error {
	if local := strings.TrimSpace(os.Getenv("MUXCORE_SANDBOX_ROOTFS_BUSYBOX")); local != "" {
		return copyFile(local, dest, 0o755)
	}
	if url := strings.TrimSpace(os.Getenv("MUXCORE_SANDBOX_ROOTFS_BUSYBOX_URL")); url != "" {
		return downloadFile(url, dest, 0o755)
	}
	return os.WriteFile(dest, []byte(StubBusybox), 0o755) //nolint:gosec // executable stub busybox fixture
}

func downloadFile(url, dest string, mode os.FileMode) error {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url) //nolint:gosec // operator-controlled MUXCORE_SANDBOX_ROOTFS_BUSYBOX_URL
	if err != nil {
		return fmt.Errorf("rootfs busybox download: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("rootfs busybox download: HTTP %d", resp.StatusCode)
	}
	if mkdirErr := os.MkdirAll(filepath.Dir(dest), 0o755); mkdirErr != nil { //nolint:gosec // container rootfs layout
		return mkdirErr
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode) //nolint:gosec // dest under controlled rootfs dir
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, resp.Body); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil { //nolint:gosec // container rootfs layout
		return err
	}
	in, err := os.Open(src) //nolint:gosec // src under controlled rootfs or operator path
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode) //nolint:gosec // dst under controlled rootfs dir
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}
