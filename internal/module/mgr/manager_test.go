package mgr

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// --- VerifyChecksum ---

func TestVerifyChecksum_EmptyExpected(t *testing.T) {
	m := &Manager{}
	bin := &ModuleBinary{ID: "mod", Path: "/nonexistent"}
	// Empty checksum = always pass (backward compat).
	if err := m.VerifyChecksum(bin, ""); err != nil {
		t.Errorf("expected nil for empty checksum, got %v", err)
	}
}

func TestVerifyChecksum_Match(t *testing.T) {
	f, _ := os.CreateTemp(t.TempDir(), "bin-*")
	f.Write([]byte("binary content"))
	f.Close()

	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("binary content")))
	m := &Manager{}
	bin := &ModuleBinary{ID: "mod", Path: f.Name()}

	if err := m.VerifyChecksum(bin, hash); err != nil {
		t.Errorf("expected nil for matching checksum, got %v", err)
	}
}

func TestVerifyChecksum_Mismatch(t *testing.T) {
	f, _ := os.CreateTemp(t.TempDir(), "bin-*")
	f.Write([]byte("binary content"))
	f.Close()

	m := &Manager{}
	bin := &ModuleBinary{ID: "mod", Path: f.Name()}

	err := m.VerifyChecksum(bin, "deadbeefdeadbeef")
	if err == nil {
		t.Fatal("expected error for mismatched checksum")
	}
}

func TestVerifyChecksum_CaseInsensitive(t *testing.T) {
	f, _ := os.CreateTemp(t.TempDir(), "bin-*")
	f.Write([]byte("data"))
	f.Close()

	hash := fmt.Sprintf("%X", sha256.Sum256([]byte("data"))) // uppercase
	m := &Manager{}
	bin := &ModuleBinary{ID: "mod", Path: f.Name()}

	if err := m.VerifyChecksum(bin, hash); err != nil {
		t.Errorf("expected case-insensitive match, got %v", err)
	}
}

func TestVerifyChecksum_FileNotFound(t *testing.T) {
	m := &Manager{}
	bin := &ModuleBinary{ID: "mod", Path: "/nonexistent/path/to/binary"}
	err := m.VerifyChecksum(bin, "abc123")
	if err == nil {
		t.Fatal("expected error when binary file does not exist")
	}
}

// --- scanModuleSource ---

func TestScanModuleSource_Clean(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main
func main() {}
`), 0644)

	found, err := scanModuleSource(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("expected no patterns in clean code, got %v", found)
	}
}

func TestScanModuleSource_DetectsUnsafe(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "bad.go"), []byte(`package main
import "unsafe"
var _ = unsafe.Pointer(nil)
`), 0644)

	_, err := scanModuleSource(dir)
	if err == nil {
		t.Fatal("expected error for 'unsafe' import")
	}
}

func TestScanModuleSource_DetectsCgo(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "cgo.go"), []byte(`package main
// #include <stdio.h>
import "C"
`), 0644)

	_, err := scanModuleSource(dir)
	if err == nil {
		t.Fatal("expected error for cgo import")
	}
}

func TestScanModuleSource_DetectsGoGenerate(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "gen.go"), []byte(`package main
//go:generate go run tool.go
`), 0644)

	found, err := scanModuleSource(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(found) == 0 {
		t.Error("expected go:generate to be detected as a warning pattern")
	}
	if found[0] != "go:generate" {
		t.Errorf("expected 'go:generate', got %v", found)
	}
}

func TestScanModuleSource_SkipsNonGoFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte(`import "C" unsafe`), 0644)

	found, err := scanModuleSource(dir)
	if err != nil {
		t.Fatalf("unexpected error scanning non-Go file: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("non-Go files should be skipped, got %v", found)
	}
}

func TestScanModuleSource_EmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	found, err := scanModuleSource(dir)
	if err != nil {
		t.Fatalf("unexpected error scanning empty dir: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("expected no patterns in empty dir, got %v", found)
	}
}

// --- moduleIDFromRepo ---

func TestModuleIDFromRepo(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"https://github.com/Muxcore-Media/downloader-qbittorrent", "downloader-qbittorrent"},
		{"https://github.com/Muxcore-Media/downloader-qbittorrent.git", "downloader-qbittorrent"},
		{"https://github.com/Muxcore-Media/downloader-qbittorrent/", "downloader-qbittorrent"},
		{"github.com/foo/bar", "bar"},
		{"single", "single"},
	}
	for _, tc := range cases {
		got := moduleIDFromRepo(tc.input)
		if got != tc.expected {
			t.Errorf("moduleIDFromRepo(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

// --- slicesContains ---

func TestSlicesContains(t *testing.T) {
	if !slicesContains([]string{"a", "b", "c"}, "b") {
		t.Error("expected true for existing item")
	}
	if slicesContains([]string{"a", "b"}, "z") {
		t.Error("expected false for missing item")
	}
	if slicesContains(nil, "x") {
		t.Error("expected false for nil slice")
	}
}
