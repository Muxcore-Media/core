package rootfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DistroProfile selects a rootfs layout beyond the BusyBox baseline.
type DistroProfile string

const (
	ProfileBusybox       DistroProfile = "busybox"
	ProfileAlpineMinimal DistroProfile = "alpine-minimal"
)

// ProfileFromEnv reads MUXCORE_SANDBOX_ROOTFS_PROFILE (default busybox).
func ProfileFromEnv() DistroProfile {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MUXCORE_SANDBOX_ROOTFS_PROFILE"))) {
	case "alpine", "alpine-minimal", "alpine_minimal":
		return ProfileAlpineMinimal
	default:
		return ProfileBusybox
	}
}

// MakeDistroRootfs builds a rootfs for the given profile.
func MakeDistroRootfs(dir string, profile DistroProfile) error {
	if err := MakeRootfs(dir); err != nil {
		return err
	}
	switch profile {
	case ProfileBusybox, "":
		return nil
	case ProfileAlpineMinimal:
		return applyAlpineMinimalLayout(dir)
	default:
		return fmt.Errorf("rootfs: unknown profile %q", profile)
	}
}

// applyAlpineMinimalLayout adds glibc-oriented paths operators expect for distro images.
func applyAlpineMinimalLayout(dir string) error {
	extra := []string{
		"lib",
		"lib64",
		"usr/bin",
		"usr/lib",
		"usr/local/bin",
		"var/log",
		"etc/apk",
		"etc/ssl/certs",
	}
	for _, d := range extra {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil { //nolint:gosec // container rootfs layout
			return err
		}
	}
	osRelease := "NAME=\"MuxCore Alpine-minimal\"\nID=muxcore-alpine\nVERSION=1.0\n"
	if err := os.WriteFile(filepath.Join(dir, "etc", "os-release"), []byte(osRelease), 0o644); err != nil { //nolint:gosec // fixture os-release for sandbox
		return err
	}
	apkRepos := "# MuxCore fixture alpine-minimal — operator supplies real apk repos\n"
	return os.WriteFile(filepath.Join(dir, "etc", "apk", "repositories"), []byte(apkRepos), 0o644) //nolint:gosec // fixture apk repos for sandbox
}
