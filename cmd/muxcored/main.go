package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	"github.com/Muxcore-Media/core/internal/api"
	"github.com/Muxcore-Media/core/internal/audit"
	"github.com/Muxcore-Media/core/internal/config"
	"github.com/Muxcore-Media/core/internal/events"
	"github.com/Muxcore-Media/core/internal/grpcmesh"
	corehealth "github.com/Muxcore-Media/core/internal/health"
	modlifecycle "github.com/Muxcore-Media/core/internal/module"
	modulemgr "github.com/Muxcore-Media/core/internal/module/mgr"
	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/internal/spool"
	"github.com/Muxcore-Media/core/internal/startup"
	"github.com/Muxcore-Media/core/internal/storage"
	"github.com/Muxcore-Media/core/internal/version"
	"github.com/Muxcore-Media/core/pkg/contracts"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
)

const securityDisclaimer = `╔══════════════════════════════════════════════════════════════╗
║                    SECURITY WARNING                          ║
║                                                              ║
║  The official MuxCore spool is at:                           ║
║  https://github.com/Muxcore-Media/spool                      ║
║                                                              ║
║  Using a third-party spool (--spool <url>) means you are     ║
║  trusting an external source to define which modules run     ║
║  on your system. Third-party spools can:                     ║
║                                                              ║
║  - List modules that contain malicious code                  ║
║  - Point to forks with backdoors or data exfiltration        ║
║  - Omit security-critical modules (e.g., auth, rate limit)   ║
║  - Specify outdated versions with known vulnerabilities      ║
║  - Change their module list at any time without notice       ║
║                                                              ║
║  MODULES RUN WITH THE SAME PRIVILEGES AS THE CORE PROCESS.   ║
║  A malicious module can access your filesystem, network,     ║
║  environment variables, and any data MuxCore manages.        ║
║                                                              ║
║  Before using a third-party spool:                           ║
║  1. Audit the spool's module list and versions               ║
║  2. Verify each module repo is what it claims to be          ║
║  3. Check that security modules (auth, rate limiting)        ║
║     are included and not replaced with stubs                 ║
║  4. Pin to a specific commit/tag, not a floating branch      ║
║  5. Run MuxCore in a sandboxed environment first             ║
║                                                              ║
║  The MuxCore project makes no guarantees about modules       ║
║  sourced from third-party spools. Use at your own risk.      ║
║                                                              ║
╚══════════════════════════════════════════════════════════════╝`

func main() {
	tagName := flag.String("tag", "", "Module tag to load from the spool (e.g., 'default')")
	spoolURL := flag.String("spool", spool.DefaultSpoolURL, "Spool URL to fetch tags from")
	printVersion := flag.Bool("version", false, "Print version and exit")
	dryRun := flag.Bool("dry-run", false, "Validate config, TLS certs, and spool connectivity then exit 0 on success")
	flag.Parse()

	if *printVersion {
		fmt.Println(version.String())
		os.Exit(0)
	}

	if *tagName != "" {
		fmt.Fprintln(os.Stderr, securityDisclaimer)
	}

	configPath := os.Getenv("MUXCORE_CONFIG")
	if configPath == "" {
		configPath = "muxcore.json"
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Warn("no config file found, using defaults", "path", configPath)
			cfg = config.Default()
		} else {
			slog.Error("load config", "error", err)
			os.Exit(1)
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// SIGHUP triggers config reload.
	sighupCh := make(chan os.Signal, 1)
	signal.Notify(sighupCh, syscall.SIGHUP)

	// cfgMu protects cfg from concurrent reads during SIGHUP reload.
	var cfgMu sync.Mutex

	logger := setupLogger(cfg.Log)
	slog.SetDefault(logger)

	slog.Info("MuxCore starting...", "version", version.String())

	// Run startup invariant checks before initializing any subsystems.
	startupResults := startup.RunAll(cfg)
	if startup.HasFatal(startupResults) {
		for _, err := range startup.FatalErrors(startupResults) {
			slog.Error("startup check failed", "error", err)
		}
		os.Exit(1)
	}

	// --dry-run: validate config and spool connectivity, then exit.
	if *dryRun {
		slog.Info("dry-run: config and startup checks passed")
		if *tagName != "" {
			slog.Info("dry-run: fetching spool tag to verify connectivity", "spool", *spoolURL, "tag", *tagName)
			tag, err := spool.FetchTag(*spoolURL, *tagName)
			if err != nil {
				slog.Error("dry-run: spool connectivity failed", "error", err)
				os.Exit(1)
			}
			slog.Info("dry-run: spool OK", "tag", tag.Name, "modules", len(tag.Modules))
		}
		slog.Info("dry-run: all checks passed — ready to start")
		os.Exit(0)
	}

	bus := events.NewMemoryBus()
	// Enable WAL if MUXCORE_EVENT_JOURNAL_PATH is set.
	if journalPath := os.Getenv("MUXCORE_EVENT_JOURNAL_PATH"); journalPath != "" {
		if err := bus.EnableWAL(journalPath); err != nil {
			slog.Error("enable WAL", "path", journalPath, "error", err)
			os.Exit(1)
		}
	}
	slog.Info("event bus ready", "type", "memory")

	meshSrv := grpcmesh.NewServer()
	meshClient := grpcmesh.NewClient(meshSrv)

	creds, err := grpcmesh.GRPCTransportCredentials(
		cfg.GRPC.CertFile,
		cfg.GRPC.KeyFile,
		cfg.GRPC.CACertFile,
		cfg.GRPC.MTLSEnabled,
	)
	if err != nil {
		slog.Error("grpc tls", "error", err)
		os.Exit(1)
	}
	// gRPC max message size — default 32MB; storage streaming requires > 4MB default.
	maxMsgBytes := cfg.GRPC.MaxMessageSizeMB * 1024 * 1024
	if maxMsgBytes <= 0 {
		maxMsgBytes = 32 * 1024 * 1024
	}
	var grpcOpts []grpc.ServerOption
	grpcOpts = append(grpcOpts,
		grpc.MaxRecvMsgSize(maxMsgBytes),
		grpc.MaxSendMsgSize(maxMsgBytes),
	)
	slog.Info("gRPC message size limit", "max_mb", maxMsgBytes/1024/1024)
	if creds != nil {
		grpcOpts = append(grpcOpts, grpc.Creds(creds))
		slog.Info("gRPC TLS enabled",
			"cert", cfg.GRPC.CertFile,
			"mtls", cfg.GRPC.MTLSEnabled,
		)
	} else {
		slog.Warn("gRPC TLS is disabled — insecure mode")
	}
	// Auth interceptor: extracts caller identity from gRPC metadata and,
	// when Authorizer+IdentityProvider are set, enforces per-method access.
	authInterceptor := grpcmesh.NewAuthInterceptor()

	grpcOpts = append(grpcOpts,
		grpc.ChainUnaryInterceptor(
			grpcLoggingInterceptor,
			grpcPanicRecoveryInterceptor,
			authInterceptor.UnaryInterceptor(),
		),
		grpc.ChainStreamInterceptor(
			grpcLoggingStreamInterceptor,
			grpcPanicRecoveryStreamInterceptor,
			authInterceptor.StreamInterceptor(),
		),
	)

	// Add gRPC keepalive enforcement to prevent resource exhaustion (CWE-400).
	grpcOpts = append(grpcOpts,
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle: 5 * time.Minute,
			Time:              2 * time.Minute,
			Timeout:           20 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             30 * time.Second,
			PermitWithoutStream: true,
		}),
	)

	grpcSrv := grpc.NewServer(grpcOpts...)
	meshSrv.RegisterWithGRPC(grpcSrv)
	// Wire TLS transport credentials into mesh client for secure cross-node routing.
	// When creds is nil (no TLS certs configured), cross-node routing is disabled
	// and calls will only route to local modules.
	meshClient.SetTransportCredentials(creds)

	reg := registry.New()
	healthGrpc := grpcmesh.NewHealthServer(reg)
	healthGrpc.RegisterWithGRPC(grpcSrv)

	eventGrpc := grpcmesh.NewEventServer(bus)
	// MUXCORE_GRPC_REQUIRE_EVENTS_AUTH=true restricts event Publish/Subscribe to
	// callers with a verified identity (x-caller-id set by auth interceptor).
	// Default: open (backward compat). Enable only after deploying an auth module.
	eventGrpc.SetRequireAuth(os.Getenv("MUXCORE_GRPC_REQUIRE_EVENTS_AUTH") == "true")
	// Enable WAL-backed Replay RPC only when a journal path is configured.
	if os.Getenv("MUXCORE_EVENT_JOURNAL_PATH") != "" {
		eventGrpc.SetWALReplayer(bus)
	}
	eventGrpc.RegisterWithGRPC(grpcSrv)

	nodeID := "muxcore-" + cfg.GRPC.Addr
	discoveryGrpc := grpcmesh.NewDiscoveryServer(
		nodeID,
		cfg.GRPC.Addr,
		cfg.Server.Addr,
		cfg.GRPC.JoinToken,
		func() []string {
			entries := reg.ListAll()
			ids := make([]string, len(entries))
			for i, e := range entries {
				ids[i] = e.Info.ID
			}
			return ids
		},
	)
	// MUXCORE_GRPC_REQUIRE_DISCOVERY_AUTH=true restricts Members/Watch/Find* to
	// callers with a verified identity. Default: open (backward compat).
	discoveryGrpc.SetRequireAuth(os.Getenv("MUXCORE_GRPC_REQUIRE_DISCOVERY_AUTH") == "true")
	discoveryGrpc.RegisterWithGRPC(grpcSrv)

	// gRPC connection pool for heartbeat efficiency and cross-node routing.
	connPool := grpcmesh.NewConnPool()
	discoveryGrpc.SetConnPool(connPool)

	store := storage.NewOrchestrator(reg)
	// Zero means "use default" — SetTimeouts ignores zero values.
	store.SetTimeouts(storage.StorageTimeouts{
		Read:   time.Duration(cfg.Storage.ReadTimeoutSeconds) * time.Second,
		Write:  time.Duration(cfg.Storage.WriteTimeoutSeconds) * time.Second,
		Delete: time.Duration(cfg.Storage.DeleteTimeoutSeconds) * time.Second,
	})
	if err := store.DiscoverStorage(); err != nil {
		slog.Warn("storage discover", "error", err)
	}
	store.DiscoverCache()
	slog.Info("storage orchestrator ready", "providers", store.ProviderCount())
	watchCancel := store.WatchModules(ctx, bus)

	srv := api.NewServer(cfg.Server.Addr, cfg.Server.CertFile, cfg.Server.KeyFile)

	// Optional Prometheus-compatible /metrics endpoint.
	if os.Getenv("MUXCORE_METRICS_ENABLE") == "true" || os.Getenv("MUXCORE_METRICS_ENABLE") == "1" {
		metricsHandler := api.MetricsHandler(&api.MetricsProvider{
			DroppedEvents:        bus.DroppedEvents,
			ActiveSubscribers:    bus.SubscriberCount,
			ConnPoolSize:         connPool.Size,
			RegistryModuleCount:  reg.Count,
			LeaderTerm:           discoveryGrpc.Term,
			IsLeader:             discoveryGrpc.IsLeader,
			StorageProviderCount: store.ProviderCount,
			ModuleDegradedCount: func() int {
				var n int
				for _, e := range reg.ListAll() {
					if e.State == contracts.ModuleStateDegraded {
						n++
					}
				}
				return n
			},
			GoroutineCount: runtime.NumGoroutine,
			AllocBytes: func() uint64 {
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				return m.Alloc
			},
		})
		srv.HandleFunc("/metrics", metricsHandler)
		srv.AddPublicPath("/metrics") // metrics endpoint is public (scraper auth handled separately)
		slog.Info("metrics endpoint enabled", "path", "/metrics")
	}

	// Debug profiling endpoints (pprof). Enable via MUXCORE_DEBUG_ENABLE=true.
	if os.Getenv("MUXCORE_DEBUG_ENABLE") == "true" || os.Getenv("MUXCORE_DEBUG_ENABLE") == "1" {
		srv.HandleFunc("/debug/pprof/", pprof.Index)
		srv.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		srv.HandleFunc("/debug/pprof/profile", pprof.Profile)
		srv.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		srv.HandleFunc("/debug/pprof/trace", pprof.Trace)
		slog.Info("debug profiling endpoints enabled", "path", "/debug/pprof/")
	}

	// Set up default audit logger (no-op when cfg.Audit.Path is empty)
	auditLogger, err := audit.NewFileLogger(cfg.Audit.Path)
	if err != nil {
		slog.Error("audit logger", "error", err)
		os.Exit(1)
	}
	if cfg.Audit.MaxSizeMB > 0 {
		auditLogger.MaxSizeMB = cfg.Audit.MaxSizeMB
	}
	if cfg.Audit.MaxRotatedFiles > 0 {
		auditLogger.MaxRotatedFiles = cfg.Audit.MaxRotatedFiles
	}
	defer auditLogger.Close()
	bus.SetAuditLogger(auditLogger)
	store.SetAuditLogger(auditLogger)

	// Storage gRPC service for sidecar module storage access
	storageGrpc := grpcmesh.NewStorageServer(store)
	storageGrpc.RegisterWithGRPC(grpcSrv)

	// --- Module capability enforcement ---
	// Discover call policy provider from the registry. Without one, all
	// inter-module mesh calls and storage operations are denied by default.
	if callPolicyEntries := reg.FindByCapability("call.policy"); len(callPolicyEntries) > 0 {
		if cp, ok := callPolicyEntries[0].Module.(contracts.CallPolicyProvider); ok {
			meshClient.SetCallPolicy(cp)
			storageGrpc.SetCallPolicy(cp)
			slog.Info("call policy provider loaded from registry",
				"module", callPolicyEntries[0].Info.ID)
		}
	}
	if meshClient.CallPolicy() == nil {
		slog.Warn("no call policy provider registered — all inter-module mesh calls and storage operations are denied until a call.policy module is deployed")
	}

	// Discover publish policy provider from the registry.
	if pubPolicyEntries := reg.FindByCapability("publish.policy"); len(pubPolicyEntries) > 0 {
		if pp, ok := pubPolicyEntries[0].Module.(contracts.PublishPolicyProvider); ok {
			bus.SetPublishPolicy(pp)
			slog.Info("publish policy provider loaded from registry",
				"module", pubPolicyEntries[0].Info.ID)
		}
	}
	if bus.PublishPolicy() == nil {
		slog.Info("no publish policy provider registered — event publication denied by default")
	}

	// Attach registry for sidecar module discovery queries
	discoveryGrpc.SetRegistry(reg)

	// Set up runtime module manager
	lifecycleMgr := modlifecycle.NewManager(reg, bus)
	lifecycleMgr.SetAuditLogger(auditLogger)
	modMgr := modulemgr.NewManager(cfg.GRPC.Addr, reg, lifecycleMgr)
	modMgr.RegisterModuleService(grpcSrv)

	// Prune the module binary cache: keep only the 3 most recent versions
	// per module to prevent unbounded disk growth on long-running deployments.
	if err := modMgr.PruneCache(3); err != nil {
		slog.Warn("module cache prune failed", "error", err)
	}

	// Fetch tag and spawn modules if --tag is specified
	if *tagName != "" {
		slog.Info("fetching tag from spool", "spool", *spoolURL, "tag", *tagName)
		tag, err := spool.FetchTag(*spoolURL, *tagName)
		if err != nil {
			slog.Error("fetch tag", "error", err)
			os.Exit(1)
		}
		slog.Info("tag loaded", "name", tag.Name, "version", tag.Version, "modules", len(tag.Modules))

		for _, tm := range tag.Modules {
			bin, err := modMgr.Resolve(tm.Repo, tm.Version)
			if err != nil {
				if tm.Required {
					slog.Error("resolve required module", "repo", tm.Repo, "error", err)
					os.Exit(1)
				}
				slog.Warn("resolve optional module failed, skipping", "repo", tm.Repo, "error", err)
				continue
			}
			// Verify binary checksum against spool tag (SLSA L3 provenance).
			// Skips verification when Checksum is empty (backward compat).
			if err := modMgr.VerifyChecksum(bin, tm.Checksum); err != nil {
				if tm.Required {
					slog.Error("checksum verification failed for required module",
						"repo", tm.Repo, "id", bin.ID, "error", err)
					os.Exit(1)
				}
				slog.Warn("checksum verification failed, skipping optional module",
					"repo", tm.Repo, "id", bin.ID, "error", err)
				continue
			}
			if err := modMgr.Spawn(ctx, bin); err != nil {
				if tm.Required {
					slog.Error("spawn required module", "id", bin.ID, "error", err)
					os.Exit(1)
				}
				slog.Warn("spawn optional module failed, skipping", "id", bin.ID, "error", err)
				continue
			}
		}
	}

	slog.Info("module registry ready", "count", reg.Count())

	// Core self-health probes. These must not publish events or write to the
	// audit log — probes run on every /health poll and would flood the WAL.
	startTime := time.Now()
	coreH := corehealth.New()
	coreH.RegisterProbe("event_bus", func(ctx context.Context) error {
		sc := bus.SubscriberCount()
		if sc < 0 {
			return fmt.Errorf("event bus not initialised")
		}
		if dc := bus.DroppedEvents(); dc > 0 {
			return fmt.Errorf("event bus dropped %d events", dc)
		}
		return nil
	})
	coreH.RegisterProbe("discovery", func(ctx context.Context) error {
		ms := discoveryGrpc.MembersSnapshot()
		if len(cfg.GRPC.SeedNodes) > 0 && len(ms) == 0 {
			return fmt.Errorf("seed nodes configured but no cluster peers discovered")
		}
		return nil
	})
	coreH.RegisterProbe("storage", func(ctx context.Context) error {
		pc := store.ProviderCount()
		if pc == 0 {
			return fmt.Errorf("no storage providers registered")
		}
		return nil
	})
	coreH.RegisterProbe("audit", func(ctx context.Context) error {
		cfgMu.Lock()
		auditPath := cfg.Audit.Path
		cfgMu.Unlock()
		if auditPath != "" {
			dir := filepath.Dir(auditPath)
			f, err := os.CreateTemp(dir, ".healthcheck-*")
			if err != nil {
				return fmt.Errorf("audit log directory not writable: %w", err)
			}
			f.Close()
			os.Remove(f.Name()) //nolint:gosec // path is from os.CreateTemp, internally controlled
		}
		return nil
	})

	srv.SetHealthChecker(func() map[string]error {
		results := make(map[string]error)
		for name, err := range coreH.Check(context.Background()) {
			results["core."+name] = err
		}
		for _, entry := range reg.ListAll() {
			if entry.State == contracts.ModuleStateDegraded {
				results[entry.Info.ID] = fmt.Errorf("module degraded")
			}
		}
		results["_uptime"] = fmt.Errorf("%s", time.Since(startTime).Round(time.Second))
		results["_version"] = fmt.Errorf("%s", version.String())
		return results
	})

	fatalErr := make(chan error, 1)

	go func() {
		if err := srv.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("api server", "error", err)
			fatalErr <- err
		}
	}()

	var lc net.ListenConfig
	grpcLis, err := lc.Listen(context.Background(), "tcp", cfg.GRPC.Addr)
	if err != nil {
		slog.Error("grpc listen", "error", err)
		os.Exit(1)
	}
	go func() {
		slog.Info("gRPC mesh listening", "addr", cfg.GRPC.Addr)
		if err := grpcSrv.Serve(grpcLis); err != nil {
			slog.Error("grpc server", "error", err)
		}
	}()

	// Auto-join seed nodes — requires TLS. Without TLS, auto-join is disabled
	// because join tokens travel in the request and must not be sent cleartext.
	if creds == nil {
		if len(cfg.GRPC.SeedNodes) > 0 {
			slog.Warn("auto-join skipped: TLS is required for cluster seed node connections")
		}
	} else if len(cfg.GRPC.SeedNodes) > 0 {
		slog.Info("auto-joining seed nodes", "seeds", cfg.GRPC.SeedNodes)
		localNode := discoveryGrpc.LocalNode()
		for _, seed := range cfg.GRPC.SeedNodes {
			go func(seedAddr string) {
				joinCtx, joinCancel := context.WithTimeout(ctx, 10*time.Second)
				defer joinCancel()
				dialOpts := []grpc.DialOption{grpc.WithTransportCredentials(creds)}
				conn, err := grpc.NewClient(seedAddr, dialOpts...)
				if err != nil {
					slog.Warn("auto-join: dial seed node", "seed", seedAddr, "error", err)
					return
				}
				defer conn.Close()
				client := discoveryv1.NewDiscoveryServiceClient(conn)
				resp, err := client.Join(joinCtx, &discoveryv1.JoinRequest{Node: localNode})
				if err != nil {
					slog.Warn("auto-join: join request failed", "seed", seedAddr, "error", err)
					return
				}
				slog.Info("auto-join: joined cluster via seed node",
					"seed", seedAddr,
					"cluster_id", resp.ClusterId,
					"leader_id", resp.LeaderId,
					"members", len(resp.Members),
				)
			}(seed)
		}
	}

	// Start heartbeat loop to advertise module list to cluster peers.
	// Uses same TLS config as seed node connections.
	// Insecure mode is only allowed when MUXCORE_INSECURE_DISABLE_TLS is explicitly set.
	var hbDialOpts []grpc.DialOption
	if creds != nil {
		hbDialOpts = append(hbDialOpts, grpc.WithTransportCredentials(creds))
	} else if insecureTLSCheck() {
		hbDialOpts = append(hbDialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		slog.Warn("heartbeat disabled: TLS is required for cluster heartbeat communication")
	}
	discoveryGrpc.StartHeartbeatLoop(ctx, hbDialOpts)

	slog.Info("MuxCore running", "addr", cfg.Server.Addr, "tag", *tagName)

	// Config hot-reload via SIGHUP.
	// Stops when ctx is cancelled (shutdown).
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-sighupCh:
			}
			slog.Info("SIGHUP received — reloading config", "path", configPath)
			result, err := config.Reload(cfg, configPath)
			if err != nil {
				slog.Error("config reload failed", "error", err)
				continue
			}

			changes := result.Changes
			if changes.LogLevel || changes.LogFormat {
				newLogger := setupLogger(result.Config.Log)
				slog.SetDefault(newLogger)
				slog.Info("logger reconfigured after reload",
					"level", result.Config.Log.Level,
					"format", result.Config.Log.Format,
				)
			}
			if changes.AuditPath {
				result.Config.Audit.Path = cfg.Audit.Path
				slog.Warn("audit.path changed — restart required for this to take effect",
					"current_path", cfg.Audit.Path,
					"new_path_on_restart", result.Config.Audit.Path,
				)
			}
			if changes.SeedNodes {
				slog.Info("seed nodes changed in config reload", "new_seeds", result.Config.GRPC.SeedNodes)
			}

			if changes.Unsafe {
				slog.Warn("config reload has unsafe changes — restart required",
					"unsafe_fields", changes.UnsafeFields,
				)
			}

			cfgMu.Lock()
			*cfg = *result.Config
			cfgMu.Unlock()

			// Publish config reload event so other subsystems can react.
			payload, _ := json.Marshal(map[string]any{
				"changes": changes,
			})
			bus.Publish(ctx, contracts.Event{
				Type:    "config.reloaded",
				Source:  "muxcored",
				Payload: payload,
			})
		}
	}()

	select {
	case <-ctx.Done():
	case err := <-fatalErr:
		slog.Error("fatal error, shutting down", "error", err)
		cancel()
	}
	slog.Info("shutting down...")

	// Phase 1: Stop SIGHUP handling to prevent config reload during teardown.
	signal.Stop(sighupCh)

	// Phase 2: Drain HTTP — stop accepting new connections, let in-flight
	// requests complete before modules are stopped beneath them.
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer drainCancel()
	srv.Drain(drainCtx)

	// Phase 3: GracefulStop gRPC — stop accepting new gRPC calls, let
	// in-flight calls complete before modules are stopped.
	grpcSrv.GracefulStop()

	// Phase 4: Shutdown timeout for remaining operations.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	// Phase 5: Stop watching for new modules (storage, registration).
	watchCancel()

	// Phase 6: Cancel all event bus subscriber workers.
	bus.Close()

	// Phase 7: Stop all module processes.
	if err := modMgr.StopAll(shutdownCtx); err != nil {
		slog.Error("module stop", "error", err)
	}

	// Phase 8: Close connection pool.
	connPool.Close()

	// Phase 9: Stop discovery eviction loop.
	discoveryGrpc.Close()

	// Phase 10: Final HTTP server shutdown.
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("api shutdown", "error", err)
	}

	slog.Info("MuxCore stopped.")
}

// grpcPanicRecoveryInterceptor catches panics in gRPC handlers and returns
// an Internal error instead of crashing the process.
func grpcPanicRecoveryInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (_ interface{}, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("gRPC panic recovered",
				"method", info.FullMethod,
				"error", rec,
				"stack", string(debug.Stack()),
			)
			err = status.Error(codes.Internal, "internal server error")
		}
	}()
	return handler(ctx, req)
}

// grpcPanicRecoveryStreamInterceptor catches panics in gRPC stream handlers.
func grpcPanicRecoveryStreamInterceptor(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (_ error) {
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("gRPC stream panic recovered",
				"method", info.FullMethod,
				"error", rec,
				"stack", string(debug.Stack()),
			)
		}
	}()
	return handler(srv, stream)
}

// grpcLoggingInterceptor logs every gRPC unary call with method, duration, and peer.
func grpcLoggingInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	start := time.Now()
	resp, err := handler(ctx, req)
	duration := time.Since(start)
	level := slog.LevelDebug
	if err != nil {
		level = slog.LevelWarn
	}
	slog.Log(ctx, level, "gRPC call",
		"method", info.FullMethod,
		"duration", duration,
		"error", err,
	)
	return resp, err
}

// grpcLoggingStreamInterceptor logs gRPC stream start.
func grpcLoggingStreamInterceptor(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	slog.Debug("gRPC stream started",
		"method", info.FullMethod,
		"is_server_stream", info.IsServerStream,
	)
	err := handler(srv, stream)
	if err != nil {
		slog.Warn("gRPC stream ended",
			"method", info.FullMethod,
			"error", err,
		)
	}
	return err
}

// insecureTLSCheck returns true if MUXCORE_INSECURE_DISABLE_TLS is set to true/1.
func insecureTLSCheck() bool {
	v := os.Getenv("MUXCORE_INSECURE_DISABLE_TLS")
	return v == "true" || v == "1"
}

func setupLogger(lc config.LogConfig) *slog.Logger {
	level := slog.LevelInfo
	switch lc.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if lc.Format == "json" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}
	return slog.New(handler)
}
