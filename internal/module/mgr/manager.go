//nolint:govet // struct field alignment
package mgr

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Muxcore-Media/contracts-reconciler/reconciler"
	"github.com/Muxcore-Media/core/internal/enroll"
	modulemgr "github.com/Muxcore-Media/core/internal/module"
	"github.com/Muxcore-Media/core/internal/peerid"
	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/internal/sandbox"
	"github.com/Muxcore-Media/core/internal/spool"
	"github.com/Muxcore-Media/core/pkg/contracts"
	modulev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/module/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

// ScanPolicy controls which dangerous patterns are rejected versus warned
// during pre-build source scanning. All rejection flags default to true
// (secure by default). Set a flag to false to allow the pattern.
type ScanPolicy struct {
	// RejectUnsafe rejects modules that import "unsafe". Default true.
	RejectUnsafe bool
	// RejectCGO rejects modules that import "C" (cgo). Default true.
	RejectCGO bool
	// RejectGoGenerate rejects modules with //go:generate directives. Default true.
	RejectGoGenerate bool
	// RejectExec rejects modules that import "os/exec". Default true.
	RejectExec bool
	// RejectSyscall rejects modules that import "syscall". Default true.
	RejectSyscall bool
	// RejectNetworkInit rejects modules that import networking packages in init(). Default true.
	RejectNetworkInit bool
	// WarnExec warns when modules import "os/exec". Default false (RejectExec takes precedence).
	WarnExec bool
	// WarnSyscall warns when modules import "syscall". Default false (RejectSyscall takes precedence).
	WarnSyscall bool
	// WarnNetworkInit warns when modules import networking packages in init(). Default false (RejectNetworkInit takes precedence).
	WarnNetworkInit bool
}

// DefaultScanPolicy is the strict default: reject unsafe, cgo, go:generate,
// os/exec, syscall, and network-in-init. All reject flags are true (secure by default).
// Warn flags default to false since the corresponding Reject flags handle it.
var DefaultScanPolicy = ScanPolicy{
	RejectUnsafe:      true,
	RejectCGO:         true,
	RejectGoGenerate:  true,
	RejectExec:        true,
	RejectSyscall:     true,
	RejectNetworkInit: true,
}

// CommandRunner abstracts os/exec for testability.
type CommandRunner interface {
	CommandContext(ctx context.Context, name string, arg ...string) *exec.Cmd
}

type execCommandRunner struct{}

func (execCommandRunner) CommandContext(ctx context.Context, name string, arg ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, arg...) //nolint:gosec // arg is test-provided or from config
}

// versionPattern validates module version strings for git branch/tag safety.
// Only allows semver-like strings: optional 'v' prefix, digits, dots, hyphens.
// Rejects shell metacharacters and git-smart-protocol injection vectors.
var versionPattern = regexp.MustCompile(`^v?\d+\.\d+\.\d+(-[a-zA-Z0-9.]+)?$`)

// ModuleBinary is a resolved module binary ready to run.
type ModuleBinary struct {
	ID            string
	Version       string
	Path          string
	Repo          string
	RestartPolicy RestartPolicy
	InstanceID    string
	// Config is instance-specific configuration passed as environment
	// variables (prefixed with MUXCORE_CFG_) to the module binary.
	Config map[string]string
}

// Manager spawns and tracks sidecar module processes.
type Manager struct {
	mu        sync.Mutex
	processes map[string]*exec.Cmd
	proxies   map[string]*SidecarProxy // module ID → proxy for health tracking
	// pendingProcesses holds processes whose SidecarProxy hasn't registered yet
	// (via gRPC TrackProxy). Once TrackProxy is called, the proxy is attached.
	pendingProcesses map[string]*exec.Cmd
	// finishedProcesses caches exit errors for processes that exited before
	// their proxy registered. Cleared when TrackProxy delivers them.
	finishedProcesses map[string]error
	// exited maps each started process to a channel that is closed once the
	// process has been reaped. exec.Cmd.Wait must be called exactly once, by
	// the goroutine that owns the process (watchProcess or the watchdog
	// waiter). StopAll and RestartModule wait on this channel instead of
	// calling Wait themselves. Never reset by StopAll: owners still need to
	// signal their waiters after shutdown clears the other maps.
	exited map[*exec.Cmd]chan struct{}
	// resolving tracks module IDs that are currently being resolved (git clone +
	// go build). Prevents duplicate concurrent resolve attempts for the same module.
	resolving map[string]bool
	meshAddr  string
	cacheDir  string
	reg       *registry.Registry
	modMgr    *modulemgr.Manager
	// ScanPolicy controls which dangerous patterns are rejected versus warned
	// during pre-build source scanning. Default is strict (secure by default).
	ScanPolicy ScanPolicy
	// allowedRepoHosts restricts git clone to specific hosts. Empty means all hosts allowed.
	// Set from SpoolConfig.AllowedHosts at bootstrap.
	allowedRepoHosts []string
	spawnCount       atomic.Int64
	restartCount     atomic.Int64
	resolveCount     atomic.Int64
	cmdRunner        CommandRunner
	// sandboxRunner optionally wraps module spawn (gVisor/Firecracker). Default no-op.
	sandboxRunner sandbox.Runner
	// watchdogPath is the path to the muxcore-watchdog binary.
	// When set, SpawnWithWatchdog launches this binary instead of the module
	// directly, enabling automatic core failover for sidecar modules.
	watchdogPath string
	// tagModules maps module ID to its spool tag entry for resurrection.
	// Populated by SetTag during bootstrap. Used by ResurrectOrphan to
	// re-spawn modules that were running on a departed cluster node.
	tagModules map[string]contracts.TagModule
	// binaries maps module ID to its ModuleBinary for restart capabilities.
	// Populated by Spawn/SpawnWithWatchdog. Used by RestartModule to
	// re-spawn a process that is alive but unhealthy.
	binaries map[string]*ModuleBinary
	// spawnCancel holds per-module cancel funcs for the context passed to
	// watchProcess. RestartModule cancels the old context before killing the
	// process, ensuring watchProcess does not attempt a competing restart.
	spawnCancel map[string]context.CancelFunc
	// PostRegisterHook is called after every successful sidecar module
	// registration, allowing core to re-discover and wire policy/auth
	// providers that may have registered after the initial bootstrap window.
	PostRegisterHook func(moduleID string, caps []string)
	// certAuth is the internal certificate authority for issuing module
	// certificates. When set, spawned modules get signed client certs
	// and the BootstrapRegister RPC is available for external modules.
	certAuth CertIssuer
	// requireMarketplaceSigs makes a valid signature mandatory for
	// marketplace DeployTag and orphan resurrection (household profile,
	// FR-EXT-003). Boot-time curated tags are not affected.
	requireMarketplaceSigs atomic.Bool
	// regPolicy holds the ADR-0018 registration rules (nil = dev).
	regPolicy atomic.Pointer[RegistrationPolicy]
}

// SecurityCapabilities are the capabilities whose provider core wires into
// its own security path. Registering one requires a verified module
// certificate in household, and each may have only one provider
// (ADR-0018, NFR-SEC-002).
var SecurityCapabilities = []string{
	contracts.CapabilityCallPolicy,
	contracts.CapabilityPublishPolicy,
	contracts.CapabilityAuthorizer,
	contracts.CapabilityIdentity,
	contracts.CapabilityAuth,
}

// RegistrationPolicy selects the ADR-0018 registration rules applied by the
// ModuleRegistration service.
//
//	Rule                                         dev                    household
//	cert CN != registering module ID             reject                 reject
//	no cert, security capability                 warn                   reject (PermissionDenied)
//	no cert, other capabilities                  allow                  warn; reject if RequireModuleCerts
//	2nd provider (other ID) of a security cap    warn, first kept       reject (FailedPrecondition)
//	same verified ID re-registers                atomic replace         atomic replace
//	Unregister                                   open                   requires cert CN == ID
type RegistrationPolicy struct {
	// Household applies the household column; false applies dev.
	Household bool
	// RequireModuleCerts is ADR-0018 stage 2 (MUXCORE_REQUIRE_MODULE_CERTS):
	// in household, reject every registration without a verified
	// certificate. Ignored in dev.
	RequireModuleCerts bool
}

// SetRegistrationPolicy sets the ADR-0018 registration rules. Until it is
// called the dev rules apply; muxcored always sets it from the profile.
func (m *Manager) SetRegistrationPolicy(p RegistrationPolicy) {
	m.regPolicy.Store(&p)
}

// RegistrationPolicy returns the active registration rules.
func (m *Manager) RegistrationPolicy() RegistrationPolicy {
	if p := m.regPolicy.Load(); p != nil {
		return *p
	}
	return RegistrationPolicy{}
}

// CertIssuer is the interface for issuing module certificates and
// validating bootstrap tokens. Implemented by CertAuthority.
type CertIssuer interface {
	IssueModuleCertForDir(moduleID string, dir string) (certPath, keyPath string, err error)
	ValidateToken(token string) (moduleID string, err error)
	CACertPEM() []byte
	GenerateToken(moduleID string) (token string, err error)
}

// SetCertAuthority sets the certificate authority for issuing module certs.
func (m *Manager) SetCertAuthority(ca CertIssuer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.certAuth = ca
}

// SetRequireMarketplaceSignatures makes VerifyMarketplaceSignature (and
// orphan resurrection) require a valid signature regardless of
// MUXCORE_SPOOL_REQUIRE_SIGNATURE. Set from the household profile.
func (m *Manager) SetRequireMarketplaceSignatures(require bool) {
	m.requireMarketplaceSigs.Store(require)
}

// RequireMarketplaceSignatures reports whether marketplace signatures are
// mandatory (see SetRequireMarketplaceSignatures).
func (m *Manager) RequireMarketplaceSignatures() bool {
	return m.requireMarketplaceSigs.Load()
}

func (m *Manager) SpawnCount() int64   { return m.spawnCount.Load() }
func (m *Manager) RestartCount() int64 { return m.restartCount.Load() }
func (m *Manager) ResolveCount() int64 { return m.resolveCount.Load() }

// trackExitLocked registers cmd as a started process whose owner goroutine
// will call Wait. Must be called with m.mu held, after cmd.Start succeeded
// and before the owner goroutine can observe the exit.
func (m *Manager) trackExitLocked(cmd *exec.Cmd) {
	if m.exited == nil {
		m.exited = make(map[*exec.Cmd]chan struct{})
	}
	m.exited[cmd] = make(chan struct{})
}

// markExitedLocked signals waiters that cmd has been reaped. Must be called
// with m.mu held, by the goroutine that called cmd.Wait.
func (m *Manager) markExitedLocked(cmd *exec.Cmd) {
	if ch, ok := m.exited[cmd]; ok {
		delete(m.exited, cmd)
		close(ch)
	}
}

// exitedChan returns a channel closed once cmd has been reaped by its owner.
// For a process that was never started or has already been reaped it
// returns an already-closed channel.
func (m *Manager) exitedChan(cmd *exec.Cmd) <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ch, ok := m.exited[cmd]; ok {
		return ch
	}
	closed := make(chan struct{})
	close(closed)
	return closed
}

// clearProcessLocked removes cmd from the tracking maps for id, but only if
// the maps still refer to this cmd: a replacement process spawned by
// RestartModule must not be untracked by the old process's waiter. Must be
// called with m.mu held.
func (m *Manager) clearProcessLocked(id string, cmd *exec.Cmd) {
	if cur, ok := m.processes[id]; ok && cur == cmd {
		delete(m.processes, id)
	}
	if cur, ok := m.pendingProcesses[id]; ok && cur == cmd {
		delete(m.pendingProcesses, id)
	}
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
		processes:         make(map[string]*exec.Cmd),
		proxies:           make(map[string]*SidecarProxy),
		pendingProcesses:  make(map[string]*exec.Cmd),
		finishedProcesses: make(map[string]error),
		exited:            make(map[*exec.Cmd]chan struct{}),
		binaries:          make(map[string]*ModuleBinary),
		spawnCancel:       make(map[string]context.CancelFunc),
		resolving:         make(map[string]bool),
		meshAddr:          meshAddr,
		cacheDir:          filepath.Join(home, ".muxcore", "modules"),
		reg:               reg,
		modMgr:            modMgr,
		ScanPolicy:        DefaultScanPolicy,
		cmdRunner:         execCommandRunner{},
		sandboxRunner:     sandbox.FromEnv(),
	}
}

// SetSandboxRunner overrides the module isolation runner (tests / ops).
func (m *Manager) SetSandboxRunner(r sandbox.Runner) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r == nil {
		m.sandboxRunner = sandbox.NoopRunner{}
		return
	}
	m.sandboxRunner = r
}

// SetAllowedRepoHosts restricts module resolution to repos hosted on the given hosts.
// Pass nil or empty to allow all hosts (not recommended for production).
func (m *Manager) SetAllowedRepoHosts(hosts []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(hosts) == 0 {
		m.allowedRepoHosts = nil
		return
	}
	m.allowedRepoHosts = make([]string, len(hosts))
	copy(m.allowedRepoHosts, hosts)
}

// SetWatchdogPath sets the path to the muxcore-watchdog binary.
// When set, SpawnWithWatchdog can be used to launch modules with
// automatic core failover. If empty, SpawnWithWatchdog falls back
// to the regular Spawn behavior.
func (m *Manager) SetWatchdogPath(path string) {
	m.watchdogPath = path
}

// SetTag stores the spool tag definition so the manager can look up module
// repos and versions for resurrection. Must be called before Spawn or
// ResurrectOrphan.
func (m *Manager) SetTag(tag *contracts.TagDefinition) {
	m.tagModules = make(map[string]contracts.TagModule, len(tag.Modules))
	for _, tm := range tag.Modules {
		// Derive module ID the same way Resolve does.
		m.tagModules[ModuleIDFromRepo(tm.Repo)] = tm
	}
}

// ResurrectOrphan resolves and spawns a module that was running on a departed
// cluster node. This node must have the module's tag entry cached (via SetTag).
// Returns an error if the module is already running locally, not in the tag,
// or if resolution/spawning fails.
func (m *Manager) ResurrectOrphan(ctx context.Context, moduleID string) error {
	tm, ok := m.tagModules[moduleID]
	if !ok {
		return fmt.Errorf("module %q not found in tag cache", moduleID)
	}

	// Atomically check if the module is already running or being resolved,
	// and mark it as resolving to prevent duplicate concurrent attempts.
	m.mu.Lock()
	if _, exists := m.processes[moduleID]; exists {
		m.mu.Unlock()
		return nil // already running, nothing to do
	}
	if m.resolving[moduleID] {
		m.mu.Unlock()
		return fmt.Errorf("module %q is already being resolved by another goroutine", moduleID)
	}
	m.resolving[moduleID] = true
	m.mu.Unlock()

	// Ensure the resolving flag is cleared on return regardless of outcome.
	defer func() {
		m.mu.Lock()
		delete(m.resolving, moduleID)
		m.mu.Unlock()
	}()

	// Check if registered but process not tracked (self-registered sidecar).
	if entry, err := m.reg.Resolve(moduleID); err == nil && entry.State == contracts.ModuleStateRunning {
		return nil
	}

	bin, err := m.ResolveTagModule(tm)
	if err != nil {
		return fmt.Errorf("resolve orphan %q: %w", moduleID, err)
	}
	if err := m.VerifyChecksum(bin, tm.Checksum); err != nil {
		return fmt.Errorf("checksum orphan %q: %w", moduleID, err)
	}
	if err := m.VerifyPublisher(tm.Publisher); err != nil {
		return fmt.Errorf("publisher orphan %q: %w", moduleID, err)
	}
	if err := m.VerifyMarketplaceSignature(bin, tm.Signature); err != nil {
		return fmt.Errorf("signature orphan %q: %w", moduleID, err)
	}
	if err := m.Spawn(ctx, bin); err != nil {
		return fmt.Errorf("spawn orphan %q: %w", moduleID, err)
	}

	slog.Info("module resurrected after node departure",
		"module", moduleID, "repo", tm.Repo, "version", tm.Version)
	return nil
}

// ResurrectPendingOrphans attempts to resurrect every module in the tag
// cache that is not already running on this node. Used when this node
// becomes leader after a leader change.
func (m *Manager) ResurrectPendingOrphans(ctx context.Context) {
	m.mu.Lock()
	tagModules := make(map[string]contracts.TagModule, len(m.tagModules))
	for k, v := range m.tagModules {
		tagModules[k] = v
	}
	m.mu.Unlock()

	for moduleID := range tagModules {
		// Check if already running.
		m.mu.Lock()
		_, hasProcess := m.processes[moduleID]
		m.mu.Unlock()
		if hasProcess {
			continue
		}
		// Check if registered but process not tracked (self-registered sidecar).
		if entry, err := m.reg.Resolve(moduleID); err == nil && entry.State == contracts.ModuleStateRunning {
			continue
		}
		if err := m.ResurrectOrphan(ctx, moduleID); err != nil {
			slog.Debug("resurrection check for orphan", "module", moduleID, "error", err)
		}
	}
}

// SetCommandRunner sets the command runner for testing.
func (m *Manager) SetCommandRunner(runner CommandRunner) {
	m.cmdRunner = runner
}

// Resolve locates a module binary for the given repo and version.
// Resolution order: cache → build from source.
func (m *Manager) Resolve(repoURL, version string) (*ModuleBinary, error) {
	return m.resolveWithInstance(repoURL, version, "", nil, "")
}

// ResolveTagModule resolves a full TagModule entry including instance ID
// and config, producing a ModuleBinary with a compound ID if an instance
// ID is set.
func (m *Manager) ResolveTagModule(tm contracts.TagModule) (*ModuleBinary, error) {
	return m.resolveWithInstance(tm.Repo, tm.Version, tm.InstanceID, tm.Config, tm.Checksum)
}

func (m *Manager) resolveWithInstance(repoURL, version, instanceID string, config map[string]string, checksum string) (*ModuleBinary, error) {
	m.resolveCount.Add(1)
	if !versionPattern.MatchString(version) {
		return nil, fmt.Errorf("invalid module version %q — must match semver pattern %s", version, versionPattern.String())
	}

	// Validate repo URL against allowed host list (supply chain protection).
	parsed, err := url.Parse(repoURL)
	if err != nil {
		return nil, fmt.Errorf("invalid repo URL %q: %w", repoURL, err)
	}
	m.mu.Lock()
	hosts := m.allowedRepoHosts
	m.mu.Unlock()
	if len(hosts) > 0 {
		hostAllowed := false
		for _, h := range hosts {
			if parsed.Host == h {
				hostAllowed = true
				break
			}
		}
		if !hostAllowed {
			return nil, fmt.Errorf("repo host %q is not in the allowed repos list", parsed.Host)
		}
	}

	moduleID := ModuleIDFromRepoWithInstance(repoURL, instanceID)

	cachedPath := filepath.Join(m.cacheDir, moduleID, version, "muxcore-module")
	if _, err2 := os.Stat(cachedPath); err2 == nil {
		slog.Info("module found in cache", "id", moduleID, "version", version)
		return &ModuleBinary{ID: moduleID, Version: version, Path: cachedPath, Repo: repoURL, InstanceID: instanceID}, nil
	}

	slog.Info("module not in cache, building from source", "id", moduleID, "repo", repoURL)

	buildDir := filepath.Join(os.TempDir(), "muxcore-build", moduleID)
	if err2 := os.RemoveAll(buildDir); err2 != nil {
		return nil, fmt.Errorf("clean build dir: %w", err2)
	}

	cloneCmd := exec.Command("git", "clone", "--depth", "1", "--branch", version, repoURL, buildDir) //nolint:gosec,noctx // repoURL/version from config; intended to build arbitrary modules
	if out, err2 := cloneCmd.CombinedOutput(); err2 != nil {
		return nil, fmt.Errorf("clone %s@%s: %w\n%s", repoURL, version, err2, out)
	}

	// Run contract reconciliation if the module declares non-canonical contracts.
	// Reads muxcore.json for a "contracts" field, checks structural compatibility
	// against canonical Muxcore-Media contract repos, and applies go.mod replace
	// directives to normalize imports. No-op for modules using canonical contracts.
	if err2 := m.reconcileContracts(buildDir); err2 != nil {
		return nil, fmt.Errorf("contract reconciliation for %s: %w", moduleID, err2)
	}

	// Pre-build source scan: detect dangerous patterns before compilation.
	// The go build step can execute go:generate directives, init() functions,
	// and cgo code. We scan for these patterns first so operators can audit
	// modules that use them. Rejected patterns are controlled by ScanPolicy
	// (default: reject unsafe, cgo, and go:generate).
	scanResults, err := scanModuleSource(buildDir, m.ScanPolicy)
	if err != nil {
		return nil, fmt.Errorf("source scan for %s: %w", moduleID, err)
	}
	// Also scan go.mod for replace directives (dependency redirection).
	scanResults = append(scanResults, scanGoMod(buildDir)...)
	if len(scanResults) > 0 {
		slog.Warn("module source scan found patterns requiring audit",
			"module", moduleID, "patterns", scanResults,
		)
	}

	binPath := filepath.Join(buildDir, "muxcore-module")
	buildCmd, err := canonicalBuildCmd(buildDir, binPath)
	if err != nil {
		return nil, fmt.Errorf("build %s: %w", moduleID, err)
	}
	if out, err := buildCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("build %s: %w\n%s", moduleID, err, out)
	}

	cacheBinDir := filepath.Join(m.cacheDir, moduleID, version)
	if err := os.MkdirAll(cacheBinDir, 0o700); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	cacheBinPath := filepath.Join(cacheBinDir, "muxcore-module")
	if err := os.Rename(binPath, cacheBinPath); err != nil {
		data, readErr := os.ReadFile(binPath) //nolint:gosec // binPath is internally constructed
		if readErr != nil {
			slog.Warn("failed to read built module binary for cache", "id", moduleID, "error", readErr)
		} else if err := os.WriteFile(cacheBinPath, data, 0o600); err != nil { //nolint:gosec // cacheBinPath is internally constructed
			slog.Warn("failed to cache module binary", "id", moduleID, "error", err)
		}
	}

	// Verify the built binary against the expected checksum if one was provided.
	if checksum != "" {
		bin := &ModuleBinary{ID: moduleID, Version: version, Path: cacheBinPath, Repo: repoURL, InstanceID: instanceID}
		if err := m.VerifyChecksum(bin, checksum); err != nil {
			return nil, fmt.Errorf("build %s: %w", moduleID, err)
		}
	}

	slog.Info("module built and cached", "id", moduleID, "version", version)
	return &ModuleBinary{ID: moduleID, Version: version, Path: cacheBinPath, Repo: repoURL, InstanceID: instanceID}, nil
}

// Spawn starts a module binary as a child process.
func (m *Manager) Spawn(ctx context.Context, bin *ModuleBinary) error {
	if bin == nil {
		return fmt.Errorf("module binary is nil")
	}
	m.spawnCount.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.processes[bin.ID]; exists {
		return fmt.Errorf("module %s already running", bin.ID)
	}

	m.binaries[bin.ID] = bin

	// Build command args with TLS certs if a CA is configured.
	args := []string{
		"--muxcore-mesh-addr", m.meshAddr,
		"--muxcore-module-id", bin.ID,
	}
	tlsCertDir := ""
	if ca := m.certAuth; ca != nil {
		tlsCertDir = filepath.Join(os.TempDir(), "muxcore-certs", bin.ID)
		if rmErr := os.RemoveAll(tlsCertDir); rmErr != nil {
			slog.Warn("clean cert dir for spawn", "id", bin.ID, "error", rmErr)
		}
		certPath, keyPath, certErr := ca.IssueModuleCertForDir(bin.ID, tlsCertDir)
		if certErr != nil {
			slog.Error("issue cert for module spawn", "id", bin.ID, "error", certErr)
		} else {
			caPath := filepath.Join(tlsCertDir, "ca.crt")
			if writeErr := os.WriteFile(caPath, ca.CACertPEM(), 0o600); writeErr != nil {
				slog.Warn("write CA cert for spawn", "id", bin.ID, "error", writeErr)
			}
			args = append(args,
				"--muxcore-tls-cert", certPath,
				"--muxcore-tls-key", keyPath,
				"--muxcore-tls-ca", caPath,
			)
		}
	}
	cmd := m.cmdRunner.CommandContext(ctx, bin.Path, args...) //nolint:gosec // bin.Path is internally built from cache
	// Inject instance-specific config as environment variables.
	// Preserve the parent process environment so PATH, HOME, etc. are inherited.
	cmd.Env = os.Environ()
	for k, v := range bin.Config {
		cmd.Env = append(cmd.Env, "MUXCORE_CFG_"+k+"="+v)
	}
	runner := m.sandboxRunner
	if runner == nil {
		runner = sandbox.NoopRunner{}
	}
	wrapped, wrapErr := runner.Wrap(ctx, sandbox.Spec{
		ModuleID: bin.ID,
		BinPath:  bin.Path,
		Args:     args,
		Env:      cmd.Env,
	})
	if wrapErr != nil {
		return fmt.Errorf("sandbox wrap %s: %w", bin.ID, wrapErr)
	}
	if wrapped.Mode != sandbox.ModeNone {
		cmd = m.cmdRunner.CommandContext(ctx, wrapped.Path, wrapped.Args...) //nolint:gosec // sandbox runner path is validated by internal/sandbox
		cmd.Env = wrapped.Env
		slog.Info("module spawn sandboxed", "id", bin.ID, "mode", wrapped.Mode, "runner", wrapped.Path)
	}
	// Prefix module output so it's distinguishable from core's own log files.
	// In JSON log mode (MUXCORE_LOG_FORMAT=json) unprefixed text output from
	// modules corrupts the JSON stream — this prefix makes filtering possible.
	cmd.Stdout = newPrefixedWriter(os.Stdout, "[module:"+bin.ID+"] ")
	cmd.Stderr = newPrefixedWriter(os.Stderr, "[module:"+bin.ID+":err] ")

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn %s: %w", bin.ID, err)
	}

	// Cancel any previous spawn context for this module (defensive).
	if cancel, ok := m.spawnCancel[bin.ID]; ok {
		cancel()
	}
	spawnCtx, spawnCancel := context.WithCancel(ctx) //nolint:gosec // cancel stored in m.spawnCancel for lifecycle management
	m.spawnCancel[bin.ID] = spawnCancel

	m.processes[bin.ID] = cmd
	m.trackExitLocked(cmd)
	slog.Info("module spawned", "id", bin.ID, "pid", cmd.Process.Pid)

	// Store in pending — watchProcess owns cmd.Wait() and will notify
	// the proxy via setExit when the process exits.
	m.pendingProcesses[bin.ID] = cmd

	go m.watchProcess(spawnCtx, cmd, bin)

	return nil
}

// SpawnWithWatchdog starts a module binary via the muxcore-watchdog wrapper.
// The watchdog monitors core connectivity and reconnects to fallback addresses
// on failure, keeping the module alive across core restarts.
//
// addrs should include the local core's gRPC address plus any cluster peers.
// The watchdog binary must have been set via SetWatchdogPath.
//
// Unlike Spawn, the module's restart policy is handled by the watchdog rather
// than by watchProcess. The module registers itself via gRPC as normal.
func (m *Manager) SpawnWithWatchdog(ctx context.Context, bin *ModuleBinary, addrs []string) error {
	if bin == nil {
		return fmt.Errorf("module binary is nil")
	}
	m.spawnCount.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.processes[bin.ID]; exists {
		return fmt.Errorf("module %s already running", bin.ID)
	}

	m.binaries[bin.ID] = bin

	if m.watchdogPath == "" {
		slog.Warn("watchdog path not set, falling back to direct spawn", "id", bin.ID)
		return m.spawnLocked(ctx, bin, m.meshAddr)
	}

	// Build comma-separated address list for the watchdog.
	addrStr := m.meshAddr
	for _, a := range addrs {
		if a != m.meshAddr {
			addrStr += "," + a
		}
	}

	cmd := m.cmdRunner.CommandContext(ctx, m.watchdogPath,
		"--module-path", bin.Path,
		"--module-id", bin.ID,
		"--mesh-addrs", addrStr,
	)
	// The watchdog prefixes its own output as "[watchdog:<id>]".
	cmd.Stdout = newPrefixedWriter(os.Stdout, "[watchdog:"+bin.ID+"] ")
	cmd.Stderr = newPrefixedWriter(os.Stderr, "[watchdog:"+bin.ID+":err] ")

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn watchdog %s: %w", bin.ID, err)
	}

	m.processes[bin.ID] = cmd
	m.trackExitLocked(cmd)
	slog.Info("module spawned via watchdog", "id", bin.ID, "pid", cmd.Process.Pid, "addrs", addrStr)

	// The watchdog manages the module lifecycle, including restarts.
	// We only track the watchdog process so StopAll can kill it.
	// The module registers via gRPC independently.
	m.pendingProcesses[bin.ID] = cmd

	// This goroutine is the sole caller of cmd.Wait for the watchdog process.
	go func() {
		err := cmd.Wait()
		m.mu.Lock()
		m.clearProcessLocked(bin.ID, cmd)
		if proxy, ok := m.proxies[bin.ID]; ok {
			proxy.setExit(err)
		} else {
			m.finishedProcesses[bin.ID] = err
		}
		m.markExitedLocked(cmd)
		m.mu.Unlock()
	}()

	return nil
}

// spawnLocked is the internal spawn logic used when the watchdog fallback
// path is taken. Must be called with m.mu held.
func (m *Manager) spawnLocked(ctx context.Context, bin *ModuleBinary, addr string) error {
	if bin == nil {
		return fmt.Errorf("module binary is nil")
	}
	cmd := m.cmdRunner.CommandContext(ctx, bin.Path,
		"--muxcore-mesh-addr", addr,
		"--muxcore-module-id", bin.ID,
	)
	// Inject instance-specific config as environment variables.
	for k, v := range bin.Config {
		cmd.Env = append(cmd.Env, "MUXCORE_CFG_"+k+"="+v)
	}
	cmd.Stdout = newPrefixedWriter(os.Stdout, "[module:"+bin.ID+"] ")
	cmd.Stderr = newPrefixedWriter(os.Stderr, "[module:"+bin.ID+":err] ")

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn %s: %w", bin.ID, err)
	}

	if cancel, ok := m.spawnCancel[bin.ID]; ok {
		cancel()
	}
	spawnCtx, spawnCancel := context.WithCancel(ctx) //nolint:gosec // cancel stored in m.spawnCancel for lifecycle management
	m.spawnCancel[bin.ID] = spawnCancel

	m.processes[bin.ID] = cmd
	m.trackExitLocked(cmd)
	slog.Info("module spawned", "id", bin.ID, "pid", cmd.Process.Pid)
	m.pendingProcesses[bin.ID] = cmd

	go m.watchProcess(spawnCtx, cmd, bin)

	return nil
}

// watchProcess monitors a sidecar process and applies the restart policy on exit.
func (m *Manager) watchProcess(ctx context.Context, cmd *exec.Cmd, bin *ModuleBinary) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("watch process panic recovered", "module", bin.ID, "panic", r)
		}
	}()
	certDir := filepath.Join(os.TempDir(), "muxcore-certs", bin.ID)
	defer func() { _ = os.RemoveAll(certDir) }()

	// Build base command args reused across restarts. TLS cert files persist
	// until the defer cleanup above runs (on final exit, not per-restart).
	baseArgs := []string{
		"--muxcore-mesh-addr", m.meshAddr,
		"--muxcore-module-id", bin.ID,
	}
	if _, statErr := os.Stat(filepath.Join(certDir, "module.crt")); statErr == nil {
		baseArgs = append(baseArgs,
			"--muxcore-tls-cert", filepath.Join(certDir, "module.crt"),
			"--muxcore-tls-key", filepath.Join(certDir, "module.key"),
		)
		if _, caStat := os.Stat(filepath.Join(certDir, "ca.crt")); caStat == nil {
			baseArgs = append(baseArgs, "--muxcore-tls-ca", filepath.Join(certDir, "ca.crt"))
		}
	}

	for attempt := 0; ; attempt++ {
		// watchProcess is the sole caller of cmd.Wait for every process it
		// owns (the initial one and each restart). Other paths wait on the
		// exited channel signalled below.
		err := cmd.Wait()

		m.mu.Lock()
		m.clearProcessLocked(bin.ID, cmd)
		if proxy, ok := m.proxies[bin.ID]; ok {
			proxy.setExit(err)
		} else {
			m.finishedProcesses[bin.ID] = err
		}
		m.markExitedLocked(cmd)
		m.mu.Unlock()

		if ctx.Err() != nil {
			return
		}

		cleanExit := err == nil
		shouldRestart := false
		switch bin.RestartPolicy {
		case RestartAlways:
			shouldRestart = attempt < maxRestartAttempts
		case RestartOnFailure:
			shouldRestart = !cleanExit && attempt < maxRestartAttempts
		default:
			shouldRestart = false
		}

		if !cleanExit {
			slog.Error("module exited unexpectedly",
				"id", bin.ID, "error", err,
				"attempt", attempt, "restart", shouldRestart,
			)
		}

		if !shouldRestart {
			return
		}

		backoff := time.Duration(1<<attempt) * time.Second
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
		m.restartCount.Add(1)
		slog.Info("restarting module", "id", bin.ID, "backoff", backoff, "attempt", attempt+1)

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		// Spawn a new process with the same baseArgs (includes TLS if issued).
		newCmd := m.cmdRunner.CommandContext(ctx, bin.Path, baseArgs...) //nolint:gosec // bin.Path is internally built
		newCmd.Env = os.Environ()
		for k, v := range bin.Config {
			newCmd.Env = append(newCmd.Env, "MUXCORE_CFG_"+k+"="+v)
		}
		newCmd.Stdout = newPrefixedWriter(os.Stdout, "[module:"+bin.ID+"] ")
		newCmd.Stderr = newPrefixedWriter(os.Stderr, "[module:"+bin.ID+":err] ")

		if startErr := newCmd.Start(); startErr != nil {
			slog.Error("module restart failed", "id", bin.ID, "error", startErr)
			return
		}

		m.mu.Lock()
		if ctx.Err() != nil {
			m.mu.Unlock()
			// Cancelled (StopAll/RestartModule) while the replacement was
			// starting. newCmd was created with ctx, so exec has already
			// killed it; reap it so it does not linger as a zombie.
			_ = newCmd.Wait()
			return
		}
		m.processes[bin.ID] = newCmd
		m.trackExitLocked(newCmd)
		m.mu.Unlock()
		slog.Info("module restarted", "id", bin.ID, "pid", newCmd.Process.Pid)

		cmd = newCmd
	}
}

// RestartModule kills the running process for the given module ID and
// re-spawns it using the stored ModuleBinary. Only works for modules that
// were spawned directly (not via watchdog). Returns an error if the module
// is not found or the spawn fails.
func (m *Manager) RestartModule(ctx context.Context, moduleID string) error {
	m.mu.Lock()
	cmd, hasProcess := m.processes[moduleID]
	bin, hasBinary := m.binaries[moduleID]

	// Cancel old spawn context so watchProcess exits on the next
	// iteration and does not attempt a competing restart.
	if cancel, ok := m.spawnCancel[moduleID]; ok {
		cancel()
		delete(m.spawnCancel, moduleID)
	}

	m.mu.Unlock()

	if !hasBinary {
		return fmt.Errorf("module %q not found in binaries", moduleID)
	}

	if hasProcess && cmd.Process != nil {
		slog.Info("restarting module (health-triggered)", "id", moduleID)
		if sigErr := cmd.Process.Signal(os.Interrupt); sigErr != nil {
			slog.Warn("restart module: signal interrupt failed, process may have exited", "module", moduleID, "error", sigErr)
		}
		// The process's owner goroutine (watchProcess or the watchdog waiter)
		// is the only caller of cmd.Wait; wait for it to reap the process.
		done := m.exitedChan(cmd)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			if killErr := cmd.Process.Kill(); killErr != nil {
				slog.Warn("restart module: kill failed", "module", moduleID, "error", killErr)
			}
			<-done
		}
	}

	return m.Spawn(ctx, bin)
}

// TrackProxy registers a SidecarProxy for health tracking.
// Called during gRPC registration. If the module was already spawned
// before registration, attaches the running process to the proxy.
func (m *Manager) TrackProxy(moduleID string, proxy *SidecarProxy) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.proxies[moduleID] = proxy
	delete(m.pendingProcesses, moduleID)
	// Deliver any cached exit error from a process that exited before
	// the proxy registered.
	if exitErr, ok := m.finishedProcesses[moduleID]; ok {
		proxy.setExit(exitErr)
		delete(m.finishedProcesses, moduleID)
	}
}

// VerifyChecksum reads the binary at bin.Path, computes its SHA256 digest,
// and compares it against the expected hex string. Returns nil if they match.
// Returns nil if expected is empty (no checksum to verify against).
// Returns an error on mismatch, or if the file cannot be read.
func (m *Manager) VerifyChecksum(bin *ModuleBinary, expected string) error {
	if bin == nil {
		return fmt.Errorf("verify checksum: module binary is nil")
	}
	if expected == "" {
		return nil // no checksum provided; skip verification
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

// VerifyPublisher enforces MUXCORE_SPOOL_ALLOWED_PUBLISHERS when configured.
func (m *Manager) VerifyPublisher(publisher string) error {
	if err := spool.CheckPublisherAllowlist(publisher); err != nil {
		return fmt.Errorf("publisher: %w", err)
	}
	return nil
}

// VerifySignature checks an optional ed25519 detached signature for the
// module binary (inline TagModule.Signature or sidecar .sig/.minisig).
// Controlled by MUXCORE_SPOOL_REQUIRE_SIGNATURE + MUXCORE_SPOOL_PUBLIC_KEY
// and/or MUXCORE_SPOOL_TRUSTED_KEYS_DIR. Supports minisign hashed (ED) mode.
func (m *Manager) VerifySignature(bin *ModuleBinary, inlineSignature string) error {
	if bin == nil {
		return fmt.Errorf("verify signature: module binary is nil")
	}
	if err := spool.VerifyArtifactSignature(bin.Path, inlineSignature); err != nil {
		return fmt.Errorf("signature for %s: %w", bin.ID, err)
	}
	return nil
}

// VerifyMarketplaceSignature is VerifySignature for modules deployed at
// runtime (marketplace DeployTag, orphan resurrection). When
// SetRequireMarketplaceSignatures(true) is in effect (household profile) a
// valid signature from a configured trusted key is mandatory; otherwise it
// behaves like VerifySignature (MUXCORE_SPOOL_REQUIRE_SIGNATURE opt-in).
func (m *Manager) VerifyMarketplaceSignature(bin *ModuleBinary, inlineSignature string) error {
	if bin == nil {
		return fmt.Errorf("verify signature: module binary is nil")
	}
	if err := spool.VerifyArtifactSignatureRequired(bin.Path, inlineSignature, m.requireMarketplaceSigs.Load()); err != nil {
		return fmt.Errorf("signature for %s: %w", bin.ID, err)
	}
	return nil
}

// PruneCache removes old cached module binaries, keeping only the most recent
// keepVersions versions per module ID. Versions are sorted lexicographically
// (semver tags produced by `git describe` sort correctly this way). A
// keepVersions of 0 removes all cached versions. Call on startup to prevent
// unbounded cache growth.
func (m *Manager) PruneCache(keepVersions int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

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
	// Clear all tracking maps so watchProcess goroutines see empty state
	// and do not attempt competing restarts during shutdown.
	m.processes = make(map[string]*exec.Cmd)
	m.pendingProcesses = make(map[string]*exec.Cmd)
	m.proxies = make(map[string]*SidecarProxy)
	m.binaries = make(map[string]*ModuleBinary)
	m.finishedProcesses = make(map[string]error)
	for _, cancel := range m.spawnCancel {
		cancel()
	}
	m.spawnCancel = make(map[string]context.CancelFunc)
	m.mu.Unlock()

	for _, cmd := range cmds {
		if cmd.Process != nil {
			if sigErr := cmd.Process.Signal(os.Interrupt); sigErr != nil {
				slog.Warn("stop modules: signal interrupt failed", "error", sigErr)
			}
		}
	}

	// Each process's owner goroutine (watchProcess or the watchdog waiter)
	// is the only caller of cmd.Wait; collect their exit signals instead of
	// calling Wait here, which would race with the owner.
	exitChans := make([]<-chan struct{}, 0, len(cmds))
	for _, cmd := range cmds {
		exitChans = append(exitChans, m.exitedChan(cmd))
	}
	done := make(chan struct{})
	go func() {
		for _, ch := range exitChans {
			<-ch
		}
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		for _, cmd := range cmds {
			if cmd.Process != nil {
				if killErr := cmd.Process.Kill(); killErr != nil {
					slog.Warn("stop modules: kill failed", "error", killErr)
				}
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

func infoFromProto(m *modulev1.ModuleInfo) contracts.ModuleInfo {
	return contracts.ModuleInfo{
		ID:             m.GetId(),
		Name:           m.GetName(),
		Version:        m.GetVersion(),
		Roles:          m.GetRoles(),
		Description:    m.GetDescription(),
		Author:         m.GetAuthor(),
		Capabilities:   m.GetCapabilities(),
		DependsOn:      m.GetDependsOn(),
		MinCoreVersion: m.GetMinCoreVersion(),
		HTTPAddr:       m.GetHttpAddr(),
	}
}

func (s *registrationServer) Register(ctx context.Context, req *modulev1.RegisterRequest) (*modulev1.RegisterResponse, error) {
	var info contracts.ModuleInfo
	if req.ModuleInfo != nil {
		info = infoFromProto(req.ModuleInfo)
	}

	if info.ID == "" {
		info.ID = req.ModuleId
	}
	if info.ID == "" {
		return &modulev1.RegisterResponse{MeshAddr: s.mgr.meshAddr, Accepted: false, Error: "module ID is required"}, nil
	}

	opts, verified, err := s.mgr.authorizeRegistration(ctx, info)
	if err != nil {
		return nil, err
	}

	if info.Name == "" {
		info.Name = info.ID
		slog.Warn("module registered without a name, using ID as name", "module_id", info.ID)
	}
	if info.Version == "" {
		info.Version = "0.0.0"
		slog.Warn("module registered without a version, assuming 0.0.0", "module_id", info.ID)
	}

	// Create a proxy that satisfies contracts.Module for registry registration.
	proxy := NewSidecarProxy(info)
	proxy.verified = verified

	// Register with the module lifecycle manager (adds to registry, publishes
	// events). The proxy is tracked only once the registration is accepted,
	// so a rejected registration never displaces the registered module's.
	deps := info.DependsOn
	replaced, err := s.mgr.modMgr.RegisterWith(ctx, proxy, deps, opts)
	if err != nil {
		if errors.Is(err, registry.ErrExclusiveConflict) {
			slog.Warn("module registration rejected: exclusive security capability already provided",
				"id", info.ID, "error", err)
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		slog.Error("module registration: registry", "id", info.ID, "error", err)
		return &modulev1.RegisterResponse{MeshAddr: s.mgr.meshAddr, Accepted: false, Error: err.Error()}, nil
	}
	s.mgr.TrackProxy(info.ID, proxy)

	slog.Info("module registered via gRPC and added to registry",
		"id", info.ID,
		"name", info.Name,
		"version", info.Version,
		"roles", info.Roles,
		"capabilities", info.Capabilities,
		"deps", deps,
		"replaced", replaced,
	)

	if hook := s.mgr.PostRegisterHook; hook != nil {
		hook(info.ID, info.Capabilities)
	}

	return &modulev1.RegisterResponse{MeshAddr: s.mgr.meshAddr, Accepted: true}, nil
}

// securityCapsOf returns the security capabilities among caps.
func securityCapsOf(caps []string) []string {
	var out []string
	for _, c := range SecurityCapabilities {
		if slicesContains(caps, c) {
			out = append(out, c)
		}
	}
	return out
}

// authorizeRegistration applies the ADR-0018 rules (see RegistrationPolicy)
// to a Register call and returns the registry options to register with, or
// a gRPC status error when the registration is rejected. verified reports
// whether the caller presented a verified certificate for info.ID.
func (m *Manager) authorizeRegistration(ctx context.Context, info contracts.ModuleInfo) (opts registry.RegisterOptions, verified bool, err error) {
	pol := m.RegistrationPolicy()
	profileName := "dev"
	if pol.Household {
		profileName = "household"
	}
	secCaps := securityCapsOf(info.Capabilities)
	cn, verified := peerid.VerifiedModuleID(ctx)

	// Rule 1 (both profiles): a verified certificate may only register its
	// own module ID.
	if verified && cn != info.ID {
		slog.Warn("module registration rejected: certificate CN does not match module ID",
			"id", info.ID, "cert_cn", cn, "profile", profileName)
		return registry.RegisterOptions{}, false, status.Errorf(codes.PermissionDenied,
			"certificate is issued for module %q, cannot register %q", cn, info.ID)
	}

	// Rules 2 and 3: registrations without a verified certificate.
	if !verified {
		switch {
		case len(secCaps) > 0 && pol.Household:
			slog.Warn("module registration rejected: security capability without a verified module certificate",
				"id", info.ID, "capabilities", secCaps, "profile", profileName)
			return registry.RegisterOptions{}, false, status.Errorf(codes.PermissionDenied,
				"registering %v requires a module certificate issued by the core CA for %q", secCaps, info.ID)
		case len(secCaps) > 0:
			slog.Warn("dev profile: security capability registered without a verified module certificate",
				"id", info.ID, "capabilities", secCaps)
		case pol.Household && pol.RequireModuleCerts:
			slog.Warn("module registration rejected: no verified module certificate (MUXCORE_REQUIRE_MODULE_CERTS)",
				"id", info.ID, "profile", profileName)
			return registry.RegisterOptions{}, false, status.Errorf(codes.PermissionDenied,
				"registering %q requires a module certificate issued by the core CA", info.ID)
		case pol.Household:
			slog.Warn("module registered without a verified module certificate; "+
				"this will be rejected when MUXCORE_REQUIRE_MODULE_CERTS is enabled",
				"id", info.ID, "profile", profileName)
		}
	}

	// Rule 5: the verified owner of an ID atomically replaces its own
	// sidecar registration. In-process modules are never replaced.
	//
	// dev profile: an unverified re-registration of the same ID also
	// replaces a stale sidecar entry, provided that entry was itself
	// registered without a verified certificate. dev already trusts the
	// client-supplied x-caller-id, and a restarted module (which may not
	// have unregistered on exit) would otherwise be refused forever.
	// Replace keeps the entry's registration order, so the ID stays the
	// provider of record for any exclusive security capability; a
	// different ID can never replace. household keeps requiring the
	// verified certificate.
	if m.reg != nil {
		if existing, getErr := m.reg.Get(info.ID); getErr == nil {
			if old, isSidecar := existing.Module.(*SidecarProxy); isSidecar {
				switch {
				case verified:
					opts.Replace = true
				case !pol.Household && !old.verified:
					opts.Replace = true
					slog.Warn("dev profile: module re-registered without a verified certificate; "+
						"replacing the existing registration",
						"id", info.ID, "previous_version", existing.Info.Version, "version", info.Version)
				}
			}
		}
	}

	// Rule 4: one provider per security capability.
	if pol.Household {
		opts.Exclusive = secCaps
	} else if m.reg != nil {
		for _, c := range secCaps {
			if owner, ok := m.reg.Provider(c); ok && owner.Info.ID != info.ID {
				slog.Warn("dev profile: second provider of an exclusive security capability; "+
					"the first-registered provider stays wired",
					"capability", c, "provider", owner.Info.ID, "ignored", info.ID)
			}
		}
	}
	return opts, verified, nil
}

func (s *registrationServer) BootstrapRegister(ctx context.Context, req *modulev1.BootstrapRegisterRequest) (*modulev1.BootstrapRegisterResponse, error) {
	ca := s.mgr.certAuth
	if ca == nil {
		return &modulev1.BootstrapRegisterResponse{
			Accepted: false,
			Error:    "certificate authority not configured on this node",
		}, nil
	}
	reject := func(msg string) (*modulev1.BootstrapRegisterResponse, error) {
		return &modulev1.BootstrapRegisterResponse{Accepted: false, Error: msg}, nil
	}

	// Everything that can be checked without consuming the token is checked
	// first, so a malformed request does not burn a single-use token.
	var csr *x509.CertificateRequest
	var signer csrSigner
	if req.GetCsrPem() != "" {
		var err error
		if csr, err = enroll.ParseCSR(req.GetCsrPem()); err != nil {
			slog.Warn("bootstrap register: bad CSR", "claimed_id", req.GetModuleId(), "error", err)
			return reject(fmt.Sprintf("invalid csr_pem: %v", err))
		}
		var ok bool
		if signer, ok = ca.(csrSigner); !ok {
			return reject("this node cannot sign CSRs")
		}
	}
	if enroll.IsV2(req.GetToken()) {
		// ADR-0017: enrollment tokens are only for module-generated keys.
		if csr == nil {
			return reject("enrollment tokens (mct_2_) require csr_pem: the module must generate its own key")
		}
		if id, ok := enroll.TokenModuleID(req.GetToken()); ok && id != req.GetModuleId() {
			return reject(fmt.Sprintf("token issued for module %q, but request claims %q", id, req.GetModuleId()))
		}
	}

	// Validate (and consume) the single-use token.
	claimedID, err := ca.ValidateToken(req.GetToken())
	if err != nil {
		slog.Warn("bootstrap register: invalid token", "claimed_id", req.GetModuleId(), "error", err)
		return reject(fmt.Sprintf("invalid token: %v", err))
	}

	// Verify the claimed module ID matches the token's target.
	if claimedID != req.GetModuleId() {
		return reject(fmt.Sprintf("token issued for module %q, but request claims %q", claimedID, req.GetModuleId()))
	}

	ips, dns := bootstrapSANs(ca, claimedID, req.GetDnsNames())
	if csr != nil {
		certPEM, err := signer.SignModuleCSR(claimedID, csr, ips, dns)
		if err != nil {
			slog.Error("bootstrap register: sign CSR failed", "module", claimedID, "error", err)
			return reject(fmt.Sprintf("certificate signing failed: %v", err))
		}
		slog.Info("module enrolled: signed module CSR", "module", claimedID, "dns_sans", dns)
		return &modulev1.BootstrapRegisterResponse{
			Accepted:   true,
			SignedCert: string(certPEM),
			CaCert:     string(ca.CACertPEM()),
		}, nil
	}

	certData, keyData, err := issueBootstrapCert(ca, claimedID, ips, dns)
	if err != nil {
		slog.Error("bootstrap register: issue cert failed", "module", claimedID, "error", err)
		return reject(fmt.Sprintf("certificate signing failed: %v", err))
	}

	slog.Info("module bootstrap registered via token", "module", claimedID)
	return &modulev1.BootstrapRegisterResponse{
		Accepted:   true,
		SignedCert: string(certData),
		KeyPem:     string(keyData),
		CaCert:     string(ca.CACertPEM()),
	}, nil
}

// memoryCertIssuer is implemented by issuers that can return a certificate
// and key without touching disk (grpcmesh.CertAuthority).
type memoryCertIssuer interface {
	IssueModuleCert(moduleID string, ips []net.IP, dnsNames []string) (certPEM, keyPEM []byte, err error)
}

// csrSigner is implemented by issuers that sign a module-generated CSR
// (grpcmesh.CertAuthority, ADR-0017).
type csrSigner interface {
	SignModuleCSR(moduleID string, csr *x509.CertificateRequest, ips []net.IP, dnsNames []string) ([]byte, error)
}

// sanPolicyIssuer is implemented by issuers that apply the operator SAN
// allow-list (MUXCORE_ENROLL_SAN_ALLOW).
type sanPolicyIssuer interface {
	EnrollmentSANs(moduleID string, requested []string) (ips []net.IP, dns, dropped []string)
}

// bootstrapSANs returns the SANs for a bootstrap certificate: loopback,
// localhost, the module ID, and the requested DNS names the issuer's
// allow-list permits (none when the issuer has no policy).
func bootstrapSANs(ca CertIssuer, moduleID string, requested []string) ([]net.IP, []string) {
	var ips []net.IP
	var dns, dropped []string
	if p, ok := ca.(sanPolicyIssuer); ok {
		ips, dns, dropped = p.EnrollmentSANs(moduleID, requested)
	} else {
		ips, dns, dropped = enroll.SANPolicy{}.SANs(moduleID, requested)
	}
	if len(dropped) > 0 {
		slog.Warn("bootstrap register: requested SANs not in "+enroll.EnvSANAllow+"; dropped",
			"module", moduleID, "dropped", dropped)
	}
	return ips, dns
}

// issueBootstrapCert issues a certificate and private key for a
// BootstrapRegister caller without a CSR (legacy path). The key is kept in
// memory when the issuer supports it; otherwise it is written to a fresh
// private (0700) temporary directory that is removed before returning, so no
// module key is left on disk after the handoff.
func issueBootstrapCert(ca CertIssuer, moduleID string, ips []net.IP, dns []string) (certPEM, keyPEM []byte, err error) {
	if mi, ok := ca.(memoryCertIssuer); ok {
		return mi.IssueModuleCert(moduleID, ips, dns)
	}
	dir, err := os.MkdirTemp("", "muxcore-bootstrap-*") // created 0700
	if err != nil {
		return nil, nil, fmt.Errorf("create private cert dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	certPath, keyPath, err := ca.IssueModuleCertForDir(moduleID, dir)
	if err != nil {
		return nil, nil, err
	}
	certPEM, err = os.ReadFile(certPath) //nolint:gosec // path inside our private temp dir
	if err != nil {
		return nil, nil, fmt.Errorf("read signed certificate: %w", err)
	}
	keyPEM, err = os.ReadFile(keyPath) //nolint:gosec // path inside our private temp dir
	if err != nil {
		return nil, nil, fmt.Errorf("read private key: %w", err)
	}
	return certPEM, keyPEM, nil
}

func (s *registrationServer) Unregister(ctx context.Context, req *modulev1.UnregisterRequest) (*modulev1.UnregisterResponse, error) {
	if s.mgr.RegistrationPolicy().Household {
		// ADR-0018: in household only the module itself (a verified
		// certificate for its ID) may unregister it.
		cn, verified := peerid.VerifiedModuleID(ctx)
		if !verified || cn != req.ModuleId {
			slog.Warn("module unregistration rejected: caller is not the module",
				"id", req.ModuleId, "cert_cn", cn, "verified", verified)
			return nil, status.Errorf(codes.PermissionDenied,
				"unregistering %q requires a module certificate issued by the core CA for %q", req.ModuleId, req.ModuleId)
		}
	}
	if err := s.mgr.modMgr.Unregister(ctx, req.ModuleId); err != nil {
		slog.Warn("module unregistration failed", "id", req.ModuleId, "error", err)
		return &modulev1.UnregisterResponse{Acknowledged: false}, nil
	}
	slog.Info("module unregistered", "id", req.ModuleId)
	return &modulev1.UnregisterResponse{Acknowledged: true}, nil
}

// cgoExts lists file extensions that indicate cgo usage when present in a
// Go module. These files are compiled by the cgo toolchain during go build
// and can execute arbitrary C/C++ code.
var cgoExts = map[string]bool{
	".c": true, ".h": true, ".s": true, ".S": true,
	".cpp": true, ".cxx": true, ".cc": true, ".m": true, ".mm": true,
}

// scanNonGoFile checks non-.go source files for patterns that can introduce
// arbitrary code execution during the build step (cgo sources, build scripts,
// embedded binaries). Returns true if the file should not be processed further.
func scanNonGoFile(path string, d fs.DirEntry, buildDir string, policy ScanPolicy, found *[]string) (bool, error) {
	ext := strings.ToLower(filepath.Ext(path))
	base := strings.ToLower(filepath.Base(path))
	if cgoExts[ext] {
		if policy.RejectCGO {
			return true, fmt.Errorf("module contains cgo source file: %s — cgo is disabled by policy", filepath.Base(path))
		}
		if !slicesContains(*found, "cgo-source") {
			*found = append(*found, "cgo-source")
		}
		return true, nil
	}
	if base == "makefile" || strings.HasSuffix(base, ".sh") || strings.HasSuffix(base, ".bash") {
		if !slicesContains(*found, "build-script") {
			*found = append(*found, "build-script")
		}
	}
	if strings.HasPrefix(path, filepath.Join(buildDir, "vendor")) ||
		strings.Contains(path, filepath.Join("vendor", "src")) ||
		strings.Contains(path, "testdata") {
		if ext != ".go" && !cgoExts[ext] && !isTextExt(ext) {
			if !slicesContains(*found, "embedded-binary") {
				*found = append(*found, "embedded-binary")
			}
		}
	}
	return false, nil
}

// scanModuleSource scans a cloned module directory for patterns that can
// execute arbitrary code during the build step. Returns a list of found
// pattern names. Which patterns are rejected (error) vs warned depends on
// the given ScanPolicy. DefaultScanPolicy rejects unsafe, cgo, and
// Go generate directives (secure by default).
func scanModuleSource(buildDir string, policy ScanPolicy) ([]string, error) { //nolint:gocyclo // source scanning has many unavoidable pattern checks
	var found []string

	walkErr := filepath.WalkDir(buildDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // WalkDir callback returns nil to skip unreadable and continue
		}
		if d.IsDir() {
			return nil
		}

		// Check non-.Go files for cgo sources, build scripts, embedded binaries.
		if !strings.HasSuffix(path, ".go") {
			done, scanErr := scanNonGoFile(path, d, buildDir, policy, &found)
			if done || scanErr != nil {
				return scanErr
			}
			return nil
		}

		// Parse the AST so import checks are not confused by comments or
		// string literals that mention "unsafe" or "C" without importing them.
		fset := token.NewFileSet()
		f, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if parseErr != nil {
			return nil //nolint:nilerr // WalkDir callback returns nil to skip unparseable and continue
		}

		if policy.RejectUnsafe || policy.RejectCGO {
			for _, imp := range f.Imports {
				importPath := strings.Trim(imp.Path.Value, `"`)
				switch {
				case policy.RejectUnsafe && importPath == "unsafe":
					return fmt.Errorf("module imports unsafe package: %s", filepath.Base(path))
				case policy.RejectCGO && importPath == "C":
					return fmt.Errorf("module imports cgo (\"C\"): %s", filepath.Base(path))
				}
			}
		}

		// Reject //go:generate directives if the policy says so.
		if policy.RejectGoGenerate {
			for _, cg := range f.Comments {
				for _, c := range cg.List {
					if strings.HasPrefix(c.Text, "//go:generate") {
						return fmt.Errorf("module uses //go:generate directive: %s", filepath.Base(path))
					}
				}
			}
		}

		// Detect dangerous runtime patterns: reject or warn depending on policy.
		for _, imp := range f.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			switch importPath {
			case "os/exec":
				if policy.RejectExec {
					return fmt.Errorf("module imports os/exec: %s — os/exec is disabled by policy (set RejectExec=false to allow)", filepath.Base(path))
				}
				if policy.WarnExec && !slicesContains(found, "os/exec") {
					found = append(found, "os/exec")
				}
			case "syscall":
				if policy.RejectSyscall {
					return fmt.Errorf("module imports syscall: %s — syscall is disabled by policy (set RejectSyscall=false to allow)", filepath.Base(path))
				}
				if policy.WarnSyscall && !slicesContains(found, "syscall") {
					found = append(found, "syscall")
				}
			case "net/http", "net":
				if policy.RejectNetworkInit && hasInitBlock(f) {
					return fmt.Errorf("module imports %s in init(): %s — network-in-init is disabled by policy (set RejectNetworkInit=false to allow)", importPath, filepath.Base(path))
				}
				if policy.WarnNetworkInit && hasInitBlock(f) && !slicesContains(found, "network-in-init") {
					found = append(found, "network-in-init")
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

// hasInitBlock returns true if the AST has an init() function declaration.
func hasInitBlock(f *ast.File) bool {
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "init" {
			return true
		}
	}
	return false
}

func slicesContains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// isTextExt returns true for file extensions that typically contain text
// rather than compiled binaries. Used by the source scanner to distinguish
// between text source files and embedded binary blobs.
func isTextExt(ext string) bool {
	switch ext {
	case ".go", ".md", ".txt", ".json", ".yaml", ".yml", ".toml",
		".xml", ".html", ".css", ".js", ".ts", ".proto",
		".mod", ".sum", ".gitignore", ".gitkeep", ".dockerignore",
		".env", ".cfg", ".conf", ".ini", ".properties",
		".sh", ".bash", ".zsh", ".ps1", ".bat", ".cmd",
		".py", ".rb", ".pl", ".php", ".lua":
		return true
	}
	return false
}

// scanGoMod checks go.mod for replace directives that redirect module
// dependencies to non-canonical sources. Returns a warning if found.
func scanGoMod(buildDir string) []string {
	var found []string
	gmPath := filepath.Join(buildDir, "go.mod")
	data, err := os.ReadFile(gmPath) //nolint:gosec // buildDir is internally constructed from temp dir
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "replace ") {
			if !slicesContains(found, "go-mod-replace") {
				found = append(found, "go-mod-replace")
			}
		}
	}
	return found
}

func ModuleIDFromRepo(repoURL string) string {
	return ModuleIDFromRepoWithInstance(repoURL, "")
}

// validInstanceID matches safe characters for instance IDs used in file paths.
// Same pattern as validTagName: alphanumeric, underscore, hyphen, dot.
var validInstanceID = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// invalidInstanceIDChars matches any character NOT allowed in instance IDs.
// Used for sanitization: strips dangerous characters from instance IDs.
var invalidInstanceIDChars = regexp.MustCompile(`[^a-zA-Z0-9._-]`)

func ModuleIDFromRepoWithInstance(repoURL, instanceID string) string {
	u, err := url.Parse(strings.TrimSuffix(repoURL, ".git"))
	if err != nil || u.Path == "" {
		return ""
	}
	// filepath.Base strips all directory components, preventing path traversal
	// via crafted repo URLs (e.g. "https://host/owner/../escape").
	base := filepath.Base(u.Path)
	if instanceID != "" {
		if !validInstanceID.MatchString(instanceID) {
			instanceID = invalidInstanceIDChars.ReplaceAllString(instanceID, "")
		}
		return base + "-" + instanceID
	}
	return base
}

// reconcileContracts checks the module's muxcore.json for non-canonical contract
// declarations and runs structural reconciliation against the canonical registry.
// If interfaces match, applies go.mod replace directives to normalize imports.
func (m *Manager) reconcileContracts(buildDir string) error {
	muxcorePath := filepath.Join(buildDir, "muxcore.json")
	data, err := os.ReadFile(muxcorePath) //nolint:gosec // path is internally constructed from buildDir
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

	// Validate resolved directives against the allow-list: only MuxCore-Media
	// contract repos are permitted. This prevents supply chain attacks where a
	// module's muxcore.json redirects imports to attacker-controlled forks.
	allowedPrefix := "github.com/Muxcore-Media/"
	for _, d := range directives {
		if !strings.HasPrefix(d.NewPath, allowedPrefix) {
			return fmt.Errorf("contract reconciliation rejected: new path %q is not in the allowed contract prefix %s",
				d.NewPath, allowedPrefix)
		}
	}

	slog.Info("applying contract reconciliation", "module", filepath.Base(buildDir), "directives", len(directives))
	slog.Warn("contract reconciliation modifies go.mod; the build is no longer canonical and spool checksum verification may fail (ADR-0012 §3)",
		"module", filepath.Base(buildDir), "directives", len(directives))
	if err := reconciler.ApplyReplaceDirectives(buildDir, directives); err != nil {
		return fmt.Errorf("apply replace directives: %w", err)
	}

	return nil
}
