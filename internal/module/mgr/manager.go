package mgr

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	modulemgr "github.com/Muxcore-Media/core/internal/module"
	modulev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/module/v1"
	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/pkg/contracts"
	"github.com/Muxcore-Media/contracts-reconciler/reconciler"
	"google.golang.org/grpc"
)

// RestartPolicy controls how the manager handles unexpected module exits.
type RestartPolicy string

const (
	// RestartNever does not restart the module on exit. Default.
	RestartNever RestartPolicy = "never"
	// RestartOnFailure restarts the module only on non-zero exit codes.
	RestartOnFailure RestartPolicy = "on-failure"
	// RestartAlways restarts the module on any exit (including clean exit 0).
	RestartAlways RestartPolicy = "always"
)

// maxRestartAttempts is the maximum number of consecutive restarts before
// the manager gives up and logs a permanent failure.
const maxRestartAttempts = 5

// ModuleBinary is a resolved module binary ready to run.
type ModuleBinary struct {
	ID            string
	Version       string
	Path          string
	Repo          string
	RestartPolicy RestartPolicy
}

// Manager spawns and tracks sidecar module processes.
type Manager struct {
	mu        sync.Mutex
	processes map[string]*exec.Cmd
	proxies   map[string]*SidecarProxy // module ID → proxy for health tracking
	meshAddr  string
	cacheDir  string
	reg       *registry.Registry
	modMgr    *modulemgr.Manager
}

// NewManager creates a module manager.
// meshAddr is the gRPC address modules should connect to.
// reg is the module registry for discovery.
// modMgr is the lifecycle manager for events/audit.
func NewManager(meshAddr string, reg *registry.Registry, modMgr *modulemgr.Manager) *Manager {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "/tmp"
	}
	return &Manager{
		processes: make(map[string]*exec.Cmd),
		proxies:   make(map[string]*SidecarProxy),
		meshAddr:  meshAddr,
		cacheDir:  filepath.Join(home, ".muxcore", "modules"),
		reg:       reg,
		modMgr:    modMgr,
	}
}

// Resolve locates a module binary for the given repo and version.
// Resolution order: cache → build from source.
func (m *Manager) Resolve(repoURL, version string) (*ModuleBinary, error) {
	moduleID := moduleIDFromRepo(repoURL)

	cachedPath := filepath.Join(m.cacheDir, moduleID, version, "muxcore-module")
	if _, err := os.Stat(cachedPath); err == nil {
		slog.Info("module found in cache", "id", moduleID, "version", version)
		return &ModuleBinary{ID: moduleID, Version: version, Path: cachedPath, Repo: repoURL}, nil
	}

	slog.Info("module not in cache, building from source", "id", moduleID, "repo", repoURL)

	buildDir := filepath.Join(os.TempDir(), "muxcore-build", moduleID)
	if err := os.RemoveAll(buildDir); err != nil {
		return nil, fmt.Errorf("clean build dir: %w", err)
	}

	cloneCmd := exec.Command("git", "clone", "--depth", "1", "--branch", version, repoURL, buildDir)
	if out, err := cloneCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("clone %s@%s: %w\n%s", repoURL, version, err, out)
	}

	// Run contract reconciliation if the module declares non-canonical contracts.
	// Reads muxcore.json for a "contracts" field, checks structural compatibility
	// against canonical Muxcore-Media contract repos, and applies go.mod replace
	// directives to normalize imports. No-op for modules using canonical contracts.
	if err := m.reconcileContracts(buildDir); err != nil {
		return nil, fmt.Errorf("contract reconciliation for %s: %w", moduleID, err)
	}

	// Pre-build source scan: detect dangerous patterns before compilation.
	// The go build step can execute go:generate directives, init() functions,
	// and cgo code. We scan for these patterns first so operators can audit
	// modules that use them. Modules with unsafe or cgo are rejected outright.
	scanResults, err := scanModuleSource(buildDir)
	if err != nil {
		return nil, fmt.Errorf("source scan for %s: %w", moduleID, err)
	}
	if len(scanResults) > 0 {
		slog.Warn("module source scan found patterns requiring audit",
			"module", moduleID, "patterns", scanResults,
		)
	}

	binPath := filepath.Join(buildDir, "muxcore-module")
	buildCmd := exec.Command("go", "build", "-o", binPath, "./cmd/module/")
	buildCmd.Dir = buildDir
	if out, err := buildCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("build %s: %w\n%s", moduleID, err, out)
	}

	cacheBinDir := filepath.Join(m.cacheDir, moduleID, version)
	if err := os.MkdirAll(cacheBinDir, 0755); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	cacheBinPath := filepath.Join(cacheBinDir, "muxcore-module")
	if err := os.Rename(binPath, cacheBinPath); err != nil {
		data, _ := os.ReadFile(binPath)
		if err := os.WriteFile(cacheBinPath, data, 0755); err != nil {
			slog.Warn("failed to cache module binary", "id", moduleID, "error", err)
		}
	}

	slog.Info("module built and cached", "id", moduleID, "version", version)
	return &ModuleBinary{ID: moduleID, Version: version, Path: cacheBinPath, Repo: repoURL}, nil
}

// Spawn starts a module binary as a child process.
func (m *Manager) Spawn(ctx context.Context, bin *ModuleBinary) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.processes[bin.ID]; exists {
		return fmt.Errorf("module %s already running", bin.ID)
	}

	cmd := exec.CommandContext(ctx, bin.Path,
		"--muxcore-mesh-addr", m.meshAddr,
		"--muxcore-module-id", bin.ID,
	)
	// Prefix module output so it's distinguishable from core's own log lines.
	// In JSON log mode (MUXCORE_LOG_FORMAT=json) unprefixed text output from
	// modules corrupts the JSON stream — this prefix makes filtering possible.
	cmd.Stdout = newPrefixedWriter(os.Stdout, "[module:"+bin.ID+"] ")
	cmd.Stderr = newPrefixedWriter(os.Stderr, "[module:"+bin.ID+":err] ")

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn %s: %w", bin.ID, err)
	}

	m.processes[bin.ID] = cmd
	slog.Info("module spawned", "id", bin.ID, "pid", cmd.Process.Pid)

	// Attach process to proxy for health tracking.
	if proxy, ok := m.proxies[bin.ID]; ok {
		proxy.TrackProcess(cmd)
	}

	go m.watchProcess(ctx, cmd, bin)

	return nil
}

// watchProcess monitors a sidecar process and applies the restart policy on exit.
func (m *Manager) watchProcess(ctx context.Context, cmd *exec.Cmd, bin *ModuleBinary) {
	for attempt := 0; ; attempt++ {
		err := cmd.Wait()

		m.mu.Lock()
		delete(m.processes, bin.ID)
		m.mu.Unlock()

		if ctx.Err() != nil {
			// Context cancelled — shutdown in progress, don't restart.
			return
		}

		cleanExit := err == nil

		// Decide whether to restart.
		shouldRestart := false
		switch bin.RestartPolicy {
		case RestartAlways:
			shouldRestart = attempt < maxRestartAttempts
		case RestartOnFailure:
			shouldRestart = !cleanExit && attempt < maxRestartAttempts
		default: // RestartNever
			shouldRestart = false
		}

		if !cleanExit {
			slog.Error("module exited unexpectedly",
				"id", bin.ID,
				"error", err,
				"attempt", attempt,
				"restart", shouldRestart,
			)
		}

		if !shouldRestart {
			return
		}

		// Exponential back-off: 1s, 2s, 4s, 8s, 16s, capped at 30s.
		backoff := time.Duration(1<<attempt) * time.Second
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
		slog.Info("restarting module", "id", bin.ID, "backoff", backoff, "attempt", attempt+1)

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		// Spawn a new process for the same binary.
		newCmd := exec.CommandContext(ctx, bin.Path,
			"--muxcore-mesh-addr", m.meshAddr,
			"--muxcore-module-id", bin.ID,
		)
		newCmd.Stdout = newPrefixedWriter(os.Stdout, "[module:"+bin.ID+"] ")
		newCmd.Stderr = newPrefixedWriter(os.Stderr, "[module:"+bin.ID+":err] ")

		if startErr := newCmd.Start(); startErr != nil {
			slog.Error("module restart failed", "id", bin.ID, "error", startErr)
			return
		}

		m.mu.Lock()
		m.processes[bin.ID] = newCmd
		m.mu.Unlock()
		slog.Info("module restarted", "id", bin.ID, "pid", newCmd.Process.Pid)

		cmd = newCmd
	}
}

// TrackProxy registers a SidecarProxy for health tracking.
// Called during gRPC registration so Spawn can attach the process.
func (m *Manager) TrackProxy(moduleID string, proxy *SidecarProxy) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.proxies[moduleID] = proxy
}

// VerifyChecksum reads the binary at bin.Path, computes its SHA256 digest,
// and compares it against the expected hex string. Returns nil if they match
// or if expected is empty (backward compatible). Returns an error on mismatch
// or if the file cannot be read.
func (m *Manager) VerifyChecksum(bin *ModuleBinary, expected string) error {
	if expected == "" {
		return nil // backward compatible: no checksum declared
	}
	data, err := os.ReadFile(bin.Path)
	if err != nil {
		return fmt.Errorf("verify checksum: read binary %s: %w", bin.Path, err)
	}
	actual := fmt.Sprintf("%x", sha256.Sum256(data))
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", bin.ID, expected, actual)
	}
	slog.Info("module checksum verified", "id", bin.ID, "checksum", expected[:16]+"...")
	return nil
}

// PruneCache removes old cached module binaries, keeping only the most recent
// keepVersions versions per module ID. Versions are sorted lexicographically
// (semver tags produced by `git describe` sort correctly this way). A
// keepVersions of 0 removes all cached versions. Call on startup to prevent
// unbounded cache growth.
func (m *Manager) PruneCache(keepVersions int) error {
	entries, err := os.ReadDir(m.cacheDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // cache doesn't exist yet
		}
		return fmt.Errorf("prune cache: read %s: %w", m.cacheDir, err)
	}

	var pruned, kept int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		moduleDir := filepath.Join(m.cacheDir, e.Name())
		versions, err := os.ReadDir(moduleDir)
		if err != nil {
			continue
		}

		// Sort versions: lexicographic order is sufficient for semver tags.
		versionNames := make([]string, 0, len(versions))
		for _, v := range versions {
			if v.IsDir() {
				versionNames = append(versionNames, v.Name())
			}
		}
		sort.Strings(versionNames)

		// Keep only the last keepVersions; remove the rest.
		remove := versionNames
		if keepVersions > 0 && len(versionNames) > keepVersions {
			remove = versionNames[:len(versionNames)-keepVersions]
		} else if keepVersions > 0 {
			remove = nil
		}

		for _, v := range remove {
			path := filepath.Join(moduleDir, v)
			if err := os.RemoveAll(path); err != nil {
				slog.Warn("prune cache: remove failed", "path", path, "error", err)
				continue
			}
			pruned++
			slog.Debug("pruned cached module version", "module", e.Name(), "version", v)
		}
		kept += len(versionNames) - len(remove)
	}

	if pruned > 0 {
		slog.Info("module cache pruned", "removed", pruned, "retained", kept, "keep_per_module", keepVersions)
	}
	return nil
}

// StopAll sends SIGTERM to all running modules.
func (m *Manager) StopAll(ctx context.Context) error {
	m.mu.Lock()
	cmds := make([]*exec.Cmd, 0, len(m.processes))
	for id, cmd := range m.processes {
		cmds = append(cmds, cmd)
		slog.Info("stopping module", "id", id)
	}
	m.processes = make(map[string]*exec.Cmd)
	m.mu.Unlock()

	for _, cmd := range cmds {
		if cmd.Process != nil {
			cmd.Process.Signal(os.Interrupt)
		}
	}

	done := make(chan struct{})
	go func() {
		for _, cmd := range cmds {
			cmd.Wait()
		}
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		for _, cmd := range cmds {
			if cmd.Process != nil {
				cmd.Process.Kill()
			}
		}
		return ctx.Err()
	}
}

// RegisterModuleService sets up the gRPC registration service.
func (m *Manager) RegisterModuleService(srv *grpc.Server) {
	modulev1.RegisterModuleRegistrationServer(srv, &registrationServer{mgr: m})
}

type registrationServer struct {
	modulev1.UnimplementedModuleRegistrationServer
	mgr *Manager
}

func (s *registrationServer) Register(ctx context.Context, req *modulev1.RegisterRequest) (*modulev1.RegisterResponse, error) {
	// Parse the module info from the registration payload.
	var info contracts.ModuleInfo
	if len(req.InfoJson) > 0 {
		if err := json.Unmarshal(req.InfoJson, &info); err != nil {
			slog.Error("module registration: invalid info_json", "id", req.ModuleId, "error", err)
			return &modulev1.RegisterResponse{MeshAddr: s.mgr.meshAddr, Accepted: false, Error: fmt.Sprintf("invalid info_json: %v", err)}, nil
		}
	}

	if info.ID == "" {
		info.ID = req.ModuleId
	}
	if info.ID == "" {
		return &modulev1.RegisterResponse{MeshAddr: s.mgr.meshAddr, Accepted: false, Error: "module ID is required"}, nil
	}

	// Create a proxy that satisfies contracts.Module for registry registration.
	proxy := NewSidecarProxy(info)
	s.mgr.TrackProxy(info.ID, proxy)

	// Register with the module lifecycle manager (adds to registry, publishes events).
	deps := info.DependsOn
	if err := s.mgr.modMgr.Register(proxy, deps); err != nil {
		slog.Error("module registration: registry", "id", info.ID, "error", err)
		return &modulev1.RegisterResponse{MeshAddr: s.mgr.meshAddr, Accepted: false, Error: err.Error()}, nil
	}

	slog.Info("module registered via gRPC and added to registry",
		"id", info.ID,
		"name", info.Name,
		"version", info.Version,
		"roles", info.Roles,
		"capabilities", info.Capabilities,
		"deps", deps,
	)
	return &modulev1.RegisterResponse{MeshAddr: s.mgr.meshAddr, Accepted: true}, nil
}

func (s *registrationServer) Unregister(ctx context.Context, req *modulev1.UnregisterRequest) (*modulev1.UnregisterResponse, error) {
	if err := s.mgr.modMgr.Unregister(req.ModuleId); err != nil {
		slog.Warn("module unregistration failed", "id", req.ModuleId, "error", err)
		return &modulev1.UnregisterResponse{Acknowledged: false}, nil
	}
	slog.Info("module unregistered", "id", req.ModuleId)
	return &modulev1.UnregisterResponse{Acknowledged: true}, nil
}

// scanModuleSource scans a cloned module directory for patterns that can
// execute arbitrary code during the build step. Returns a list of found
// pattern names. Modules using "unsafe" or "cgo" are rejected with an error.
//
// Detected patterns (warning):
//   - go:generate — directives execute arbitrary commands during go build
//
// Rejected patterns (error):
//   - "unsafe" — pointer arithmetic, type punning, memory safety violations
//   - import "C" — cgo enables arbitrary C code execution at build time
func scanModuleSource(buildDir string) ([]string, error) {
	var found []string

	walkErr := filepath.WalkDir(buildDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable files
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		// Parse the AST so import checks are not confused by comments or
		// string literals that mention "unsafe" or "C" without importing them.
		fset := token.NewFileSet()
		f, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if parseErr != nil {
			return nil // skip files that don't parse (generated stubs, etc.)
		}

		// Reject imports of "unsafe" or cgo ("C") — both enable memory-unsafe
		// or arbitrary-C-code execution and are prohibited in sidecar modules.
		for _, imp := range f.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			switch importPath {
			case "unsafe":
				return fmt.Errorf("module imports unsafe package: %s", filepath.Base(path))
			case "C":
				return fmt.Errorf("module imports cgo (\"C\"): %s", filepath.Base(path))
			}
		}

		// Detect (warn): //go:generate directives execute arbitrary commands
		// during go build and should be reviewed by the operator.
		for _, cg := range f.Comments {
			for _, c := range cg.List {
				if strings.HasPrefix(c.Text, "//go:generate") {
					if !slicesContains(found, "go:generate") {
						found = append(found, "go:generate")
					}
				}
			}
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return found, nil
}

func slicesContains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func moduleIDFromRepo(repoURL string) string {
	u, err := url.Parse(strings.TrimSuffix(repoURL, ".git"))
	if err != nil || u.Path == "" {
		return ""
	}
	// filepath.Base strips all directory components, preventing path traversal
	// via crafted repo URLs (e.g. "https://host/owner/../escape").
	return filepath.Base(u.Path)
}


// reconcileContracts checks the module's muxcore.json for non-canonical contract
// declarations and runs structural reconciliation against the canonical registry.
// If interfaces match, applies go.mod replace directives to normalize imports.
func (m *Manager) reconcileContracts(buildDir string) error {
	muxcorePath := filepath.Join(buildDir, "muxcore.json")
	data, err := os.ReadFile(muxcorePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no muxcore.json — nothing to reconcile
		}
		return fmt.Errorf("read muxcore.json: %w", err)
	}

	var meta struct {
		Contracts []struct {
			Repo      string `json:"repo"`
			Version   string `json:"version"`
			Interface string `json:"interface"`
		} `json:"contracts"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return fmt.Errorf("parse muxcore.json: %w", err)
	}

	if len(meta.Contracts) == 0 {
		return nil // no contract declarations — nothing to reconcile
	}

	r := &reconciler.Resolver{}
	var decls []reconciler.Declaration
	for _, c := range meta.Contracts {
		decls = append(decls, reconciler.Declaration{
			Repo:      c.Repo,
			Version:   c.Version,
			Interface: c.Interface,
		})
	}

	directives, errs := r.ResolveAll(decls)
	for _, e := range errs {
		slog.Warn("contract reconciliation warning", "error", e)
	}

	if len(directives) == 0 {
		return nil
	}

	slog.Info("applying contract reconciliation", "module", filepath.Base(buildDir), "directives", len(directives))
	if err := reconciler.ApplyReplaceDirectives(buildDir, directives); err != nil {
		return fmt.Errorf("apply replace directives: %w", err)
	}

	return nil
}