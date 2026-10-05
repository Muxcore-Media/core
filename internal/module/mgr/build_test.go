package mgr

import (
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoToolchainFromGoMod(t *testing.T) {
	tests := []struct {
		name, in, want string
		wantErr        bool
	}{
		{"major minor", "module x\n\ngo 1.26\n", "go1.26.0", false},
		{"with patch", "module x\n\ngo 1.26.6\n", "go1.26.6", false},
		{"comment", "module x\ngo 1.25 // note\n", "go1.25.0", false},
		{"prerelease", "module x\ngo 1.27rc1\n", "go1.27rc1", false},
		{"missing", "module x\n", "", true},
		{"invalid", "module x\ngo latest\n", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := goToolchainFromGoMod(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func writeTinyModule(t *testing.T, dir, goLine string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "module"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/tiny\n\ngo "+goLine+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmd", "module", "main.go"), []byte("package main\n\nfunc main() { println(\"hi\") }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalBuildCmd(t *testing.T) {
	dir := t.TempDir()
	writeTinyModule(t, dir, "1.26")
	t.Setenv("GOFLAGS", "-mod=mod")
	t.Setenv("CGO_ENABLED", "1")
	t.Setenv("GOPRIVATE", "example.com/*")
	cmd, err := canonicalBuildCmd(dir, "/tmp/out")
	if err != nil {
		t.Fatal(err)
	}
	wantTail := []string{"build", "-trimpath", "-buildvcs=false", "-ldflags=-buildid=", "-o", "/tmp/out", "./cmd/module/"}
	if got := cmd.Args[1:]; strings.Join(got, " ") != strings.Join(wantTail, " ") {
		t.Errorf("args = %v, want %v", got, wantTail)
	}
	if cmd.Dir != dir {
		t.Errorf("Dir = %q, want %q", cmd.Dir, dir)
	}
	count := map[string]int{}
	vals := map[string]string{}
	for _, kv := range cmd.Env {
		k, v, _ := strings.Cut(kv, "=")
		count[k]++
		vals[k] = v
	}
	for k, v := range map[string]string{"CGO_ENABLED": "0", "GOFLAGS": "-mod=readonly", "GOTOOLCHAIN": "go1.26.0", "GOPRIVATE": "example.com/*"} {
		if vals[k] != v || count[k] != 1 {
			t.Errorf("env %s = %q (x%d), want %q once", k, vals[k], count[k], v)
		}
	}
	if os.Getenv("PATH") != "" && vals["PATH"] == "" {
		t.Error("PATH not preserved")
	}
}

func TestCanonicalBuildCmd_NoGoMod(t *testing.T) {
	if _, err := canonicalBuildCmd(t.TempDir(), "/tmp/out"); err == nil {
		t.Fatal("expected error for missing go.mod")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalBuildCmd(dir, "/tmp/out"); err == nil {
		t.Fatal("expected error for missing go line")
	}
}

func TestCanonicalBuild_Reproducible(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	verOut, err := exec.Command("go", "env", "GOVERSION").Output()
	if err != nil {
		t.Skipf("go env GOVERSION: %v", err)
	}
	local := strings.TrimSpace(string(verOut)) // e.g. go1.26.6
	goLine := strings.TrimPrefix(local, "go")
	if !goVersionRe.MatchString(goLine) {
		t.Skipf("unsupported local toolchain %q", local)
	}
	// Pin GOTOOLCHAIN resolution to the local toolchain: the go line equals it,
	// so no download occurs.
	sum := func() [32]byte {
		dir := t.TempDir()
		writeTinyModule(t, dir, goLine)
		out := filepath.Join(dir, "muxcore-module")
		cmd, err := canonicalBuildCmd(dir, out)
		if err != nil {
			t.Fatal(err)
		}
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("build failed (toolchain unavailable?): %v\n%s", err, b)
		}
		data, err := os.ReadFile(out) //nolint:gosec // test path
		if err != nil {
			t.Fatal(err)
		}
		return sha256.Sum256(data)
	}
	a, b := sum(), sum()
	if a != b {
		t.Errorf("builds in different dirs differ: %x vs %x", a, b)
	}
}
