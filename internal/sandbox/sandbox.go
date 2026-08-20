// Package sandbox provides optional process isolation wrappers for module
// spawn. Default is a no-op runner (modules inherit loom privileges).
//
// Env:
//
//	MUXCORE_MODULE_SANDBOX=none|gvisor|firecracker  (default: none)
//	MUXCORE_SANDBOX_GVISOR_BIN=runsc                (optional override)
//	MUXCORE_SANDBOX_FIRECRACKER_BIN=firecracker-spawn (optional override)
//	MUXCORE_SANDBOX_OCI_AUTO=1                      (gVisor: generate OCI bundle)
//	MUXCORE_SANDBOX_SECCOMP=1                       (apply default seccomp profile)
//	MUXCORE_SANDBOX_FC_ROOTFS=/path/to/rootfs       (Firecracker rootfs; optional if ROOTFS_AUTO=1)
//	MUXCORE_SANDBOX_ROOTFS_AUTO=1                   (BusyBox minimal rootfs via sandbox/rootfs)
//	MUXCORE_SANDBOX_ROOTFS_TEMPLATE=/path           (copy existing rootfs into OCI bundle)
//
// gVisor/Firecracker binaries are not bundled. When selected but missing,
// Wrap returns an error so operators do not silently fall back to unsandboxed
// spawn. See core/SECURITY.md and sandbox/oci, sandbox/rootfs.
package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Muxcore-Media/core/internal/sandbox/oci"
	"github.com/Muxcore-Media/core/internal/sandbox/rootfs"
	"github.com/Muxcore-Media/core/internal/sandbox/seccomp"
)

// Mode selects an isolation backend.
type Mode string

const (
	ModeNone        Mode = "none"
	ModeGVisor      Mode = "gvisor"
	ModeFirecracker Mode = "firecracker"
)

// Spec describes a module process to wrap.
type Spec struct {
	ModuleID string
	BinPath  string
	Args     []string
	Env      []string
}

// Result is the rewritten exec path/args after wrapping.
type Result struct {
	Path string
	Args []string
	Env  []string
	Mode Mode
}

// Runner rewrites spawn commands for optional isolation.
type Runner interface {
	Mode() Mode
	Wrap(ctx context.Context, spec Spec) (Result, error)
}

// NoopRunner launches the module binary directly (default).
type NoopRunner struct{}

func (NoopRunner) Mode() Mode { return ModeNone }

func (NoopRunner) Wrap(ctx context.Context, spec Spec) (Result, error) {
	_ = ctx
	return Result{Path: spec.BinPath, Args: append([]string{}, spec.Args...), Env: append([]string{}, spec.Env...), Mode: ModeNone}, nil
}

// GVisorRunner wraps with runsc (gVisor). Requires runsc on PATH or
// MUXCORE_SANDBOX_GVISOR_BIN. With MUXCORE_SANDBOX_OCI_AUTO=1, generates a
// minimal OCI bundle (see sandbox/oci) and uses `runsc run --bundle`.
// OCI bundles always embed the default seccomp profile; MUXCORE_SANDBOX_SECCOMP=1
// also attaches a --seccomp-filter path for `runsc do exec`.
type GVisorRunner struct {
	Bin string
}

func (r GVisorRunner) Mode() Mode { return ModeGVisor }

func (r GVisorRunner) Wrap(ctx context.Context, spec Spec) (Result, error) {
	_ = ctx
	bin := r.Bin
	if bin == "" {
		bin = strings.TrimSpace(os.Getenv("MUXCORE_SANDBOX_GVISOR_BIN"))
	}
	if bin == "" {
		bin = "runsc"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return Result{}, fmt.Errorf("gvisor sandbox selected but %q not found: %w", bin, err)
	}
	if os.Getenv("MUXCORE_SANDBOX_OCI_AUTO") == "1" {
		dir, err := os.MkdirTemp("", "muxcore-oci-*")
		if err != nil {
			return Result{}, fmt.Errorf("gvisor oci bundle temp: %w", err)
		}
		bundle, err := oci.WriteBundle(dir, oci.Spec{
			ModuleID: spec.ModuleID,
			BinPath:  spec.BinPath,
			Args:     spec.Args,
			Env:      spec.Env,
		})
		if err != nil {
			_ = os.RemoveAll(dir)
			return Result{}, err
		}
		if err := oci.ValidateBundle(bundle.Dir); err != nil {
			_ = os.RemoveAll(dir)
			return Result{}, err
		}
		cid := spec.ModuleID
		if cid == "" {
			cid = "muxcore-module"
		}
		args := oci.RunscRunArgs(bin, bundle.Dir, filepath.Base(cid))
		return Result{Path: args[0], Args: args[1:], Env: append([]string{}, spec.Env...), Mode: ModeGVisor}, nil
	}
	// Minimal wrap: runsc do exec — prefer MUXCORE_SANDBOX_OCI_AUTO=1 for bundles.
	args := []string{"do", "exec"}
	if seccomp.ShouldApply(false) {
		profPath, err := seccomp.WriteTemp()
		if err != nil {
			return Result{}, fmt.Errorf("gvisor seccomp: %w", err)
		}
		if override := seccomp.PathOverride(); override != "" {
			profPath = override
		}
		args = append(args, "--seccomp-filter="+profPath)
	}
	args = append(args, spec.BinPath)
	args = append(args, spec.Args...)
	return Result{Path: bin, Args: args, Env: append([]string{}, spec.Env...), Mode: ModeGVisor}, nil
}

// FirecrackerRunner wraps with a firecracker helper binary. Same posture as
// gVisor: opt-in, fail closed if binary or rootfs missing.
//
// Env:
//
//	MUXCORE_SANDBOX_FIRECRACKER_BIN — helper (default firecracker-spawn)
//	MUXCORE_SANDBOX_FC_ROOTFS — rootfs image or directory path
//	MUXCORE_SANDBOX_ROOTFS_AUTO=1 — when FC_ROOTFS unset, build a BusyBox rootfs
//	  via sandbox/rootfs and pass that directory to the helper
type FirecrackerRunner struct {
	Bin string
}

func (r FirecrackerRunner) Mode() Mode { return ModeFirecracker }

func (r FirecrackerRunner) Wrap(ctx context.Context, spec Spec) (Result, error) {
	_ = ctx
	bin := r.Bin
	if bin == "" {
		bin = strings.TrimSpace(os.Getenv("MUXCORE_SANDBOX_FIRECRACKER_BIN"))
	}
	if bin == "" {
		bin = "firecracker-spawn"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return Result{}, fmt.Errorf("firecracker sandbox selected but %q not found: %w", bin, err)
	}
	rootfsPath, err := resolveFirecrackerRootfs()
	if err != nil {
		return Result{}, err
	}
	args := []string{"--module", spec.ModuleID, "--rootfs", rootfsPath, "--", spec.BinPath}
	args = append(args, spec.Args...)
	return Result{Path: bin, Args: args, Env: append([]string{}, spec.Env...), Mode: ModeFirecracker}, nil
}

// resolveFirecrackerRootfs returns MUXCORE_SANDBOX_FC_ROOTFS, or builds a
// temporary BusyBox rootfs when MUXCORE_SANDBOX_ROOTFS_AUTO=1.
func resolveFirecrackerRootfs() (string, error) {
	rootfsPath := strings.TrimSpace(os.Getenv("MUXCORE_SANDBOX_FC_ROOTFS"))
	if rootfsPath != "" {
		if _, err := os.Stat(rootfsPath); err != nil {
			return "", fmt.Errorf("firecracker sandbox rootfs %q: %w", rootfsPath, err)
		}
		return rootfsPath, nil
	}
	if !rootfs.AutoEnabled() {
		return "", fmt.Errorf("firecracker sandbox selected but MUXCORE_SANDBOX_FC_ROOTFS is unset (set path or MUXCORE_SANDBOX_ROOTFS_AUTO=1)")
	}
	dir, err := os.MkdirTemp("", "muxcore-fc-rootfs-*")
	if err != nil {
		return "", fmt.Errorf("firecracker auto rootfs temp: %w", err)
	}
	if err := rootfs.MakeDistroRootfs(dir, rootfs.ProfileFromEnv()); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("firecracker auto rootfs: %w", err)
	}
	return dir, nil
}

// FromEnv builds a Runner from MUXCORE_MODULE_SANDBOX.
func FromEnv() Runner {
	switch Mode(strings.ToLower(strings.TrimSpace(os.Getenv("MUXCORE_MODULE_SANDBOX")))) {
	case ModeGVisor:
		return GVisorRunner{}
	case ModeFirecracker:
		return FirecrackerRunner{}
	default:
		return NoopRunner{}
	}
}
