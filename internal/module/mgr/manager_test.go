package mgr

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/Muxcore-Media/core/internal/events"
	modulemgr "github.com/Muxcore-Media/core/internal/module"
	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/internal/sandbox"
	"github.com/Muxcore-Media/core/pkg/contracts"
	modulev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/module/v1"
)

// --- VerifyChecksum ---

func TestVerifyChecksum_EmptyExpected(t *testing.T) {
	m := &Manager{}
	bin := &ModuleBinary{ID: "mod", Path: "/nonexistent"}
	// Empty checksum is allowed — verification is skipped.
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

func TestVerifySignature_OptionalOff(t *testing.T) {
	t.Setenv("MUXCORE_SPOOL_REQUIRE_SIGNATURE", "")
	t.Setenv("MUXCORE_SPOOL_PUBLIC_KEY", "")
	m := &Manager{}
	bin := &ModuleBinary{ID: "mod", Path: filepath.Join(t.TempDir(), "x")}
	_ = os.WriteFile(bin.Path, []byte("x"), 0o600)
	if err := m.VerifySignature(bin, ""); err != nil {
		t.Fatal(err)
	}
}

func TestVerifySignature_NilBinary(t *testing.T) {
	m := &Manager{}
	if err := m.VerifySignature(nil, ""); err == nil {
		t.Fatal("expected nil binary error")
	}
}

// --- scanModuleSource ---

func TestScanModuleSource_Clean(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main
func main() {}
`), 0600)

	found, err := scanModuleSource(dir, DefaultScanPolicy)
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
`), 0600)

	_, err := scanModuleSource(dir, DefaultScanPolicy)
	if err == nil {
		t.Fatal("expected error for 'unsafe' import")
	}
}

func TestScanModuleSource_DetectsCgo(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "cgo.go"), []byte(`package main
// #include <stdio.h>
import "C"
`), 0600)

	_, err := scanModuleSource(dir, DefaultScanPolicy)
	if err == nil {
		t.Fatal("expected error for cgo import")
	}
}

func TestScanModuleSource_DetectsGoGenerate(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "gen.go"), []byte(`package main
//go:generate go run tool.go
`), 0600)

	_, err := scanModuleSource(dir, DefaultScanPolicy)
	if err == nil {
		t.Fatal("expected error for go:generate directive")
	}
}

func TestScanModuleSource_SkipsNonGoFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte(`import "C" unsafe`), 0600)

	found, err := scanModuleSource(dir, DefaultScanPolicy)
	if err != nil {
		t.Fatalf("unexpected error scanning non-Go file: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("non-Go files should be skipped, got %v", found)
	}
}

func TestScanModuleSource_EmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	found, err := scanModuleSource(dir, DefaultScanPolicy)
	if err != nil {
		t.Fatalf("unexpected error scanning empty dir: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("expected no patterns in empty dir, got %v", found)
	}
}

func TestScanModuleSource_UnsafeInCommentAllowed(t *testing.T) {
	dir := t.TempDir()
	// "unsafe" appears only in a comment and a string literal — neither is an import.
	os.WriteFile(filepath.Join(dir, "safe.go"), []byte(`package main

// This function does NOT use the unsafe package internally.
// See docs for details on memory safety.
const note = "avoiding unsafe pointer arithmetic"

func main() {}
`), 0600)

	found, err := scanModuleSource(dir, DefaultScanPolicy)
	if err != nil {
		t.Fatalf("comment/string containing 'unsafe' should not trigger rejection: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("expected no patterns, got %v", found)
	}
}

func TestScanModuleSource_BlankUnsafeImportRejected(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "blank.go"), []byte(`package main

import _ "unsafe"
`), 0600)

	_, err := scanModuleSource(dir, DefaultScanPolicy)
	if err == nil {
		t.Fatal("blank import of 'unsafe' should be rejected")
	}
}

// --- ModuleIDFromRepo ---

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
		got := ModuleIDFromRepo(tc.input)
		if got != tc.expected {
			t.Errorf("ModuleIDFromRepo(%q) = %q, want %q", tc.input, got, tc.expected)
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

// --- NewManager ---

func TestNewManager(t *testing.T) {
	m := NewManager("127.0.0.1:9000", nil, nil)
	if m == nil {
		t.Fatal("NewManager returned nil")
	}
	if m.SpawnCount() != 0 {
		t.Errorf("SpawnCount = %d, want 0", m.SpawnCount())
	}
	if m.RestartCount() != 0 {
		t.Errorf("RestartCount = %d, want 0", m.RestartCount())
	}
	if m.ResolveCount() != 0 {
		t.Errorf("ResolveCount = %d, want 0", m.ResolveCount())
	}
	if m.ScanPolicy != DefaultScanPolicy {
		t.Errorf("ScanPolicy = %+v, want DefaultScanPolicy", m.ScanPolicy)
	}
}

// --- SetAllowedRepoHosts ---

func TestSetAllowedRepoHosts(t *testing.T) {
	m := NewManager("addr", nil, nil)
	m.SetAllowedRepoHosts([]string{"github.com", "gitlab.com"})
	if len(m.allowedRepoHosts) != 2 {
		t.Errorf("allowedRepoHosts len = %d, want 2", len(m.allowedRepoHosts))
	}
	if m.allowedRepoHosts[0] != "github.com" || m.allowedRepoHosts[1] != "gitlab.com" {
		t.Errorf("allowedRepoHosts = %v, want [github.com gitlab.com]", m.allowedRepoHosts)
	}
}

func TestSetAllowedRepoHosts_ClearsOnEmpty(t *testing.T) {
	m := NewManager("addr", nil, nil)
	m.SetAllowedRepoHosts([]string{"github.com"})
	m.SetAllowedRepoHosts(nil)
	if m.allowedRepoHosts != nil {
		t.Error("expected nil after SetAllowedRepoHosts(nil)")
	}
	m.SetAllowedRepoHosts([]string{})
	if m.allowedRepoHosts != nil {
		t.Error("expected nil after SetAllowedRepoHosts([])")
	}
}

// --- SetCommandRunner ---

func TestSetCommandRunner(t *testing.T) {
	m := NewManager("addr", nil, nil)
	runner := execCommandRunner{}
	m.SetCommandRunner(runner)
	if m.cmdRunner == nil {
		t.Error("expected cmdRunner to be set")
	}
}

// --- Spawn ---

type spawnMockRunner struct{}

func (spawnMockRunner) CommandContext(ctx context.Context, name string, arg ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "echo", "mock-module-output")
}

type longRunningRunner struct{}

func (longRunningRunner) CommandContext(ctx context.Context, name string, arg ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "sleep", "3600")
}

func TestSpawn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	m := NewManager("127.0.0.1:9000", nil, nil)
	m.SetCommandRunner(spawnMockRunner{})

	bin := &ModuleBinary{
		ID:            "test-mod",
		Version:       "v1.0.0",
		Path:          "/fake/path",
		RestartPolicy: RestartNever,
	}

	if err := m.Spawn(ctx, bin); err != nil {
		t.Fatalf("Spawn() = %v, want nil", err)
	}

	if got := m.SpawnCount(); got != 1 {
		t.Errorf("SpawnCount = %d, want 1", got)
	}
}

func TestSpawnSandboxWrapFailsClosed(t *testing.T) {
	m := NewManager("127.0.0.1:9090", nil, nil)
	m.SetSandboxRunner(failSandbox{})
	bin := &ModuleBinary{ID: "sandbox-fail", Path: "/bin/true"}
	err := m.Spawn(context.Background(), bin)
	if err == nil || !strings.Contains(err.Error(), "sandbox wrap") {
		t.Fatalf("expected sandbox wrap error, got %v", err)
	}
}

func TestSpawnGVisorModeFailsClosedWithoutRunsc(t *testing.T) {
	t.Setenv("MUXCORE_MODULE_SANDBOX", "gvisor")
	t.Setenv("MUXCORE_SANDBOX_GVISOR_BIN", filepath.Join(t.TempDir(), "missing-runsc"))
	m := NewManager("127.0.0.1:9091", nil, nil)
	m.SetSandboxRunner(sandbox.FromEnv())
	err := m.Spawn(context.Background(), &ModuleBinary{ID: "gvisor-missing", Path: "/bin/true"})
	if err == nil {
		t.Fatal("expected fail-closed when runsc missing")
	}
	if !strings.Contains(err.Error(), "sandbox wrap") && !strings.Contains(err.Error(), "gvisor") {
		t.Fatalf("unexpected error: %v", err)
	}
}

type failSandbox struct{}

func (failSandbox) Mode() sandbox.Mode { return sandbox.ModeGVisor }

func (failSandbox) Wrap(ctx context.Context, spec sandbox.Spec) (sandbox.Result, error) {
	return sandbox.Result{}, fmt.Errorf("refused")
}

func TestSpawn_Duplicate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	m := NewManager("127.0.0.1:9000", nil, nil)
	m.SetCommandRunner(longRunningRunner{})

	bin := &ModuleBinary{
		ID:            "dup-mod",
		Version:       "v1.0.0",
		Path:          "/fake/path",
		RestartPolicy: RestartNever,
	}

	if err := m.Spawn(ctx, bin); err != nil {
		t.Fatalf("first Spawn() = %v, want nil", err)
	}

	err := m.Spawn(ctx, bin)
	if err == nil {
		t.Error("expected error for duplicate spawn, got nil")
	}
}

// --- PruneCache ---

func TestPruneCache_NoDir(t *testing.T) {
	m := &Manager{cacheDir: "/nonexistent/cache/path"}
	if err := m.PruneCache(5); err != nil {
		t.Errorf("PruneCache with missing dir = %v, want nil", err)
	}
}

func TestPruneCache_EmptyCache(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{cacheDir: dir}
	if err := m.PruneCache(5); err != nil {
		t.Errorf("PruneCache empty = %v, want nil", err)
	}
}

func TestPruneCache_KeepAll(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{cacheDir: dir}
	moduleDir := filepath.Join(dir, "test-mod")
	for _, v := range []string{"v1.0.0", "v1.1.0", "v1.2.0"} {
		os.MkdirAll(filepath.Join(moduleDir, v), 0700)
	}
	if err := m.PruneCache(3); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"v1.0.0", "v1.1.0", "v1.2.0"} {
		if _, err := os.Stat(filepath.Join(moduleDir, v)); err != nil {
			t.Errorf("expected version %s to exist: %v", v, err)
		}
	}
}

func TestPruneCache_KeepOne(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{cacheDir: dir}
	moduleDir := filepath.Join(dir, "test-mod")
	for _, v := range []string{"v1.0.0", "v1.1.0", "v1.2.0"} {
		os.MkdirAll(filepath.Join(moduleDir, v), 0700)
	}
	if err := m.PruneCache(1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(moduleDir, "v1.2.0")); err != nil {
		t.Error("expected v1.2.0 to be kept")
	}
	if _, err := os.Stat(filepath.Join(moduleDir, "v1.0.0")); !os.IsNotExist(err) {
		t.Error("expected v1.0.0 to be pruned")
	}
	if _, err := os.Stat(filepath.Join(moduleDir, "v1.1.0")); !os.IsNotExist(err) {
		t.Error("expected v1.1.0 to be pruned")
	}
}

func TestPruneCache_KeepZero(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{cacheDir: dir}
	moduleDir := filepath.Join(dir, "test-mod")
	for _, v := range []string{"v1.0.0", "v1.1.0"} {
		os.MkdirAll(filepath.Join(moduleDir, v), 0700)
	}
	if err := m.PruneCache(0); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(moduleDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) > 0 {
		t.Errorf("expected no versions after prune(0), got %d", len(entries))
	}
}

func TestPruneCache_SkipsFiles(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{cacheDir: dir}
	os.WriteFile(filepath.Join(dir, "not-a-module"), []byte("content"), 0600)
	if err := m.PruneCache(5); err != nil {
		t.Errorf("PruneCache with files = %v, want nil", err)
	}
}

func TestPruneCache_SkipsMultipleModules(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{cacheDir: dir}
	for _, mod := range []string{"modA", "modB", "modC"} {
		for _, v := range []string{"v1.0.0", "v1.1.0"} {
			os.MkdirAll(filepath.Join(dir, mod, v), 0700)
		}
	}
	if err := m.PruneCache(1); err != nil {
		t.Fatal(err)
	}
	for _, mod := range []string{"modA", "modB", "modC"} {
		entries, _ := os.ReadDir(filepath.Join(dir, mod))
		if len(entries) != 1 {
			t.Errorf("module %s: expected 1 version after prune, got %d", mod, len(entries))
		}
	}
}

// --- TrackProxy ---

func TestTrackProxy_NoPending(t *testing.T) {
	m := NewManager("addr", nil, nil)
	proxy := NewSidecarProxy(contracts.ModuleInfo{ID: "test-mod"})
	m.TrackProxy("test-mod", proxy)

	m.mu.Lock()
	p, exists := m.proxies["test-mod"]
	m.mu.Unlock()
	if !exists {
		t.Error("expected proxy to be stored in m.proxies")
	}
	if p != proxy {
		t.Error("expected stored proxy to be the same instance")
	}
}

func TestTrackProxy_WithPending(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	m := NewManager("addr", nil, nil)
	m.SetCommandRunner(longRunningRunner{})

	bin := &ModuleBinary{
		ID:            "pending-mod",
		Version:       "v1.0.0",
		Path:          "/fake/path",
		RestartPolicy: RestartNever,
	}
	if err := m.Spawn(ctx, bin); err != nil {
		t.Fatalf("Spawn() = %v, want nil", err)
	}

	// Spawn stores process in pendingProcesses because proxy doesn't exist yet.
	m.mu.Lock()
	_, pendingExists := m.pendingProcesses["pending-mod"]
	m.mu.Unlock()
	if !pendingExists {
		t.Skip("process already exited — race between spawn and track")
	}

	proxy := NewSidecarProxy(contracts.ModuleInfo{ID: "pending-mod"})
	m.TrackProxy("pending-mod", proxy)

	// After TrackProxy, pending should be cleared.
	m.mu.Lock()
	_, pendingStillExists := m.pendingProcesses["pending-mod"]
	m.mu.Unlock()
	if pendingStillExists {
		t.Error("expected pendingProcesses to be cleared after TrackProxy")
	}

	// Proxy should report healthy because process is still running.
	if err := proxy.Health(context.Background()); err != nil {
		t.Errorf("expected nil health for running process, got %v", err)
	}
}

// --- infoFromProto ---

func TestInfoFromProto_AllFields(t *testing.T) {
	proto := &modulev1.ModuleInfo{
		Id:             "test-mod",
		Name:           "Test Module",
		Version:        "v1.0.0",
		Roles:          []string{"worker", "provider"},
		Description:    "A test module for testing",
		Author:         "dev@muxcore.io",
		Capabilities:   []string{"storage", "compute"},
		DependsOn:      []string{"foundation"},
		MinCoreVersion: "v0.1.0",
		HttpAddr:       ":9090",
	}
	want := contracts.ModuleInfo{
		ID:             "test-mod",
		Name:           "Test Module",
		Version:        "v1.0.0",
		Roles:          []string{"worker", "provider"},
		Description:    "A test module for testing",
		Author:         "dev@muxcore.io",
		Capabilities:   []string{"storage", "compute"},
		DependsOn:      []string{"foundation"},
		MinCoreVersion: "v0.1.0",
		HTTPAddr:       ":9090",
	}
	got := infoFromProto(proto)

	if got.ID != want.ID {
		t.Errorf("ID = %q, want %q", got.ID, want.ID)
	}
	if got.Name != want.Name {
		t.Errorf("Name = %q, want %q", got.Name, want.Name)
	}
	if got.Version != want.Version {
		t.Errorf("Version = %q, want %q", got.Version, want.Version)
	}
	if got.Description != want.Description {
		t.Errorf("Description = %q, want %q", got.Description, want.Description)
	}
	if got.Author != want.Author {
		t.Errorf("Author = %q, want %q", got.Author, want.Author)
	}
	if got.MinCoreVersion != want.MinCoreVersion {
		t.Errorf("MinCoreVersion = %q, want %q", got.MinCoreVersion, want.MinCoreVersion)
	}
	if got.HTTPAddr != want.HTTPAddr {
		t.Errorf("HTTPAddr = %q, want %q", got.HTTPAddr, want.HTTPAddr)
	}
}

func TestInfoFromProto_Empty(t *testing.T) {
	info := infoFromProto(&modulev1.ModuleInfo{})
	if info.ID != "" {
		t.Errorf("ID = %q, want empty", info.ID)
	}
	if info.Name != "" {
		t.Errorf("Name = %q, want empty", info.Name)
	}
	if info.Version != "" {
		t.Errorf("Version = %q, want empty", info.Version)
	}
	if len(info.Roles) != 0 {
		t.Errorf("Roles = %v, want empty", info.Roles)
	}
	if info.Description != "" {
		t.Errorf("Description = %q, want empty", info.Description)
	}
	if info.Author != "" {
		t.Errorf("Author = %q, want empty", info.Author)
	}
	if len(info.Capabilities) != 0 {
		t.Errorf("Capabilities = %v, want empty", info.Capabilities)
	}
	if len(info.DependsOn) != 0 {
		t.Errorf("DependsOn = %v, want empty", info.DependsOn)
	}
	if info.MinCoreVersion != "" {
		t.Errorf("MinCoreVersion = %q, want empty", info.MinCoreVersion)
	}
	if info.HTTPAddr != "" {
		t.Errorf("HTTPAddr = %q, want empty", info.HTTPAddr)
	}
}

// --- reconcileContracts ---

func TestReconcileContracts_NoMuxcoreJSON(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{}
	if err := m.reconcileContracts(dir); err != nil {
		t.Errorf("reconcileContracts with no muxcore.json = %v, want nil", err)
	}
}

func TestReconcileContracts_EmptyContracts(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "muxcore.json"), []byte(`{"contracts": []}`), 0600); err != nil {
		t.Fatal(err)
	}
	m := &Manager{}
	if err := m.reconcileContracts(dir); err != nil {
		t.Errorf("reconcileContracts with empty contracts = %v, want nil", err)
	}
}

func TestReconcileContracts_MissingContractsField(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "muxcore.json"), []byte(`{"name": "test"}`), 0600); err != nil {
		t.Fatal(err)
	}
	m := &Manager{}
	if err := m.reconcileContracts(dir); err != nil {
		t.Errorf("reconcileContracts with no contracts field = %v, want nil", err)
	}
}

func TestReconcileContracts_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "muxcore.json"), []byte(`not valid json`), 0600); err != nil {
		t.Fatal(err)
	}
	m := &Manager{}
	if err := m.reconcileContracts(dir); err == nil {
		t.Error("reconcileContracts with invalid JSON: expected error, got nil")
	}
}

// --- ModuleIDFromRepoWithInstance ---

func TestModuleIDFromRepoWithInstance(t *testing.T) {
	cases := []struct {
		repo       string
		instanceID string
		expected   string
	}{
		{"https://github.com/Muxcore-Media/downloader", "", "downloader"},
		{"https://github.com/Muxcore-Media/downloader", "inst1", "downloader-inst1"},
		{"https://github.com/Muxcore-Media/downloader.git", "v2", "downloader-v2"},
	}
	for _, tc := range cases {
		got := ModuleIDFromRepoWithInstance(tc.repo, tc.instanceID)
		if got != tc.expected {
			t.Errorf("ModuleIDFromRepoWithInstance(%q, %q) = %q, want %q", tc.repo, tc.instanceID, got, tc.expected)
		}
	}
}

func TestModuleIDFromRepoWithInstance_Sanitizes(t *testing.T) {
	got := ModuleIDFromRepoWithInstance("https://github.com/owner/mod", "bad/instance;id&")
	if got != "mod-badinstanceid" {
		t.Errorf("sanitized instance ID = %q, want %q", got, "mod-badinstanceid")
	}
}

// --- SetWatchdogPath ---

func TestSetWatchdogPath(t *testing.T) {
	m := NewManager("addr", nil, nil)
	m.SetWatchdogPath("/usr/bin/watchdog")
	if m.watchdogPath != "/usr/bin/watchdog" {
		t.Errorf("watchdogPath = %q, want %q", m.watchdogPath, "/usr/bin/watchdog")
	}
}

// --- SetTag ---

func TestSetTag(t *testing.T) {
	m := NewManager("addr", nil, nil)
	tag := &contracts.TagDefinition{
		Name: "test-tag",
		Modules: []contracts.TagModule{
			{Repo: "https://github.com/Muxcore-Media/modA", Version: "v1.0.0"},
			{Repo: "https://github.com/Muxcore-Media/modB", Version: "v2.0.0"},
		},
	}
	m.SetTag(tag)
	if len(m.tagModules) != 2 {
		t.Errorf("tagModules len = %d, want 2", len(m.tagModules))
	}
	if _, ok := m.tagModules["modA"]; !ok {
		t.Error("expected modA in tagModules")
	}
	if _, ok := m.tagModules["modB"]; !ok {
		t.Error("expected modB in tagModules")
	}
}

// --- VerifyChecksum nil binary ---

func TestVerifyChecksum_NilBinary(t *testing.T) {
	m := &Manager{}
	err := m.VerifyChecksum(nil, "abc123")
	if err == nil {
		t.Fatal("expected error for nil binary")
	}
}

// --- scanModuleSource os/exec and syscall ---

func TestScanModuleSource_DetectsExec(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "exec.go"), []byte(`package main
import "os/exec"
var _ = exec.Command
`), 0600)

	_, err := scanModuleSource(dir, DefaultScanPolicy)
	if err == nil {
		t.Fatal("expected error for os/exec import")
	}
}

func TestScanModuleSource_DetectsSyscall(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "sys.go"), []byte(`package main
import "syscall"
var _ = syscall.SYS_WRITE
`), 0600)

	_, err := scanModuleSource(dir, DefaultScanPolicy)
	if err == nil {
		t.Fatal("expected error for syscall import")
	}
}

func TestScanModuleSource_ExecWarnOnly(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "exec.go"), []byte(`package main
import "os/exec"
var _ = exec.Command
`), 0600)

	policy := ScanPolicy{WarnExec: true}
	found, err := scanModuleSource(dir, policy)
	if err != nil {
		t.Fatalf("unexpected error with warn-only: %v", err)
	}
	if !slicesContains(found, "os/exec") {
		t.Errorf("expected 'os/exec' in found patterns, got %v", found)
	}
}

func TestScanModuleSource_SyscallWarnOnly(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "sys.go"), []byte(`package main
import "syscall"
var _ = syscall.SYS_WRITE
`), 0600)

	policy := ScanPolicy{WarnSyscall: true}
	found, err := scanModuleSource(dir, policy)
	if err != nil {
		t.Fatalf("unexpected error with warn-only: %v", err)
	}
	if !slicesContains(found, "syscall") {
		t.Errorf("expected 'syscall' in found patterns, got %v", found)
	}
}

// --- scanGoMod ---

func TestScanGoMod_DetectsReplace(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte(`module example.com/mod

go 1.21

replace github.com/foo/bar => ../local/bar
`), 0600)

	found := scanGoMod(dir)
	if !slicesContains(found, "go-mod-replace") {
		t.Errorf("expected 'go-mod-replace' in found, got %v", found)
	}
}

func TestScanGoMod_NoReplace(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte(`module example.com/mod

go 1.21
`), 0600)

	found := scanGoMod(dir)
	if slicesContains(found, "go-mod-replace") {
		t.Errorf("unexpected 'go-mod-replace' in found: %v", found)
	}
}

func TestScanGoMod_NoGoMod(t *testing.T) {
	dir := t.TempDir()
	found := scanGoMod(dir)
	if len(found) != 0 {
		t.Errorf("expected no findings without go.mod, got %v", found)
	}
}

// --- hasInitBlock ---

func TestHasInitBlock(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "init.go"), []byte(`package main
import "net/http"
func init() {
	_ = http.DefaultClient
}
`), 0600)

	policy := ScanPolicy{RejectNetworkInit: true}
	_, err := scanModuleSource(dir, policy)
	if err == nil {
		t.Fatal("expected error for network import in init()")
	}
}

func TestHasInitBlock_NoInit(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "noinit.go"), []byte(`package main
import "net/http"
func main() {
	_ = http.DefaultClient
}
`), 0600)

	policy := ScanPolicy{RejectNetworkInit: true}
	_, err := scanModuleSource(dir, policy)
	if err != nil {
		t.Fatalf("unexpected error without init(): %v", err)
	}
}

// --- isTextExt ---

func TestIsTextExt(t *testing.T) {
	textExts := []string{".go", ".md", ".txt", ".json", ".yaml", ".yml", ".toml",
		".xml", ".html", ".css", ".js", ".ts", ".proto",
		".mod", ".sum", ".sh", ".py", ".rb"}
	for _, ext := range textExts {
		if !isTextExt(ext) {
			t.Errorf("isTextExt(%q) = false, want true", ext)
		}
	}

	binaryExts := []string{".exe", ".bin", ".so", ".dll", ".dylib", ".png", ".jpg", ".gz"}
	for _, ext := range binaryExts {
		if isTextExt(ext) {
			t.Errorf("isTextExt(%q) = true, want false", ext)
		}
	}
}

// --- Spawn nil binary ---

func TestSpawn_NilBinary(t *testing.T) {
	m := NewManager("addr", nil, nil)
	err := m.Spawn(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil binary")
	}
}

// --- StopAll clears maps ---

func TestStopAll_ClearsMaps(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	m := NewManager("addr", nil, nil)
	m.SetCommandRunner(spawnMockRunner{})

	bin := &ModuleBinary{
		ID:            "stop-clear",
		Version:       "v1.0.0",
		Path:          "/fake/path",
		RestartPolicy: RestartNever,
	}
	m.Spawn(ctx, bin)

	time.Sleep(100 * time.Millisecond)

	stopCtx, stopCancel := context.WithTimeout(ctx, 2*time.Second)
	defer stopCancel()
	m.StopAll(stopCtx)

	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.processes) != 0 {
		t.Errorf("processes not cleared: %d entries", len(m.processes))
	}
	if len(m.pendingProcesses) != 0 {
		t.Errorf("pendingProcesses not cleared: %d entries", len(m.pendingProcesses))
	}
	if len(m.proxies) != 0 {
		t.Errorf("proxies not cleared: %d entries", len(m.proxies))
	}
	if len(m.binaries) != 0 {
		t.Errorf("binaries not cleared: %d entries", len(m.binaries))
	}
	if len(m.finishedProcesses) != 0 {
		t.Errorf("finishedProcesses not cleared: %d entries", len(m.finishedProcesses))
	}
	if len(m.spawnCancel) != 0 {
		t.Errorf("spawnCancel not cleared: %d entries", len(m.spawnCancel))
	}
}

// --- TrackProxy delivers cached exit error ---

func TestTrackProxy_DeliversCachedExitError(t *testing.T) {
	m := NewManager("addr", nil, nil)

	exitErr := fmt.Errorf("exit status 1")
	m.mu.Lock()
	m.finishedProcesses["cached-mod"] = exitErr
	m.mu.Unlock()

	proxy := NewSidecarProxy(contracts.ModuleInfo{ID: "cached-mod"})
	m.TrackProxy("cached-mod", proxy)

	m.mu.Lock()
	_, stillCached := m.finishedProcesses["cached-mod"]
	m.mu.Unlock()
	if stillCached {
		t.Error("expected finishedProcesses entry to be cleared after TrackProxy")
	}

	if err := proxy.Health(context.Background()); err == nil {
		t.Error("expected proxy to report cached exit error")
	}
}

// --- versionPattern ---

func TestVersionPattern(t *testing.T) {
	valid := []string{
		"v1.0.0",
		"1.0.0",
		"v0.1.0",
		"v1.2.3",
		"v1.0.0-rc1",
		"v1.0.0-beta.1",
		"v10.20.30",
	}
	for _, v := range valid {
		if !versionPattern.MatchString(v) {
			t.Errorf("versionPattern should match %q", v)
		}
	}

	invalid := []string{
		"",
		"latest",
		"master",
		"v1",
		"v1.0",
		"v1.0.0; rm -rf /",
		"$(whoami)",
		"v1.0.0-$(evil)",
		"1.0.0+build",
	}
	for _, v := range invalid {
		if versionPattern.MatchString(v) {
			t.Errorf("versionPattern should NOT match %q", v)
		}
	}
}

// --- NewManager defaults ---

func TestNewManager_Defaults(t *testing.T) {
	m := NewManager("127.0.0.1:9000", nil, nil)
	if m.meshAddr != "127.0.0.1:9000" {
		t.Errorf("meshAddr = %q, want %q", m.meshAddr, "127.0.0.1:9000")
	}
	if m.processes == nil {
		t.Error("processes map should be initialized")
	}
	if m.proxies == nil {
		t.Error("proxies map should be initialized")
	}
	if m.pendingProcesses == nil {
		t.Error("pendingProcesses map should be initialized")
	}
	if m.finishedProcesses == nil {
		t.Error("finishedProcesses map should be initialized")
	}
	if m.binaries == nil {
		t.Error("binaries map should be initialized")
	}
	if m.spawnCancel == nil {
		t.Error("spawnCancel map should be initialized")
	}
	if m.resolving == nil {
		t.Error("resolving map should be initialized")
	}
}

// --- scanModuleSource cgo source file ---

func TestScanModuleSource_DetectsCgoSourceFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "bridge.c"), []byte(`#include <stdio.h>
void hello() { printf("hello\n"); }
`), 0600)

	_, err := scanModuleSource(dir, DefaultScanPolicy)
	if err == nil {
		t.Fatal("expected error for cgo source file")
	}
}

// --- SetCertAuthority / Resolve validation / Resurrect / Restart ---

type stubCertIssuer struct{}

func (stubCertIssuer) IssueModuleCertForDir(moduleID string, dir string) (string, string, error) {
	return "", "", nil
}
func (stubCertIssuer) ValidateToken(token string) (string, error) { return "mod", nil }
func (stubCertIssuer) CACertPEM() []byte                          { return []byte("ca") }
func (stubCertIssuer) GenerateToken(moduleID string) (string, error) {
	return "tok", nil
}

func TestSetCertAuthority(t *testing.T) {
	m := NewManager("addr", nil, nil)
	m.SetCertAuthority(stubCertIssuer{})
	if m.certAuth == nil {
		t.Fatal("expected certAuth set")
	}
}

func TestResolve_InvalidVersion(t *testing.T) {
	m := NewManager("addr", nil, nil)
	_, err := m.Resolve("https://github.com/Muxcore-Media/x", "latest")
	if err == nil {
		t.Fatal("expected invalid version error")
	}
	if m.ResolveCount() != 1 {
		t.Errorf("ResolveCount = %d, want 1", m.ResolveCount())
	}
}

func TestResolve_DisallowedHost(t *testing.T) {
	m := NewManager("addr", nil, nil)
	m.SetAllowedRepoHosts([]string{"github.com"})
	_, err := m.Resolve("https://evil.example/repo", "v1.0.0")
	if err == nil {
		t.Fatal("expected disallowed host error")
	}
}

func TestResolve_CacheHit(t *testing.T) {
	m := NewManager("addr", nil, nil)
	cacheRoot := t.TempDir()
	m.cacheDir = cacheRoot
	moduleID := ModuleIDFromRepo("https://github.com/Muxcore-Media/cache-hit-mod")
	binPath := filepath.Join(cacheRoot, moduleID, "v1.2.3", "muxcore-module")
	if err := os.MkdirAll(filepath.Dir(binPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binPath, []byte("#!/bin/true\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	bin, err := m.Resolve("https://github.com/Muxcore-Media/cache-hit-mod", "v1.2.3")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if bin.Path != binPath {
		t.Fatalf("Path = %q, want %q", bin.Path, binPath)
	}
	if bin.ID != moduleID {
		t.Fatalf("ID = %q, want %q", bin.ID, moduleID)
	}
}

func TestResolveTagModule_InvalidVersion(t *testing.T) {
	m := NewManager("addr", nil, nil)
	_, err := m.ResolveTagModule(contracts.TagModule{
		Repo: "https://github.com/Muxcore-Media/x", Version: "not-a-semver",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestResurrectOrphan_NotInTag(t *testing.T) {
	m := NewManager("addr", nil, nil)
	err := m.ResurrectOrphan(context.Background(), "missing-mod")
	if err == nil {
		t.Fatal("expected not found error")
	}
}

func TestResurrectOrphan_AlreadyResolving(t *testing.T) {
	m := NewManager("addr", nil, nil)
	m.SetTag(&contracts.TagDefinition{
		Modules: []contracts.TagModule{
			{Repo: "https://github.com/Muxcore-Media/orphan-mod", Version: "v1.0.0"},
		},
	})
	id := ModuleIDFromRepo("https://github.com/Muxcore-Media/orphan-mod")
	m.resolving[id] = true
	err := m.ResurrectOrphan(context.Background(), id)
	if err == nil {
		t.Fatal("expected already resolving error")
	}
}

func TestResurrectOrphan_AlreadyRunningProcess(t *testing.T) {
	m := NewManager("addr", nil, nil)
	m.SetTag(&contracts.TagDefinition{
		Modules: []contracts.TagModule{
			{Repo: "https://github.com/Muxcore-Media/run-mod", Version: "v1.0.0"},
		},
	})
	id := ModuleIDFromRepo("https://github.com/Muxcore-Media/run-mod")
	m.processes[id] = exec.Command("true")
	if err := m.ResurrectOrphan(context.Background(), id); err != nil {
		t.Fatalf("expected nil for already running, got %v", err)
	}
}

func TestResurrectPendingOrphans_Empty(t *testing.T) {
	m := NewManager("addr", nil, nil)
	m.ResurrectPendingOrphans(context.Background())
}

func TestRestartModule_MissingBinary(t *testing.T) {
	m := NewManager("addr", nil, nil)
	err := m.RestartModule(context.Background(), "nope")
	if err == nil {
		t.Fatal("expected missing binary error")
	}
}

func TestRegisterModuleService_Registers(t *testing.T) {
	m := NewManager("addr", nil, nil)
	srv := grpc.NewServer()
	m.RegisterModuleService(srv)
	info := srv.GetServiceInfo()
	if len(info) == 0 {
		t.Fatal("expected at least one registered service")
	}
}

func TestScanNonGoFile_RejectCGO(t *testing.T) {
	var found []string
	skip, err := scanNonGoFile("/tmp/x.c", nil, "/tmp", DefaultScanPolicy, &found)
	if err == nil {
		t.Fatal("expected cgo reject error")
	}
	if !skip {
		t.Fatal("expected skip=true")
	}
}

func TestExecCommandRunner_CommandContext(t *testing.T) {
	var r execCommandRunner
	cmd := r.CommandContext(context.Background(), "echo", "hi")
	if cmd == nil || cmd.Path == "" && cmd.Args[0] != "echo" {
		// Path may be resolved; Args should start with echo
		if len(cmd.Args) == 0 || cmd.Args[0] != "echo" {
			t.Fatalf("unexpected cmd: %#v", cmd)
		}
	}
}

func testLifecycleMgr(t *testing.T) (*Manager, *registry.Registry, *modulemgr.Manager) {
	t.Helper()
	reg := registry.New()
	bus := events.NewMemoryBus()
	life := modulemgr.NewManager(reg, bus)
	m := NewManager("127.0.0.1:9090", reg, life)
	return m, reg, life
}

func TestRegistrationServer_RegisterAndUnregister(t *testing.T) {
	m, reg, _ := testLifecycleMgr(t)
	srv := &registrationServer{mgr: m}

	hookCalled := false
	m.PostRegisterHook = func(id string, caps []string) {
		hookCalled = true
		if id != "reg-mod" {
			t.Errorf("hook id=%s", id)
		}
	}

	resp, err := srv.Register(context.Background(), &modulev1.RegisterRequest{
		ModuleId: "reg-mod",
		ModuleInfo: &modulev1.ModuleInfo{
			Id:           "reg-mod",
			Name:         "Reg Mod",
			Version:      "1.2.3",
			Capabilities: []string{"test.cap"},
			Roles:        []string{"test"},
			HttpAddr:     ":1234",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Accepted {
		t.Fatalf("not accepted: %s", resp.Error)
	}
	if resp.MeshAddr != "127.0.0.1:9090" {
		t.Errorf("mesh addr %q", resp.MeshAddr)
	}
	if !hookCalled {
		t.Fatal("expected PostRegisterHook")
	}
	if _, err := reg.Get("reg-mod"); err != nil {
		t.Fatal("module not in registry")
	}
	m.mu.Lock()
	_, tracked := m.proxies["reg-mod"]
	m.mu.Unlock()
	if !tracked {
		t.Fatal("proxy not tracked")
	}

	uresp, err := srv.Unregister(context.Background(), &modulev1.UnregisterRequest{ModuleId: "reg-mod"})
	if err != nil {
		t.Fatal(err)
	}
	if !uresp.Acknowledged {
		t.Fatal("unregister not acknowledged")
	}
	if _, err := reg.Get("reg-mod"); err == nil {
		t.Fatal("module still in registry")
	}
}

func TestRegistrationServer_RegisterDefaultsAndMissingID(t *testing.T) {
	m, _, _ := testLifecycleMgr(t)
	srv := &registrationServer{mgr: m}

	resp, err := srv.Register(context.Background(), &modulev1.RegisterRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Accepted {
		t.Fatal("expected reject without module id")
	}

	resp, err = srv.Register(context.Background(), &modulev1.RegisterRequest{
		ModuleId: "bare-id",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Accepted {
		t.Fatalf("expected accept with ModuleId only: %s", resp.Error)
	}
}

func TestRegistrationServer_UnregisterMissing(t *testing.T) {
	m, _, _ := testLifecycleMgr(t)
	srv := &registrationServer{mgr: m}
	resp, err := srv.Unregister(context.Background(), &modulev1.UnregisterRequest{ModuleId: "nope"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Acknowledged {
		t.Fatal("expected not acknowledged for missing module")
	}
}

func TestRegistrationServer_BootstrapWithoutCA(t *testing.T) {
	m, _, _ := testLifecycleMgr(t)
	srv := &registrationServer{mgr: m}
	resp, err := srv.BootstrapRegister(context.Background(), &modulev1.BootstrapRegisterRequest{
		ModuleId: "m1",
		Token:    "tok",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Accepted {
		t.Fatal("expected reject without cert authority")
	}
	if resp.Error == "" {
		t.Fatal("expected error message")
	}
}

func TestRestartModule_RespawnsFromBinary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	m := NewManager("addr", nil, nil)
	m.SetCommandRunner(spawnMockRunner{})

	bin := &ModuleBinary{
		ID:            "restart-me",
		Version:       "v1.0.0",
		Path:          "/fake/path",
		RestartPolicy: RestartNever,
	}
	m.mu.Lock()
	m.binaries[bin.ID] = bin
	m.mu.Unlock()

	if err := m.RestartModule(ctx, bin.ID); err != nil {
		t.Fatalf("RestartModule: %v", err)
	}
	if got := m.SpawnCount(); got != 1 {
		t.Errorf("SpawnCount=%d want 1", got)
	}

	stopCtx, stopCancel := context.WithTimeout(ctx, 2*time.Second)
	defer stopCancel()
	m.StopAll(stopCtx)
}

func TestReconcileContracts_WithContracts(t *testing.T) {
	dir := t.TempDir()
	mux := filepath.Join(dir, "muxcore.json")
	content := `{
  "contracts": [
    {"repo": "github.com/Muxcore-Media/core/pkg/contracts", "interface": "Module", "version": "v0.4.0"}
  ]
}`
	if err := os.WriteFile(mux, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewManager("addr", nil, nil)
	// Should not panic; may warn if reconciler cannot resolve remotely.
	_ = m.reconcileContracts(dir)
}

// --- SpawnWithWatchdog / BootstrapRegister success paths ---

func TestSpawnWithWatchdog_NilBinary(t *testing.T) {
	m := NewManager("127.0.0.1:9000", nil, nil)
	if err := m.SpawnWithWatchdog(context.Background(), nil, nil); err == nil {
		t.Fatal("expected error for nil binary")
	}
}

func TestSpawnWithWatchdog_FallbackWithoutPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	m := NewManager("127.0.0.1:9000", nil, nil)
	m.SetCommandRunner(spawnMockRunner{})
	bin := &ModuleBinary{
		ID:            "wd-fallback",
		Version:       "v1.0.0",
		Path:          "/fake/path",
		RestartPolicy: RestartNever,
		Config:        map[string]string{"k": "v"},
	}
	if err := m.SpawnWithWatchdog(ctx, bin, []string{"127.0.0.1:9001"}); err != nil {
		t.Fatalf("SpawnWithWatchdog fallback: %v", err)
	}
	if got := m.SpawnCount(); got != 1 {
		t.Errorf("SpawnCount=%d want 1", got)
	}
	stopCtx, stopCancel := context.WithTimeout(ctx, 2*time.Second)
	defer stopCancel()
	m.StopAll(stopCtx)
}

func TestSpawnWithWatchdog_WithWatchdogBinary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	m := NewManager("127.0.0.1:9000", nil, nil)
	m.SetWatchdogPath("/fake/watchdog")
	m.SetCommandRunner(longRunningRunner{})
	bin := &ModuleBinary{
		ID:            "wd-spawn",
		Version:       "v1.0.0",
		Path:          "/fake/module",
		RestartPolicy: RestartNever,
	}
	if err := m.SpawnWithWatchdog(ctx, bin, []string{"127.0.0.1:9000", "127.0.0.1:9001"}); err != nil {
		t.Fatalf("SpawnWithWatchdog: %v", err)
	}
	if got := m.SpawnCount(); got != 1 {
		t.Errorf("SpawnCount=%d want 1", got)
	}
	if err := m.SpawnWithWatchdog(ctx, bin, nil); err == nil {
		t.Fatal("expected duplicate spawn error")
	}
	stopCtx, stopCancel := context.WithTimeout(ctx, 2*time.Second)
	defer stopCancel()
	m.StopAll(stopCtx)
}

type fileCertIssuer struct {
	moduleID string
	tokenErr error
	issueErr error
	skipKey  bool
}

func (f fileCertIssuer) ValidateToken(token string) (string, error) {
	if f.tokenErr != nil {
		return "", f.tokenErr
	}
	return f.moduleID, nil
}

func (f fileCertIssuer) IssueModuleCertForDir(moduleID string, dir string) (string, string, error) {
	if f.issueErr != nil {
		return "", "", f.issueErr
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	cert := filepath.Join(dir, moduleID+".crt")
	key := filepath.Join(dir, moduleID+".key")
	if err := os.WriteFile(cert, []byte("CERT"), 0o600); err != nil {
		return "", "", err
	}
	if f.skipKey {
		// Return a key path that does not exist (avoid leftover TempDir collisions).
		return cert, filepath.Join(dir, moduleID+".missing.key"), nil
	}
	if err := os.WriteFile(key, []byte("KEY"), 0o600); err != nil {
		return "", "", err
	}
	return cert, key, nil
}

func (f fileCertIssuer) CACertPEM() []byte { return []byte("CA") }
func (f fileCertIssuer) GenerateToken(moduleID string) (string, error) {
	return "tok", nil
}

func TestRegistrationServer_BootstrapSuccess(t *testing.T) {
	m, _, _ := testLifecycleMgr(t)
	m.SetCertAuthority(fileCertIssuer{moduleID: "m1"})
	srv := &registrationServer{mgr: m}
	resp, err := srv.BootstrapRegister(context.Background(), &modulev1.BootstrapRegisterRequest{
		ModuleId: "m1",
		Token:    "good",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Accepted {
		t.Fatalf("expected accept: %s", resp.Error)
	}
	if resp.SignedCert != "CERT" || resp.KeyPem != "KEY" || resp.CaCert != "CA" {
		t.Fatalf("unexpected PEM payloads: cert=%q key=%q ca=%q", resp.SignedCert, resp.KeyPem, resp.CaCert)
	}
}

func TestRegistrationServer_BootstrapInvalidToken(t *testing.T) {
	m, _, _ := testLifecycleMgr(t)
	m.SetCertAuthority(fileCertIssuer{moduleID: "m1", tokenErr: errors.New("expired")})
	srv := &registrationServer{mgr: m}
	resp, err := srv.BootstrapRegister(context.Background(), &modulev1.BootstrapRegisterRequest{
		ModuleId: "m1",
		Token:    "bad",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Accepted {
		t.Fatal("expected reject for invalid token")
	}
}

func TestRegistrationServer_BootstrapModuleMismatch(t *testing.T) {
	m, _, _ := testLifecycleMgr(t)
	m.SetCertAuthority(fileCertIssuer{moduleID: "other"})
	srv := &registrationServer{mgr: m}
	resp, err := srv.BootstrapRegister(context.Background(), &modulev1.BootstrapRegisterRequest{
		ModuleId: "m1",
		Token:    "tok",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Accepted {
		t.Fatal("expected reject for module/token mismatch")
	}
}

func TestRegistrationServer_BootstrapIssueFail(t *testing.T) {
	m, _, _ := testLifecycleMgr(t)
	m.SetCertAuthority(fileCertIssuer{moduleID: "m1", issueErr: errors.New("sign fail")})
	srv := &registrationServer{mgr: m}
	resp, err := srv.BootstrapRegister(context.Background(), &modulev1.BootstrapRegisterRequest{
		ModuleId: "m1",
		Token:    "tok",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Accepted {
		t.Fatal("expected reject when cert issue fails")
	}
}

func TestRegistrationServer_BootstrapMissingKeyFile(t *testing.T) {
	m, _, _ := testLifecycleMgr(t)
	m.SetCertAuthority(fileCertIssuer{moduleID: "m-nokey", skipKey: true})
	srv := &registrationServer{mgr: m}
	resp, err := srv.BootstrapRegister(context.Background(), &modulev1.BootstrapRegisterRequest{
		ModuleId: "m-nokey",
		Token:    "tok",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Accepted {
		t.Fatal("expected reject when key file missing")
	}
}

func TestSpawn_WithCertAuthority(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	m := NewManager("127.0.0.1:9000", nil, nil)
	m.SetCertAuthority(fileCertIssuer{moduleID: "tls-mod"})
	m.SetCommandRunner(spawnMockRunner{})
	bin := &ModuleBinary{
		ID:            "tls-mod",
		Version:       "v1.0.0",
		Path:          "/fake/path",
		RestartPolicy: RestartNever,
	}
	if err := m.Spawn(ctx, bin); err != nil {
		t.Fatalf("Spawn with CA: %v", err)
	}
	stopCtx, stopCancel := context.WithTimeout(ctx, 2*time.Second)
	defer stopCancel()
	m.StopAll(stopCtx)
}

type stubLifecycleModule struct {
	info contracts.ModuleInfo
}

func (m *stubLifecycleModule) Info() contracts.ModuleInfo     { return m.info }
func (m *stubLifecycleModule) Init(_ context.Context) error   { return nil }
func (m *stubLifecycleModule) Start(_ context.Context) error  { return nil }
func (m *stubLifecycleModule) Stop(_ context.Context) error   { return nil }
func (m *stubLifecycleModule) Health(_ context.Context) error { return nil }

func TestResolve_InvalidURL(t *testing.T) {
	m := NewManager("addr", nil, nil)
	_, err := m.Resolve("://bad", "v1.0.0")
	if err == nil {
		t.Fatal("expected invalid URL error")
	}
}

func TestResolveTagModule_InstanceCacheHit(t *testing.T) {
	m := NewManager("addr", nil, nil)
	cacheRoot := t.TempDir()
	m.cacheDir = cacheRoot
	repo := "https://github.com/Muxcore-Media/inst-cache"
	id := ModuleIDFromRepoWithInstance(repo, "east")
	binPath := filepath.Join(cacheRoot, id, "v1.0.0", "muxcore-module")
	if err := os.MkdirAll(filepath.Dir(binPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binPath, []byte("#!/bin/true\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	bin, err := m.ResolveTagModule(contracts.TagModule{
		Repo: repo, Version: "v1.0.0", InstanceID: "east",
		Config: map[string]string{"k": "v"},
	})
	if err != nil {
		t.Fatalf("ResolveTagModule: %v", err)
	}
	if bin.ID != id || bin.InstanceID != "east" {
		t.Fatalf("got id=%q instance=%q", bin.ID, bin.InstanceID)
	}
	if bin.Path != binPath {
		t.Fatalf("path=%q want %q", bin.Path, binPath)
	}
}

func TestResurrectOrphan_RegisteredRunning(t *testing.T) {
	reg := registry.New()
	id := ModuleIDFromRepo("https://github.com/Muxcore-Media/sidecar-run")
	if err := reg.Register(&stubLifecycleModule{
		info: contracts.ModuleInfo{ID: id, Name: "Sidecar", Version: "1.0.0"},
	}, nil); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetState(id, contracts.ModuleStateStarting); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetState(id, contracts.ModuleStateRunning); err != nil {
		t.Fatal(err)
	}
	m := NewManager("addr", reg, nil)
	m.SetTag(&contracts.TagDefinition{
		Modules: []contracts.TagModule{
			{Repo: "https://github.com/Muxcore-Media/sidecar-run", Version: "v1.0.0"},
		},
	})
	if err := m.ResurrectOrphan(context.Background(), id); err != nil {
		t.Fatalf("expected nil for registered running sidecar, got %v", err)
	}
}

func TestResurrectOrphan_ChecksumFail(t *testing.T) {
	m := NewManager("addr", registry.New(), nil)
	cacheRoot := t.TempDir()
	m.cacheDir = cacheRoot
	repo := "https://github.com/Muxcore-Media/orphan-badsum"
	id := ModuleIDFromRepo(repo)
	binPath := filepath.Join(cacheRoot, id, "v1.0.0", "muxcore-module")
	if err := os.MkdirAll(filepath.Dir(binPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binPath, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	m.SetTag(&contracts.TagDefinition{
		Modules: []contracts.TagModule{
			{Repo: repo, Version: "v1.0.0", Checksum: "0000000000000000000000000000000000000000000000000000000000000000"},
		},
	})
	err := m.ResurrectOrphan(context.Background(), id)
	if err == nil {
		t.Fatal("expected checksum failure")
	}
}

func TestResurrectPendingOrphans_SkipsRunningProcess(t *testing.T) {
	m := NewManager("addr", nil, nil)
	repo := "https://github.com/Muxcore-Media/pending-run"
	id := ModuleIDFromRepo(repo)
	m.SetTag(&contracts.TagDefinition{
		Modules: []contracts.TagModule{{Repo: repo, Version: "v1.0.0"}},
	})
	m.processes[id] = exec.Command("true")
	m.ResurrectPendingOrphans(context.Background())
}

func TestResurrectPendingOrphans_SkipsRegisteredRunning(t *testing.T) {
	reg := registry.New()
	repo := "https://github.com/Muxcore-Media/pending-reg"
	id := ModuleIDFromRepo(repo)
	if err := reg.Register(&stubLifecycleModule{
		info: contracts.ModuleInfo{ID: id, Name: "Reg", Version: "1.0.0"},
	}, nil); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetState(id, contracts.ModuleStateStarting); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetState(id, contracts.ModuleStateRunning); err != nil {
		t.Fatal(err)
	}
	m := NewManager("addr", reg, nil)
	m.SetTag(&contracts.TagDefinition{
		Modules: []contracts.TagModule{{Repo: repo, Version: "v1.0.0"}},
	})
	m.ResurrectPendingOrphans(context.Background())
}
