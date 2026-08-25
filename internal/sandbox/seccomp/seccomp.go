// Package seccomp loads the default MuxCore module seccomp profile.
//
// Env:
//
//	MUXCORE_SANDBOX_SECCOMP=1 — apply profile on gVisor OCI wraps (and expose path
//	  for Firecracker helpers). Always applied when wrapping with gVisor OCI auto.
package seccomp

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed default.json
var defaultJSON []byte

// DefaultJSON returns the embedded default seccomp profile bytes.
func DefaultJSON() []byte {
	out := make([]byte, len(defaultJSON))
	copy(out, defaultJSON)
	return out
}

// Enabled reports whether operators opted into seccomp via env, or gVisor wrap
// should always attach the profile (caller decides).
func EnvEnabled() bool {
	return os.Getenv("MUXCORE_SANDBOX_SECCOMP") == "1"
}

// Profile is a minimal OCI linux.seccomp object (Docker/OCI shape).
type Profile map[string]any

// LoadDefault unmarshals the embedded default profile.
func LoadDefault() (Profile, error) {
	var p Profile
	if err := json.Unmarshal(defaultJSON, &p); err != nil {
		return nil, fmt.Errorf("seccomp default profile: %w", err)
	}
	if p["defaultAction"] == nil || p["syscalls"] == nil {
		return nil, fmt.Errorf("seccomp default profile missing required fields")
	}
	return p, nil
}

// WriteTemp writes the default profile to a temp file and returns its path.
// Caller should remove the parent dir when done if desired.
func WriteTemp() (path string, err error) {
	dir, err := os.MkdirTemp("", "muxcore-seccomp-*")
	if err != nil {
		return "", err
	}
	path = filepath.Join(dir, "default.json")
	if err := os.WriteFile(path, defaultJSON, 0o644); err != nil { //nolint:gosec // embedded public seccomp profile, not secret
		_ = os.RemoveAll(dir)
		return "", err
	}
	return path, nil
}

// ShouldApply returns true when MUXCORE_SANDBOX_SECCOMP=1 or force (gVisor OCI).
func ShouldApply(force bool) bool {
	return force || EnvEnabled()
}

// PathOverride returns MUXCORE_SANDBOX_SECCOMP_PROFILE if set.
func PathOverride() string {
	return strings.TrimSpace(os.Getenv("MUXCORE_SANDBOX_SECCOMP_PROFILE"))
}
