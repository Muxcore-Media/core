package startup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Muxcore-Media/core/internal/config"
)

func TestRunAll_NoFatalOnDefaultConfig(t *testing.T) {
	cfg := config.Default()
	results := RunAll(cfg, "")
	if HasFatal(results) {
		for _, r := range results {
			if r.Fatal != nil {
				t.Errorf("unexpected fatal check %q: %v", r.Name, r.Fatal)
			}
		}
	}
}

func TestHasFatal(t *testing.T) {
	r1 := make([]Result, 0, 2)
	r1 = append(r1, Result{Name: "a"}, Result{Name: "b", Warning: "warn"})
	if HasFatal(r1) {
		t.Error("HasFatal should be false with no fatal results")
	}
	r2 := append(r1, Result{Name: "c", Fatal: os.ErrPermission})
	if !HasFatal(r2) {
		t.Error("HasFatal should be true with a fatal result")
	}
}

func TestFatalErrors(t *testing.T) {
	results := []Result{
		{Name: "a"},
		{Name: "b", Fatal: os.ErrPermission},
		{Name: "c", Fatal: os.ErrNotExist},
	}
	errs := FatalErrors(results)
	if len(errs) != 2 {
		t.Errorf("expected 2 fatal errors, got %d", len(errs))
	}
}

func TestCheckModuleCache_WritableDir(t *testing.T) {
	// Override UserHomeDir by setting HOME to a temp dir.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	r := checkModuleCache(config.Default())
	if r.Fatal != nil {
		t.Errorf("checkModuleCache failed on writable temp dir: %v", r.Fatal)
	}
}

func TestCheckAuditLogDir_Disabled(t *testing.T) {
	cfg := config.Default()
	cfg.Audit.Path = "" // disabled
	r := checkAuditLogDir(cfg)
	if r.Fatal != nil || r.Warning != "" {
		t.Errorf("disabled audit should pass cleanly, got fatal=%v warning=%q", r.Fatal, r.Warning)
	}
}

func TestCheckAuditLogDir_WritableDir(t *testing.T) {
	tmp := t.TempDir()
	cfg := config.Default()
	cfg.Audit.Path = filepath.Join(tmp, "audit.jsonl")

	r := checkAuditLogDir(cfg)
	if r.Fatal != nil {
		t.Errorf("writable audit dir should pass, got %v", r.Fatal)
	}
}

func TestCheckAuditLogDir_UnwritableDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("cannot test unwritable dirs as root")
	}
	tmp := t.TempDir()
	unwritable := filepath.Join(tmp, "locked")
	if err := os.Mkdir(unwritable, 0444); err != nil { //nolint:gosec // intentional: testing unwritable dir error path
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Audit.Path = filepath.Join(unwritable, "subdir", "audit.jsonl")

	r := checkAuditLogDir(cfg)
	if r.Fatal == nil {
		t.Error("expected fatal for unwritable audit dir")
	}
}

func TestCheckCosignPub_NotFound(t *testing.T) {
	// Run from a temp dir where cosign.pub doesn't exist.
	tmp := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(tmp)
	defer os.Chdir(orig)

	r := checkCosignPub(config.Default())
	if r.Fatal != nil {
		t.Errorf("missing cosign.pub should warn not fail, got %v", r.Fatal)
	}
	if r.Warning == "" {
		t.Error("expected warning for missing cosign.pub")
	}
}

func TestCheckCosignPub_FoundAndReadable(t *testing.T) {
	tmp := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(tmp)
	defer os.Chdir(orig)

	if err := os.WriteFile("cosign.pub", []byte("fake-key-data"), 0600); err != nil {
		t.Fatal(err)
	}

	r := checkCosignPub(config.Default())
	if r.Fatal != nil || r.Warning != "" {
		t.Errorf("readable cosign.pub should pass cleanly, got fatal=%v warning=%q", r.Fatal, r.Warning)
	}
}

func TestCheckGoVersion_ReturnsResult(t *testing.T) {
	r := checkGoVersion(config.Default())
	if r.Name != "go_version_match" {
		t.Errorf("expected name go_version_match, got %q", r.Name)
	}
	// Whether it passes or warns depends on the runtime; just check it ran.
}
