// Package startup provides invariant checks that run before core fully boots.
// These catch misconfigurations early — missing directories, wrong permissions,
// version mismatches — so they surface as clear log messages rather than
// cryptic runtime errors.
package startup

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Muxcore-Media/core/internal/config"
	"github.com/Muxcore-Media/core/internal/version"
)

// Result holds the outcome of a single startup check.
type Result struct {
	Name    string
	Warning string // non-fatal — logged and returned
	Fatal   error  // fatal — caller should abort startup
}

// RunAll executes all startup invariant checks against the given config.
// Returns all results. Warnings are non-fatal. Fatal errors must be handled
// by the caller (typically: log and os.Exit(1) from main).
//
// RunAll never calls os.Exit itself — that responsibility belongs to the caller.
func RunAll(cfg *config.Config) []Result {
	checks := []struct {
		name string
		fn   func(*config.Config) Result
	}{
		{"module_cache_writable", checkModuleCache},
		{"audit_log_dir_writable", checkAuditLogDir},
		{"cosign_pub_readable", checkCosignPub},
		{"go_version_match", checkGoVersion},
	}

	results := make([]Result, 0, len(checks))

	for _, c := range checks {
		r := c.fn(cfg)
		results = append(results, r)

		if r.Fatal != nil {
			slog.Error("startup check FAILED", "check", r.Name, "error", r.Fatal)
		} else if r.Warning != "" {
			slog.Warn("startup check warning", "check", r.Name, "warning", r.Warning)
		} else {
			slog.Debug("startup check passed", "check", r.Name)
		}
	}

	slog.Info("core version info",
		"version", version.String(),
		"go_version", runtime.Version(),
		"os", runtime.GOOS,
		"arch", runtime.GOARCH,
	)

	return results
}

// HasFatal reports whether any result in a RunAll slice is fatal.
func HasFatal(results []Result) bool {
	for _, r := range results {
		if r.Fatal != nil {
			return true
		}
	}
	return false
}

// FatalErrors returns all fatal errors from a RunAll slice.
func FatalErrors(results []Result) []error {
	var errs []error
	for _, r := range results {
		if r.Fatal != nil {
			errs = append(errs, r.Fatal)
		}
	}
	return errs
}

// checkModuleCache verifies the module cache directory exists and is writable.
func checkModuleCache(_ *config.Config) Result {
	r := Result{Name: "module_cache_writable"}

	home, err := os.UserHomeDir()
	if err != nil {
		home = "/tmp"
	}
	cacheDir := filepath.Join(home, ".muxcore", "modules")

	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		r.Fatal = fmt.Errorf("module cache directory %q is not writable: %w", cacheDir, err)
		return r
	}

	f, err := os.CreateTemp(cacheDir, ".startup-check-*")
	if err != nil {
		r.Fatal = fmt.Errorf("cannot write to module cache directory %q: %w", cacheDir, err)
		return r
	}
	f.Close()
	os.Remove(f.Name())

	return r
}

// checkAuditLogDir verifies the directory for the audit log file is writable.
func checkAuditLogDir(cfg *config.Config) Result {
	r := Result{Name: "audit_log_dir_writable"}

	if cfg.Audit.Path == "" {
		return r
	}

	dir := filepath.Dir(cfg.Audit.Path)

	if err := os.MkdirAll(dir, 0700); err != nil {
		r.Fatal = fmt.Errorf("audit log directory %q is not writable: %w", dir, err)
		return r
	}

	f, err := os.CreateTemp(dir, ".startup-check-*")
	if err != nil {
		r.Fatal = fmt.Errorf("cannot write to audit log directory %q: %w", dir, err)
		return r
	}
	f.Close()
	os.Remove(f.Name())

	return r
}

// checkCosignPub verifies the cosign.pub key file is readable.
func checkCosignPub(_ *config.Config) Result {
	r := Result{Name: "cosign_pub_readable"}

	locations := []string{"cosign.pub", filepath.Join("..", "cosign.pub")}

	for _, loc := range locations {
		if _, err := os.Stat(loc); err == nil {
			data, err := os.ReadFile(loc) //nolint:gosec // loc is from a hardcoded allow-list
			if err != nil {
				r.Fatal = fmt.Errorf("cosign.pub found at %q but not readable: %w", loc, err)
				return r
			}
			if len(data) == 0 {
				r.Warning = fmt.Sprintf("cosign.pub at %q is empty — spool verification will fail", loc)
			}
			return r
		}
	}

	r.Warning = "cosign.pub not found — spool module verification will use embedded key if available"
	return r
}

// checkGoVersion warns if the running Go version differs from the expected major.minor.
func checkGoVersion(_ *config.Config) Result {
	r := Result{Name: "go_version_match"}

	goVersion := runtime.Version()
	expectedMajor, expectedMinor := 1, 26

	var rtMajor, rtMinor int
	goVersion = strings.TrimPrefix(goVersion, "go")
	if _, err := fmt.Sscanf(goVersion, "%d.%d", &rtMajor, &rtMinor); err != nil {
		r.Warning = fmt.Sprintf("could not parse Go runtime version %q", runtime.Version())
		return r
	}

	if rtMajor != expectedMajor || rtMinor != expectedMinor {
		r.Warning = fmt.Sprintf(
			"Go runtime version %s differs from expected go%d.%d — build may behave unexpectedly",
			runtime.Version(), expectedMajor, expectedMinor,
		)
	}

	return r
}
