// Package profile resolves core's security profile (ADR-0016).
//
// MUXCORE_PROFILE selects one of two profiles:
//
//   - dev: insecure operation (MUXCORE_INSECURE_DISABLE_TLS) is allowed; core
//     prints a warning banner and /health reports "insecure":true.
//   - household: the secure default. TLS is always on (core auto-creates its
//     CA under <data dir>/ca when no certificate files are configured), the
//     insecure flag is fatal, and marketplace deploys require signatures.
//
// "staging" is an alias for household. "sqlite" and "postgres" are the
// installer's legacy database selector and are treated as unset (with a
// warning pointing at MUXCORE_DB_BACKEND).
//
// Phase-0 inference (ADR-0016 §5): when MUXCORE_PROFILE is unset, the
// profile is dev if the insecure flag is set (with a deprecation warning) and
// household otherwise.
package profile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Name is a security profile name.
type Name string

const (
	// Dev allows insecure operation with a loud warning.
	Dev Name = "dev"
	// Household is the secure default profile.
	Household Name = "household"
)

// Env var names read by Resolve.
const (
	EnvProfile        = "MUXCORE_PROFILE"
	EnvInsecure       = "MUXCORE_INSECURE_DISABLE_TLS"
	EnvInsecureLegacy = "MUXCORE_DEV_TLS_SKIP"
	EnvDataDir        = "MUXCORE_DATA_DIR"
	EnvCAExportDir    = "MUXCORE_CA_EXPORT_DIR"
	EnvDBBackend      = "MUXCORE_DB_BACKEND"
)

// DefaultDataDir is the data directory used when MUXCORE_DATA_DIR is unset,
// relative to the working directory (/app/data in the container images).
const DefaultDataDir = "data"

// Source records how the profile was resolved.
type Source string

const (
	// SourceExplicit: MUXCORE_PROFILE named the profile.
	SourceExplicit Source = "MUXCORE_PROFILE"
	// SourceAlias: MUXCORE_PROFILE used an alias (staging → household).
	SourceAlias Source = "MUXCORE_PROFILE alias"
	// SourceInferredInsecure: profile unset, insecure flag set → dev (phase 0).
	SourceInferredInsecure Source = "inferred from insecure flag (deprecated)"
	// SourceDefault: profile unset, no insecure flag → household.
	SourceDefault Source = "default"
)

// Resolved is the outcome of profile resolution.
type Resolved struct {
	Name Name
	// Source says how Name was chosen; logged at startup.
	Source Source
	// Raw is the MUXCORE_PROFILE value as given (trimmed).
	Raw string
	// Warnings are operator-facing messages to log at warn level.
	Warnings []string
	// Insecure reports whether the insecure flag (MUXCORE_INSECURE_DISABLE_TLS
	// or the deprecated MUXCORE_DEV_TLS_SKIP) is set. Never true for
	// household: Resolve returns an error instead.
	Insecure bool
}

// IsDev reports whether the profile is dev.
func (r Resolved) IsDev() bool { return r.Name == Dev }

// IsHousehold reports whether the profile is household.
func (r Resolved) IsHousehold() bool { return r.Name == Household }

// RequireMarketplaceSignatures reports whether marketplace DeployTag and
// orphan resurrection must carry a valid signature (FR-EXT-003).
func (r Resolved) RequireMarketplaceSignatures() bool { return r.Name == Household }

// ErrInsecureInHousehold is returned when the insecure flag is set in the
// household profile.
var ErrInsecureInHousehold = errors.New("insecure mode is not allowed in the household profile")

// FromEnv resolves the profile from the process environment.
func FromEnv() (Resolved, error) { return Resolve(os.Getenv) }

// insecureFlag reports whether the canonical or deprecated insecure flag is
// set, and which variable set it.
func insecureFlag(getenv func(string) string) (bool, string) {
	for _, k := range []string{EnvInsecure, EnvInsecureLegacy} {
		if v := strings.TrimSpace(getenv(k)); v == "true" || v == "1" {
			return true, k
		}
	}
	return false, ""
}

// Resolve determines the security profile from getenv. It returns an error
// for an unknown MUXCORE_PROFILE value and when the insecure flag is set in
// the household profile; both are fatal at startup.
func Resolve(getenv func(string) string) (Resolved, error) {
	raw := strings.TrimSpace(getenv(EnvProfile))
	insecure, insecureVar := insecureFlag(getenv)
	r := Resolved{Raw: raw, Insecure: insecure}

	switch strings.ToLower(raw) {
	case "dev":
		r.Name, r.Source = Dev, SourceExplicit
	case "household":
		r.Name, r.Source = Household, SourceExplicit
	case "staging":
		r.Name, r.Source = Household, SourceAlias
	case "sqlite", "postgres":
		r.Warnings = append(r.Warnings, fmt.Sprintf(
			"%s=%s is the legacy installer database selector and does not choose a security profile; "+
				"set %s=%s instead (treating %s as unset)",
			EnvProfile, raw, EnvDBBackend, strings.ToLower(raw), EnvProfile))
		r.inferUnset(insecureVar)
	case "":
		r.inferUnset(insecureVar)
	default:
		return r, fmt.Errorf("unknown %s %q: valid values are dev, household (staging is an alias for household)", EnvProfile, raw)
	}

	if r.Name == Household && r.Insecure {
		return r, fmt.Errorf("%w: %s is set but the profile is household (%s) — "+
			"unset %s, or set %s=dev for a development install",
			ErrInsecureInHousehold, insecureVar, r.Source, insecureVar, EnvProfile)
	}
	return r, nil
}

func (r *Resolved) inferUnset(insecureVar string) {
	if r.Insecure {
		r.Name, r.Source = Dev, SourceInferredInsecure
		r.Warnings = append(r.Warnings, fmt.Sprintf(
			"%s is unset; inferring the dev profile from %s. This inference is deprecated and will be "+
				"removed (ADR-0016 phase 2) — set %s=dev explicitly",
			EnvProfile, insecureVar, EnvProfile))
		return
	}
	r.Name, r.Source = Household, SourceDefault
}

// DataDir returns core's data directory: MUXCORE_DATA_DIR, or DefaultDataDir
// relative to the working directory. The result is absolute when the working
// directory can be determined.
func DataDir(getenv func(string) string) string {
	dir := strings.TrimSpace(getenv(EnvDataDir))
	if dir == "" {
		dir = DefaultDataDir
	}
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

// CAExportDir returns MUXCORE_CA_EXPORT_DIR ("" when unset).
func CAExportDir(getenv func(string) string) string {
	return strings.TrimSpace(getenv(EnvCAExportDir))
}

// Banner is the dev-profile startup warning printed to stderr.
const Banner = `╔══════════════════════════════════════════════════════════════╗
║               DEV PROFILE — NOT FOR REAL USE                 ║
║                                                              ║
║  MUXCORE_PROFILE=dev. Insecure operation is allowed:         ║
║  mesh traffic may be plaintext and module identity may come  ║
║  from client-supplied metadata. Anyone who can reach the     ║
║  mesh port can impersonate a module.                         ║
║                                                              ║
║  Use MUXCORE_PROFILE=household (the default) for any install ║
║  that holds real data or is reachable from a network.        ║
╚══════════════════════════════════════════════════════════════╝`
