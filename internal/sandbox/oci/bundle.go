// Package oci generates minimal OCI runtime bundle stubs for module sandboxing.
//
// Layout written by WriteBundle:
//
//	bundle/
//	  config.json     — minimal OCI runtime-spec process + root
//	  rootfs/
//	    muxcore-module — copy of the module binary
//	    bin/sh, bin/busybox, proc, sys, dev, tmp, etc/passwd — when
//	      MUXCORE_SANDBOX_ROOTFS_AUTO=1 or MUXCORE_SANDBOX_ROOTFS_TEMPLATE is set
//
// runsc dry-run / validate (document for operators):
//
//	# After WriteBundle(dir, spec):
//	runsc --root /tmp/runsc-root spec validate --bundle "$dir"
//	# or help-only smoke when validate is unavailable:
//	runsc --help
//
// Env (optional, used by sandbox.GVisorRunner):
//
//	MUXCORE_SANDBOX_OCI_AUTO=1 — generate a temp bundle and wrap with
//	  `runsc run --bundle <dir> <id>` instead of `runsc do exec`.
//	MUXCORE_SANDBOX_ROOTFS_AUTO=1 — build BusyBox-style rootfs via sandbox/rootfs
//	MUXCORE_SANDBOX_ROOTFS_TEMPLATE — copy an existing rootfs tree into the bundle
package oci

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Muxcore-Media/core/internal/sandbox/rootfs"
	"github.com/Muxcore-Media/core/internal/sandbox/seccomp"
)

// Spec describes the module process to place in a bundle.
type Spec struct {
	ModuleID string
	BinPath  string
	Args     []string
	Env      []string
	// WorkDir inside the container (default "/").
	WorkDir string
}

// Bundle is a written OCI-ish layout on disk.
type Bundle struct {
	Dir        string
	ConfigPath string
	Rootfs     string
	Binary     string
}

// WriteBundle creates config.json + rootfs with the module binary.
// When MUXCORE_SANDBOX_ROOTFS_TEMPLATE or MUXCORE_SANDBOX_ROOTFS_AUTO=1 is set,
// the rootfs is populated with a minimal BusyBox layout (see sandbox/rootfs).
func WriteBundle(dir string, spec Spec) (*Bundle, error) {
	if strings.TrimSpace(spec.BinPath) == "" {
		return nil, fmt.Errorf("oci bundle: BinPath required")
	}
	if dir == "" {
		return nil, fmt.Errorf("oci bundle: dir required")
	}
	rootfsDir := filepath.Join(dir, "rootfs")
	if err := os.MkdirAll(rootfsDir, 0o755); err != nil {
		return nil, err
	}
	if _, err := rootfs.ApplyToDir(rootfsDir); err != nil {
		return nil, fmt.Errorf("oci bundle: rootfs: %w", err)
	}
	destBin := filepath.Join(rootfsDir, "muxcore-module")
	if err := copyFile(spec.BinPath, destBin, 0o755); err != nil {
		return nil, fmt.Errorf("oci bundle: copy binary: %w", err)
	}

	cwd := spec.WorkDir
	if cwd == "" {
		cwd = "/"
	}
	args := append([]string{"/muxcore-module"}, spec.Args...)
	env := append([]string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}, spec.Env...)

	linux := map[string]any{
		"namespaces": []map[string]string{
			{"type": "pid"},
			{"type": "ipc"},
			{"type": "uts"},
			{"type": "mount"},
		},
	}
	// Always attach default seccomp on OCI gVisor bundles; also when SECCOMP=1.
	if seccomp.ShouldApply(true) {
		prof, err := seccomp.LoadDefault()
		if err != nil {
			return nil, err
		}
		if override := seccomp.PathOverride(); override != "" {
			raw, err := os.ReadFile(override)
			if err != nil {
				return nil, fmt.Errorf("seccomp profile override: %w", err)
			}
			var custom seccomp.Profile
			if err := json.Unmarshal(raw, &custom); err != nil {
				return nil, fmt.Errorf("seccomp profile override json: %w", err)
			}
			prof = custom
		}
		linux["seccomp"] = prof
	}
	cfg := map[string]any{
		"ociVersion": "1.0.2",
		"process": map[string]any{
			"terminal": false,
			"user":     map[string]any{"uid": 0, "gid": 0},
			"args":     args,
			"env":      env,
			"cwd":      cwd,
		},
		"root": map[string]any{
			"path":     "rootfs",
			"readonly": false,
		},
		"hostname": sanitizeHostname(spec.ModuleID),
		"linux":    linux,
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, err
	}
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, raw, 0o644); err != nil {
		return nil, err
	}
	return &Bundle{Dir: dir, ConfigPath: configPath, Rootfs: rootfsDir, Binary: destBin}, nil
}

// ValidateBundle checks required files exist (local layout check, no runsc).
func ValidateBundle(dir string) error {
	for _, name := range []string{"config.json", "rootfs", filepath.Join("rootfs", "muxcore-module")} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("oci bundle incomplete: %s: %w", name, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return err
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("oci config.json: %w", err)
	}
	if cfg["ociVersion"] == nil || cfg["process"] == nil || cfg["root"] == nil {
		return fmt.Errorf("oci config.json missing required fields")
	}
	return nil
}

// RunscValidateArgs returns argv for `runsc … spec validate --bundle dir`
// (operators / CI). Does not execute.
func RunscValidateArgs(runscBin, bundleDir string) []string {
	if runscBin == "" {
		runscBin = "runsc"
	}
	return []string{runscBin, "spec", "validate", "--bundle", bundleDir}
}

// RunscRunArgs returns argv to start a container from a bundle.
func RunscRunArgs(runscBin, bundleDir, containerID string) []string {
	if runscBin == "" {
		runscBin = "runsc"
	}
	if containerID == "" {
		containerID = "muxcore-module"
	}
	return []string{runscBin, "run", "--bundle", bundleDir, containerID}
}

// LookPathRunsc resolves runsc (or override). Returns error if missing.
func LookPathRunsc(bin string) (string, error) {
	if bin == "" {
		bin = "runsc"
	}
	return exec.LookPath(bin)
}

// TryRunscValidate shells out to runsc spec validate when available.
// Returns (skipped=true, nil) when runsc is not on PATH.
func TryRunscValidate(runscBin, bundleDir string) (skipped bool, err error) {
	path, err := LookPathRunsc(runscBin)
	if err != nil {
		return true, nil
	}
	args := RunscValidateArgs(path, bundleDir)
	cmd := exec.Command(args[0], args[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Some runsc builds lack `spec validate`; treat as soft skip with note.
		if strings.Contains(string(out), "unknown") || strings.Contains(err.Error(), "unknown") {
			return true, nil
		}
		return false, fmt.Errorf("runsc validate: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return false, nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func sanitizeHostname(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "muxcore-module"
	}
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := b.String()
	if len(out) > 63 {
		out = out[:63]
	}
	return out
}
