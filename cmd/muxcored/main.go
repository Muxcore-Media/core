package main

import (
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/Muxcore-Media/core/internal/bootstrap"
	"github.com/Muxcore-Media/core/internal/grpcmesh"
	modlifecycle "github.com/Muxcore-Media/core/internal/module"
	"github.com/Muxcore-Media/core/internal/spool"
	"github.com/Muxcore-Media/core/internal/startup"
	"github.com/Muxcore-Media/core/internal/version"
	"github.com/Muxcore-Media/core/internal/workerpool"
	"github.com/Muxcore-Media/core/pkg/sys"
	"google.golang.org/grpc"
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
	sys.SetGOMAXPROCSFromCgroup()
	tagName, spoolURL, watchdogPath, taskDir, idempotencyDir, deadletterDir, printVersion, dryRun := parseFlags()

	if *printVersion {
		fmt.Println(version.String())
		os.Exit(0)
	}

	if *tagName != "" {
		fmt.Fprintln(os.Stderr, securityDisclaimer)
	}

	cfg, configPath := loadConfig()

	ctx, cancel, sighupCh := setupContext()
	defer cancel()

	maxMsgBytes := cfg.GRPC.MaxMessageSizeMB * 1024 * 1024
	if maxMsgBytes <= 0 {
		maxMsgBytes = 32 * 1024 * 1024
	}
	var cfgMu sync.Mutex

	logger := bootstrap.SetupLogger(cfg.Log)
	slog.SetDefault(logger)
	slog.Info("MuxCore starting...", "version", version.String())

	// ADR-0016: resolve the security profile before anything listens.
	prof, err := resolveProfile()
	if err != nil {
		slog.Error("security profile", "error", err)
		os.Exit(1)
	}

	if len(cfg.Spool.AllowedHosts) > 0 {
		spool.SetAllowedHosts(cfg.Spool.AllowedHosts)
		slog.Info("spool host allow-list enabled", "hosts", cfg.Spool.AllowedHosts)
	}

	startupResults := startup.RunAll(cfg, configPath)
	if startup.HasFatal(startupResults) {
		for _, err := range startup.FatalErrors(startupResults) {
			slog.Error("startup check failed", "error", err)
		}
		os.Exit(1)
	}

	if *dryRun {
		runDryRun(ctx, *tagName, *spoolURL)
	}

	bus := initEventBus()

	grpcSrv, meshClient, discoveryGrpc, connPool, reg, sec, authInterceptor, nodeID, cluster := initGRPCMesh(ctx, cfg, bus, prof)
	creds := sec.serverCreds
	sidecar := sec.sidecar

	store, watchCancel := initStorage(ctx, cfg, reg, bus, sidecar, maxMsgBytes)

	workerPool := workerpool.New(nodeID)
	if *taskDir != "" {
		taskStore, err := workerpool.NewFileStore(*taskDir)
		if err != nil {
			slog.Error("workerpool: create task store", "error", err)
			os.Exit(1)
		}
		workerPool.SetStore(taskStore)
		slog.Info("workerpool: persistent task storage enabled", "dir", *taskDir)
	}
	workerpool.RegisterSelf(reg, workerPool, nodeID)
	workerPool.Start(ctx)
	slog.Info("worker pool initialized")

	disp := workerpool.NewDispatcher(workerPool, reg, meshClient)
	disp.SetNodeResolver(workerpool.NewClusterNodeResolver(cluster))
	disp.Start(ctx)
	slog.Info("task dispatcher started")

	_ = initIdempotency(ctx, reg, nodeID, *idempotencyDir)

	initDeadLetter(ctx, reg, nodeID, *deadletterDir)
	initRetry(reg, nodeID)
	initEventStore(reg, nodeID)

	srv, metricsProvider := initHTTPServer(cfg, reg, bus, store, meshClient, discoveryGrpc, connPool, workerPool)
	srv.SetProfile(string(prof.Name), prof.Insecure)

	auditLogger := initAudit(cfg, bus, store, srv)
	defer func() { _ = auditLogger.Close() }()

	storageGrpc := grpcmesh.NewStorageServer(store)
	storageGrpc.RegisterWithGRPC(grpcSrv)

	wirePoliciesAndAuth(reg, meshClient, storageGrpc, bus, srv, authInterceptor, sidecar, maxMsgBytes)

	discoveryGrpc.SetRegistry(reg)

	modMgr, lifecycleMgr := initModuleManager(cfg, reg, bus, auditLogger, grpcSrv, metricsProvider, *watchdogPath, authInterceptor, sec.certAuth, prof)
	modMgr.PostRegisterHook = func(moduleID string, caps []string) {
		_ = bootstrap.WireCallPolicy(reg, meshClient, storageGrpc, sidecar, maxMsgBytes)
		_ = bootstrap.WirePublishPolicy(reg, bus, sidecar, maxMsgBytes)
		_ = bootstrap.WireAuth(reg, srv, authInterceptor, sidecar, maxMsgBytes)
	}

	lifecycleMgr.SetRestarter(modMgr)
	lifecycleMgr.StartHealthCheckLoop(ctx, modlifecycle.DefaultHealthCheckInterval)
	slog.Info("health check scheduler started")

	registerManagementGRPC(grpcSrv, *spoolURL, cfg, &cfgMu, modMgr, reg, nodeID, discoveryGrpc, auditLogger)

	bootstrap.RunClusterEventListener(ctx, cluster, nodeID, workerPool, modMgr)

	var peerAddrs []string
	if len(cfg.GRPC.SeedNodes) > 0 {
		peerAddrs = append(peerAddrs, cfg.GRPC.SeedNodes...)
	}
	if err := bootstrap.LoadAndSpawnModules(ctx, *tagName, *spoolURL, modMgr, peerAddrs); err != nil {
		slog.Error("load and spawn modules", "error", err)
		os.Exit(1)
	}

	if err := lifecycleMgr.InitAll(ctx); err != nil {
		slog.Error("module init", "error", err)
	}
	if err := lifecycleMgr.StartAll(ctx); err != nil {
		slog.Error("module start", "error", err)
	}

	slog.Info("module registry ready", "count", reg.Count())

	srv.SetHealthChecker(bootstrap.InitHealthProbes(ctx, bus, discoveryGrpc, store, cfg, reg, &cfgMu))

	fatalErr := startHTTPAndGRPC(cfg, srv, grpcSrv)

	waitForSidecarPolicies(reg, meshClient, storageGrpc, bus, srv, authInterceptor, sidecar, maxMsgBytes)

	bootstrap.AutoJoinSeedNodes(ctx, cfg, creds, discoveryGrpc)

	msgSizeOpts := []grpc.DialOption{
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(maxMsgBytes),
			grpc.MaxCallSendMsgSize(maxMsgBytes),
		),
	}
	var hbDialOpts []grpc.DialOption
	hbDialOpts = append(hbDialOpts, msgSizeOpts...)
	if creds != nil {
		hbDialOpts = append(hbDialOpts, grpc.WithTransportCredentials(creds))
		discoveryGrpc.StartHeartbeatLoop(ctx, hbDialOpts)
	} else {
		slog.Warn("heartbeat disabled: TLS is required for cluster heartbeat communication")
	}

	slog.Info("MuxCore running", "addr", cfg.Server.Addr, "tag", *tagName)

	bootstrap.RunConfigReloadLoop(ctx, sighupCh, configPath, cfg, &cfgMu, bus)

	awaitAndShutdown(ctx, cancel, sighupCh, fatalErr, srv, grpcSrv, watchCancel, workerPool, bus, modMgr, connPool, discoveryGrpc)
}
