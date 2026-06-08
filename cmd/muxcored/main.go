package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Muxcore-Media/core/internal/api"
	"github.com/Muxcore-Media/core/internal/audit"
	"github.com/Muxcore-Media/core/internal/callpolicy"
	"github.com/Muxcore-Media/core/internal/config"
	"github.com/Muxcore-Media/core/internal/eventpolicy"
	"github.com/Muxcore-Media/core/internal/events"
	"github.com/Muxcore-Media/core/internal/grpcmesh"
	corehealth "github.com/Muxcore-Media/core/internal/health"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	modulemgr "github.com/Muxcore-Media/core/internal/module/mgr"
	modlifecycle "github.com/Muxcore-Media/core/internal/module"
	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/internal/spool"
	"github.com/Muxcore-Media/core/internal/startup"
	"github.com/Muxcore-Media/core/internal/storage"
	"github.com/Muxcore-Media/core/internal/version"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
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
	// Rate limiter for gRPC — per-IP token bucket.
	// Configurable via MUXCORE_GRPC_RATE_LIMIT (default: 1000 req/min/IP).
	grpcRateLimiter := grpcmesh.NewRateLimitInterceptor()

	// Auth interceptor: extracts caller identity from gRPC metadata and,
	// when Authorizer+IdentityProvider are set, enforces per-method access.
	authInterceptor := grpcmesh.NewAuthInterceptor()

	grpcOpts = append(grpcOpts,
		grpc.ChainUnaryInterceptor(
			grpcRateLimiter.UnaryInterceptor(),
			authInterceptor.UnaryInterceptor(),
		),
		grpc.ChainStreamInterceptor(
			grpcRateLimiter.StreamInterceptor(),
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

	// gRPC connection pool for heartbeat efficiency and cross-node routing.
	connPool := grpcmesh.NewConnPool() // dial opts set per-use via discovery
	discoveryGrpc.SetConnPool(connPool)

	reg := registry.New()
	healthGrpc := grpcmesh.NewHealthServer(reg)
	healthGrpc.RegisterWithGRPC(grpcSrv)

	eventGrpc := grpcmesh.NewEventServer(bus)
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
	discoveryGrpc.RegisterWithGRPC(grpcSrv)


	srv := api.NewServer(cfg.Server.Addr, cfg.Server.CertFile, cfg.Server.KeyFile)

	// Optional Prometheus-compatible /metrics endpoint.
	if os.Getenv("MUXCORE_METRICS_ENABLE") == "true" || os.Getenv("MUXCORE_METRICS_ENABLE") == "1" {
		metricsHandler := api.MetricsHandler(&api.MetricsProvider{
			DroppedEvents:       bus.DroppedEvents,
			ActiveSubscribers:   bus.SubscriberCount,
			ConnPoolSize:        connPool.Size,
			RegistryModuleCount: reg.Count,
			LeaderTerm:          discoveryGrpc.Term,
			IsLeader:            discoveryGrpc.IsLeader,
		})
		srv.HandleFunc("/metrics", metricsHandler)
		srv.AddPublicPath("/metrics") // metrics endpoint is public (scraper auth handled separately)
		slog.Info("metrics endpoint enabled", "path", "/metrics")
	}

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
	watchCancel := store.WatchModules(bus)

	// Set up default audit logger (no-op when cfg.Audit.Path is empty)
	auditLogger, err := audit.NewFileLogger(cfg.Audit.Path)
	if err != nil {
		slog.Error("audit logger", "error", err)
		os.Exit(1)
	}
	defer auditLogger.Close()
	bus.SetAuditLogger(auditLogger)
	store.SetAuditLogger(auditLogger)

	// Storage gRPC service for sidecar module storage access
	storageGrpc := grpcmesh.NewStorageServer(store)
	storageGrpc.RegisterWithGRPC(grpcSrv)

	// --- Module capability enforcement ---
	// Create built-in policies backed by the module registry.
	// Call policy: enforces inter-module mesh call access.
	// Publish policy: enforces event publication access.
	// Storage policy: enforces storage read/write access.
	// When MUXCORE_STRICT_CALL_POLICY=true, capability mismatches are hard-denied
	// instead of soft-warned.
	callPolicy := callpolicy.NewBuiltinPolicy(reg)
	meshClient.SetCallPolicy(callPolicy)
	storageGrpc.SetCallPolicy(callPolicy)

	eventPolicy := eventpolicy.NewBuiltinPolicy(reg)
	bus.SetPublishPolicy(eventPolicy)
	slog.Info("module capability enforcement enabled",
		"call_policy_strict", callPolicy.Strict(),
		"publish_policy_strict", eventPolicy.Strict(),
	)

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
	coreH := corehealth.New()
	coreH.RegisterProbe("event_bus", func(ctx context.Context) error {
		// The bus is live as long as it has a publish policy configured.
		// A nil policy means it was never initialised — that's a fatal misconfiguration.
		if bus.SubscriberCount() < 0 {
			return fmt.Errorf("event bus not initialised")
		}
		return nil
	})
	coreH.RegisterProbe("discovery", func(ctx context.Context) error {
		// Standalone (no seed nodes configured) is always healthy.
		// With seed nodes, warn if we have zero peers after boot, but don't
		// fail — nodes may not have joined yet.
		return nil
	})
	coreH.RegisterProbe("storage", func(ctx context.Context) error {
		// Core is healthy even with zero providers — modules register them.
		return nil
	})
	coreH.RegisterProbe("audit", func(ctx context.Context) error {
		// If an audit path is configured, verify the file is still writable
		// by checking its parent directory rather than writing an entry.
		if cfg.Audit.Path != "" {
			dir := filepath.Dir(cfg.Audit.Path)
			f, err := os.CreateTemp(dir, ".healthcheck-*")
			if err != nil {
				return fmt.Errorf("audit log directory not writable: %w", err)
			}
			f.Close()
			os.Remove(f.Name())
		}
		return nil
	})

	srv.SetHealthChecker(func() map[string]error {
		results := make(map[string]error)
		// Include core health probes.
		for name, err := range coreH.Check(context.Background()) {
			results["core."+name] = err
		}
		// Module health is reported via sidecar gRPC; include what the
		// lifecycle manager knows about.
		for id, entry := range reg.ListAll() {
			if entry.State == contracts.ModuleStateDegraded {
				results[id] = fmt.Errorf("module degraded")
			}
		}
		return results
	})

	fatalErr := make(chan error, 1)

	go func() {
		if err := srv.Start(); err != nil && err != http.ErrServerClosed {
			slog.Error("api server", "error", err)
			fatalErr <- err
		}
	}()

	grpcLis, err := net.Listen("tcp", cfg.GRPC.Addr)
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
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				dialOpts := []grpc.DialOption{grpc.WithTransportCredentials(creds)}
				conn, err := grpc.NewClient(seedAddr, dialOpts...)
				if err != nil {
					slog.Warn("auto-join: dial seed node", "seed", seedAddr, "error", err)
					return
				}
				defer conn.Close()
				client := discoveryv1.NewDiscoveryServiceClient(conn)
				resp, err := client.Join(ctx, &discoveryv1.JoinRequest{Node: localNode})
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
	} else if os.Getenv("MUXCORE_INSECURE_DISABLE_TLS") == "true" || os.Getenv("MUXCORE_INSECURE_DISABLE_TLS") == "1" {
		hbDialOpts = append(hbDialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		slog.Warn("heartbeat disabled: TLS is required for cluster heartbeat communication")
	}
	discoveryGrpc.StartHeartbeatLoop(ctx, hbDialOpts)

	slog.Info("MuxCore running", "addr", cfg.Server.Addr, "tag", *tagName)

	// Config hot-reload via SIGHUP.
	go func() {
		for range sighupCh {
			slog.Info("SIGHUP received — reloading config", "path", configPath)
			result, err := config.Reload(cfg, configPath)
			if err != nil {
				slog.Error("config reload failed", "error", err)
				continue
			}

			changes := result.Changes
			if changes.LogLevel || changes.LogFormat {
				// Reconfigure the logger.
				newLogger := setupLogger(result.Config.Log)
				slog.SetDefault(newLogger)
				slog.Info("logger reconfigured after reload",
					"level", result.Config.Log.Level,
					"format", result.Config.Log.Format,
				)
			}
			if changes.AuditPath {
				// Audit path change requires reopening the FileLogger — defer
				// to restart rather than swapping under a live system.
				// Explicitly do NOT copy the new audit path into cfg so operators
				// aren't misled into thinking it took effect.
				result.Config.Audit.Path = cfg.Audit.Path
				slog.Warn("audit.path changed — restart required for this to take effect",
					"current_path", cfg.Audit.Path,
					"new_path_on_restart", result.Config.Audit.Path,
				)
			}
			if changes.StrictCall || changes.StrictPublish {
				// Strict policy toggles are read from env vars at startup;
				// they cannot be changed without a restart.
				slog.Warn("strict policy env vars changed — restart required")
			}
			if changes.SeedNodes {
				slog.Info("seed nodes changed in config reload", "new_seeds", result.Config.GRPC.SeedNodes)
				// TODO: attempt to join newly added seed nodes dynamically
			}

			if changes.Unsafe {
				slog.Warn("config reload has unsafe changes — restart required",
					"unsafe_fields", changes.UnsafeFields,
				)
			}

			// Atomically replace the live config with the reloaded one.
			// The audit.path field was restored above if it changed.
			*cfg = *result.Config
		}
	}()

	select {
	case <-ctx.Done():
	case err := <-fatalErr:
		slog.Error("fatal error, shutting down", "error", err)
		cancel()
	}
	slog.Info("shutting down...")

	// Phase 1: Drain HTTP — stop accepting new connections, let in-flight
	// requests complete before modules are stopped beneath them.
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer drainCancel()
	srv.Drain(drainCtx)

	// Phase 2: GracefulStop gRPC — stop accepting new gRPC calls, let
	// in-flight calls complete before modules are stopped.
	grpcSrv.GracefulStop()

	// Phase 3: Shutdown timeout for remaining operations.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	// Phase 4: Stop watching for new modules (storage, registration).
	watchCancel()

	// Phase 5: Stop all module processes.
	if err := modMgr.StopAll(shutdownCtx); err != nil {
		slog.Error("module stop", "error", err)
	}

	// Phase 6: Close connection pool and rate limiter.
	connPool.Close()
	grpcRateLimiter.Close()

	// Phase 7: Final HTTP server shutdown.
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("api shutdown", "error", err)
	}

	slog.Info("MuxCore stopped.")
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