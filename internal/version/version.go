// Package version provides the core build version and compatibility checks.
// The Version variable is set at build time via linker flags:
//
//	go build -ldflags="-X github.com/Muxcore-Media/core/internal/version.Version=1.0.0"
//
// When not set, the default "0.0.0-dev" indicates a development build.
package version

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is the semantic version of the running core binary.
// Set via linker flag at build time. Falls back to "0.0.0-dev".
var Version = "0.0.0-dev"

// CompatError describes why a module is incompatible with the running core.
type CompatError struct {
	ModuleID       string
	MinCoreVersion string
	CoreVersion    string
	Reason         string
}

func (e *CompatError) Error() string {
	return fmt.Sprintf("module %q requires core >= %s (running %s): %s",
		e.ModuleID, e.MinCoreVersion, e.CoreVersion, e.Reason)
}

// CheckModule ensures a module's MinCoreVersion is compatible with the
// running core version. Returns nil if compatible, or a *CompatError if not.
//
// Rules:
//   - Empty MinCoreVersion is always compatible (backward compat).
//   - Major version must match exactly. Different major = incompatible.
//   - Minor version: core.Minor >= requested.Minor.
//   - Patch version: if minor matches, core.Patch >= requested.Patch.
func CheckModule(moduleID, minCoreVersion string) error {
	if minCoreVersion == "" {
		return nil
	}

	coreVer := Version
	if coreVer == "0.0.0-dev" {
		// Development builds accept everything.
		return nil
	}

	core, err := parseSemVer(coreVer)
	if err != nil {
		// Can't parse core version — allow (shouldn't happen with linker-set versions).
		return nil
	}

	mod, err := parseSemVer(minCoreVersion)
	if err != nil {
		return &CompatError{
			ModuleID:       moduleID,
			MinCoreVersion: minCoreVersion,
			CoreVersion:    coreVer,
			Reason:         fmt.Sprintf("invalid MinCoreVersion: %v", err),
		}
	}

	// Major version must match.
	if core.major != mod.major {
		return &CompatError{
			ModuleID:       moduleID,
			MinCoreVersion: minCoreVersion,
			CoreVersion:    coreVer,
			Reason:         fmt.Sprintf("major version mismatch: module targets v%d.x, core is v%d.x", mod.major, core.major),
		}
	}

	// Minor version check: core.minor >= mod.minor.
	if core.minor < mod.minor {
		return &CompatError{
			ModuleID:       moduleID,
			MinCoreVersion: minCoreVersion,
			CoreVersion:    coreVer,
			Reason:         fmt.Sprintf("core minor version too old: need %d.%d.x, have %d.%d.x", mod.major, mod.minor, core.major, core.minor),
		}
	}

	// Patch check: if minor matches, core.patch >= mod.patch.
	if core.minor == mod.minor && core.patch < mod.patch {
		return &CompatError{
			ModuleID:       moduleID,
			MinCoreVersion: minCoreVersion,
			CoreVersion:    coreVer,
			Reason:         fmt.Sprintf("core patch version too old: need %d.%d.%d, have %d.%d.%d", mod.major, mod.minor, mod.patch, core.major, core.minor, core.patch),
		}
	}

	return nil
}

type semVer struct {
	major, minor, patch int
}

// parseSemVer parses a "v1.2.3" or "1.2.3" version string.
func parseSemVer(v string) (semVer, error) {
	v = strings.TrimPrefix(v, "v")
	parts := strings.Split(v, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return semVer{}, fmt.Errorf("expected MAJOR.MINOR[.PATCH], got %q", v)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return semVer{}, fmt.Errorf("invalid major version: %q", parts[0])
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return semVer{}, fmt.Errorf("invalid minor version: %q", parts[1])
	}

	patch := 0
	if len(parts) == 3 {
		patch, err = strconv.Atoi(parts[2])
		if err != nil {
			return semVer{}, fmt.Errorf("invalid patch version: %q", parts[2])
		}
	}

	return semVer{major: major, minor: minor, patch: patch}, nil
}

// String returns a human-readable version string.
func String() string {
	return Version
}
