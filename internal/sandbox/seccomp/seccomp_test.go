package seccomp

import "testing"

func TestLoadDefault(t *testing.T) {
	p, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	if p["defaultAction"] != "SCMP_ACT_ERRNO" {
		t.Fatalf("defaultAction=%v", p["defaultAction"])
	}
	sys, ok := p["syscalls"].([]any)
	if !ok || len(sys) == 0 {
		t.Fatalf("syscalls=%T %#v", p["syscalls"], p["syscalls"])
	}
}

func TestWriteTemp(t *testing.T) {
	path, err := WriteTemp()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { /* temp dir left for OS */ })
	if path == "" {
		t.Fatal("empty path")
	}
	raw := DefaultJSON()
	if len(raw) < 100 {
		t.Fatalf("embedded profile too small: %d", len(raw))
	}
}

func TestEnvEnabled(t *testing.T) {
	t.Setenv("MUXCORE_SANDBOX_SECCOMP", "")
	if EnvEnabled() || ShouldApply(false) {
		t.Fatal("expected off")
	}
	t.Setenv("MUXCORE_SANDBOX_SECCOMP", "1")
	if !EnvEnabled() || !ShouldApply(false) {
		t.Fatal("expected on")
	}
	if !ShouldApply(true) {
		t.Fatal("force should apply")
	}
}
