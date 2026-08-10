package main

import (
	"context"
	"expvar"
	"flag"
	"fmt"
	"log/slog"
	"net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/Muxcore-Media/core/internal/api"
	"github.com/Muxcore-Media/core/internal/audit"
	"github.com/Muxcore-Media/core/internal/bootstrap"
	"github.com/Muxcore-Media/core/internal/config"
	"github.com/Muxcore-Media/core/internal/deadletter"
	"github.com/Muxcore-Media/core/internal/events"
	"github.com/Muxcore-Media/core/internal/eventstore"
	"github.com/Muxcore-Media/core/internal/grpcmesh"
	"github.com/Muxcore-Media/core/internal/idempotency"
	modlifecycle "github.com/Muxcore-Media/core/internal/module"
	modulemgr "github.com/Muxcore-Media/core/internal/module/mgr"
	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/internal/retry"
	"github.com/Muxcore-Media/core/internal/spool"
	"github.com/Muxcore-Media/core/internal/startup"
	"github.com/Muxcore-Media/core/internal/storage"
	localstorage "github.com/Muxcore-Media/core/internal/storage/local"
	"github.com/Muxcore-Media/core/internal/version"
	"github.com/Muxcore-Media/core/internal/workerpool"
	"github.com/Muxcore-Media/core/pkg/contracts"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
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

	grpcSrv, meshClient, discoveryGrpc, connPool, reg, creds, authInterceptor, nodeID, cluster, certAuth := initGRPCMesh(ctx, cfg, bus)

	store, watchCancel := initStorage(ctx, cfg, reg, bus)

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

	workerpool.NewDispatcher(workerPool, reg, meshClient).Start(ctx)
	slog.Info("task dispatcher started")

	_ = initIdempotency(ctx, reg, nodeID, *idempotencyDir)

	initDeadLetter(ctx, reg, nodeID, *deadletterDir)
	initRetry(reg, nodeID)
	initEventStore(reg, nodeID)

	srv, metricsProvider := initHTTPServer(cfg, reg, bus, store, meshClient, discoveryGrpc, connPool, workerPool)

	auditLogger := initAudit(cfg, bus, store, srv)
	defer func() { _ = auditLogger.Close() }()

	storageGrpc := grpcmesh.NewStorageServer(store)
	storageGrpc.RegisterWithGRPC(grpcSrv)

	wirePoliciesAndAuth(reg, meshClient, storageGrpc, bus, srv, authInterceptor, creds, maxMsgBytes)

	discoveryGrpc.SetRegistry(reg)

	modMgr, lifecycleMgr := initModuleManager(cfg, reg, bus, auditLogger, grpcSrv, metricsProvider, *watchdogPath, authInterceptor, certAuth)
	modMgr.PostRegisterHook = func(moduleID string, caps []string) {
		_ = bootstrap.WireCallPolicy(reg, meshClient, storageGrpc, creds, maxMsgBytes)
		_ = bootstrap.WirePublishPolicy(reg, bus, creds, maxMsgBytes)
		_ = bootstrap.WireAuth(reg, srv, authInterceptor, creds, maxMsgBytes)
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

	waitForSidecarPolicies(reg, meshClient, storageGrpc, bus, srv, authInterceptor, creds, maxMsgBytes)

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

func parseFlags() (tagName, spoolURL, watchdogPath, taskDir, idempotencyDir, deadletterDir *string, printVersion, dryRun *bool) { //nolint:gocritic // too many results for flag parsing
	tagName = flag.String("tag", "", "Module tag to load from the spool (e.g., 'default')")
	spoolURL = flag.String("spool", spool.DefaultSpoolURL, "Spool URL to fetch tags from")
	watchdogPath = flag.String("watchdog-path", "", "Path to the muxcore-watchdog binary (for module failover)")
	taskDir = flag.String("task-dir", "", "Directory for persistent task storage (enables task recovery across restarts)")
	idempotencyDir = flag.String("idempotency-dir", "", "Directory for persistent idempotency key storage (enables dedup across restarts)")
	deadletterDir = flag.String("deadletter-dir", "", "Directory for persistent dead letter event storage (enables replay across restarts)")
	printVersion = flag.Bool("version", false, "Print version and exit")
	dryRun = flag.Bool("dry-run", false, "Validate config, TLS certs, and spool connectivity then exit 0 on success")
	flag.Parse()
	return
}

func loadConfig() (*config.Config, string) {
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
	return cfg, configPath
}

func setupContext() (context.Context, context.CancelFunc, chan os.Signal) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	sighupCh := make(chan os.Signal, 1)
	signal.Notify(sighupCh, syscall.SIGHUP)
	return ctx, cancel, sighupCh
}

func initIdempotency(ctx context.Context, reg *registry.Registry, nodeID, dir string) *idempotency.Store {
	var s *idempotency.Store
	var err error
	if dir != "" {
		s, err = idempotency.NewWithDir(dir)
		if err != nil {
			slog.Error("idempotency: create file store", "error", err)
			os.Exit(1)
		}
		slog.Info("idempotency: persistent storage enabled", "dir", dir)
	} else {
		s = idempotency.New()
	}
	idempotency.RegisterSelf(reg, s, nodeID)
	s.Start(ctx)
	slog.Info("idempotency store initialized")
	return s
}

func initDeadLetter(ctx context.Context, reg *registry.Registry, nodeID, dir string) *deadletter.Store {
	var s *deadletter.Store
	var err error
	if dir != "" {
		s, err = deadletter.NewWithDir(dir)
		if err != nil {
			slog.Error("deadletter: create file store", "error", err)
			os.Exit(1)
		}
		slog.Info("deadletter: persistent storage enabled", "dir", dir)
	} else {
		s = deadletter.New()
	}
	deadletter.RegisterSelf(reg, s, nodeID)
	slog.Info("dead letter store initialized")
	return s
}

func initEventStore(reg *registry.Registry, nodeID string) *eventstore.Store {
	s := eventstore.New()
	eventstore.RegisterSelf(reg, s, nodeID)
	slog.Info("event store initialized")
	return s
}

func initRetry(reg *registry.Registry, nodeID string) *retry.Provider {
	p := retry.NewProvider()
	retry.RegisterSelf(reg, p, nodeID)
	slog.Info("retry provider initialized")
	return p
}

func initEventBus() *events.MemoryBus {
	bus := events.NewMemoryBus()
	if journalPath := os.Getenv("MUXCORE_EVENT_JOURNAL_PATH"); journalPath != "" {
		if err := bus.EnableWAL(journalPath); err != nil {
			slog.Error("enable WAL", "path", journalPath, "error", err)
			os.Exit(1)
		}
	}
	slog.Info("event bus ready", "type", "memory")
	return bus
}

func initGRPCMesh(ctx context.Context, cfg *config.Config, bus *events.MemoryBus) (grpcSrv *grpc.Server, meshClient *grpcmesh.Client, discoveryGrpc *grpcmesh.DiscoveryServer, connPool *grpcmesh.ConnPool, reg *registry.Registry, creds credentials.TransportCredentials, authInterceptor *grpcmesh.AuthInterceptor, nodeID string, cluster contracts.Cluster, certAuth *grpcmesh.CertAuthority) { //nolint:gocritic // too many results for gRPC mesh initialization
	meshSrv := grpcmesh.NewServer()
	meshClient = grpcmesh.NewClient(meshSrv)
	slog.Info("gRPC mesh server created")

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

	if cfg.GRPC.MTLSEnabled || cfg.GRPC.CACertDir != "" {
		caDir := cfg.GRPC.CACertDir
		if caDir == "" {
			home, _ := os.UserHomeDir()
			if home == "" {
				home = "/tmp"
			}
			caDir = filepath.Join(home, ".muxcore", "ca")
		}
		var caErr error
		certAuth, caErr = grpcmesh.NewCertAuthority(caDir)
		if caErr != nil {
			slog.Error("certificate authority", "error", caErr)
			os.Exit(1)
		}
		if creds == nil && cfg.GRPC.MTLSEnabled {
			mTLSCreds := grpcmesh.MTLSConfig(certAuth)
			if mTLSCreds != nil {
				creds = credentials.NewTLS(mTLSCreds)
			}
		}
		slog.Info("certificate authority ready", "dir", caDir)
	}

	authInterceptor = grpcmesh.NewAuthInterceptor()

	grpcOpts = append(grpcOpts,
		grpc.ChainUnaryInterceptor(
			bootstrap.GRPCLoggingInterceptor,
			bootstrap.GRPCPanicRecoveryInterceptor,
			authInterceptor.UnaryInterceptor(),
		),
		grpc.ChainStreamInterceptor(
			bootstrap.GRPCLoggingStreamInterceptor,
			bootstrap.GRPCPanicRecoveryStreamInterceptor,
			authInterceptor.StreamInterceptor(), //nolint:contextcheck // gRPC interceptor factory — context is derived from stream
		),
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

	grpcSrv = grpc.NewServer(grpcOpts...)
	meshSrv.RegisterWithGRPC(grpcSrv)
	meshClient.SetTransportCredentials(creds)

	reg = registry.New()
	slog.Info("module registry created")

	healthGrpc := grpcmesh.NewHealthServer(reg)
	healthGrpc.RegisterWithGRPC(grpcSrv)
	grpcmesh.RegisterStandardHealthProbe(grpcSrv)
	slog.Info("standard gRPC health probe registered")

	eventGrpc := grpcmesh.NewEventServer(bus)
	if os.Getenv("MUXCORE_EVENT_JOURNAL_PATH") != "" {
		eventGrpc.SetWALReplayer(bus)
	}
	eventGrpc.RegisterWithGRPC(grpcSrv)

	nodeID = "muxcore-" + cfg.GRPC.Addr
	discoveryGrpc = grpcmesh.NewDiscoveryServer(
		nodeID,
		cfg.GRPC.Addr,
		cfg.Server.Addr,
		cfg.GRPC.JoinToken,
		func() ([]string, map[string]string) {
			entries := reg.List()
			ids := make([]string, len(entries))
			health := make(map[string]string, len(entries))
			for i, e := range entries {
				ids[i] = e.Info.ID
				if e.Health != nil {
					health[e.Info.ID] = e.Health.Error()
				}
			}
			return ids, health
		},
	)
	discoveryGrpc.RegisterWithGRPC(grpcSrv)
	slog.Info("discovery server created", "node_id", nodeID, "grpc_addr", cfg.GRPC.Addr)

	cluster = grpcmesh.NewCluster(discoveryGrpc)
	meshClient.SetCluster(cluster)
	meshClient.SetLBStrategy(grpcmesh.NewRoundRobinStrategy())
	_ = cluster.Start(ctx)
	slog.Info("cluster adapter started")

	connPool = grpcmesh.NewConnPool()
	discoveryGrpc.SetConnPool(connPool)
	slog.Info("gRPC connection pool created")

	return
}

func initStorage(ctx context.Context, cfg *config.Config, reg *registry.Registry, bus *events.MemoryBus) (*storage.Orchestrator, context.CancelFunc) {
	store := storage.NewOrchestrator(reg)
	store.SetTimeouts(storage.Timeouts{
		Read:   time.Duration(cfg.Storage.ReadTimeoutSeconds) * time.Second,
		Write:  time.Duration(cfg.Storage.WriteTimeoutSeconds) * time.Second,
		Delete: time.Duration(cfg.Storage.DeleteTimeoutSeconds) * time.Second,
	})
	if err := store.DiscoverStorage(); err != nil {
		slog.Warn("storage discover", "error", err)
	}
	if storageDir := os.Getenv("MUXCORE_STORAGE_DIR"); storageDir != "" {
		localProvider, err := localstorage.New(storageDir)
		if err != nil {
			slog.Error("local storage provider", "dir", storageDir, "error", err)
			os.Exit(1)
		}
		store.SetProvider("storage.local", localProvider)
		slog.Info("local storage provider registered", "dir", storageDir)
	}
	store.DiscoverTiers()
	store.DiscoverCache()
	slog.Info("storage orchestrator ready", "providers", store.ProviderCount())
	watchCancel := store.WatchModules(ctx, bus)
	return store, watchCancel
}

func initHTTPServer(cfg *config.Config, reg *registry.Registry, bus *events.MemoryBus, store *storage.Orchestrator, meshClient *grpcmesh.Client, discoveryGrpc *grpcmesh.DiscoveryServer, connPool *grpcmesh.ConnPool, workerPool *workerpool.Pool) (*api.Server, *api.MetricsProvider) {
	srv := api.NewServer(cfg.Server.Addr, cfg.Server.CertFile, cfg.Server.KeyFile)

	var metricsProvider *api.MetricsProvider
	if os.Getenv("MUXCORE_METRICS_ENABLE") == "true" || os.Getenv("MUXCORE_METRICS_ENABLE") == "1" {
		metricsProvider = &api.MetricsProvider{
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
			AuthFailureCount:   srv.AuthFailuresTotalCount,
			BackoffActiveCount: srv.BackoffActiveCount,
			MeshCallCount:      meshClient.CallCount,
			PublishCount:       bus.PublishCount,
			RequestCount:       srv.RequestCount,
			StoragePutCount:    store.PutCount,
			StorageCount:       store.Count,
			StorageDeleteCount: store.DeleteCount,
			StatusHTTP2xx:      srv.StatusHTTP2xx,
			StatusHTTP4xx:      srv.StatusHTTP4xx,
			StatusHTTP5xx:      srv.StatusHTTP5xx,
		}
		srv.HandleFunc("/metrics", api.MetricsHandler(metricsProvider))
		srv.AddPublicPath("/metrics")
		slog.Warn("metrics endpoint enabled at /metrics — no authentication; do not expose to the internet")
	}

	if os.Getenv("MUXCORE_DEBUG_ENABLE") == "true" || os.Getenv("MUXCORE_DEBUG_ENABLE") == "1" {
		srv.HandleFunc("/debug/pprof/", pprof.Index)
		srv.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		srv.HandleFunc("/debug/pprof/profile", pprof.Profile)
		srv.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		srv.HandleFunc("/debug/pprof/trace", pprof.Trace)
		srv.Handle("/debug/vars", expvar.Handler())
		slog.Warn("debug profiling endpoints enabled at /debug/pprof/ — no authentication; do not expose to the internet")
		slog.Warn("debug runtime variables at /debug/vars — no authentication; do not expose to the internet")
		srv.AddPublicPath("/debug/vars")
	}

	if workerPool != nil {
		taskHandlers := api.NewTaskHandlers(workerPool)
		taskHandlers.RegisterRoutes(srv)
		slog.Info("task management API enabled", "base", "/api/v1/tasks")
	}

	return srv, metricsProvider
}

func initAudit(cfg *config.Config, bus *events.MemoryBus, store *storage.Orchestrator, srv *api.Server) *audit.FileLogger {
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
	bus.SetAuditLogger(auditLogger)
	store.SetAuditLogger(auditLogger)
	srv.SetAuditLogger(auditLogger)
	return auditLogger
}

func initModuleManager(cfg *config.Config, reg *registry.Registry, bus *events.MemoryBus, auditLogger *audit.FileLogger, grpcSrv *grpc.Server, metricsProvider *api.MetricsProvider, watchdogPath string, authInterceptor *grpcmesh.AuthInterceptor, certAuth *grpcmesh.CertAuthority) (*modulemgr.Manager, *modlifecycle.Manager) {
	lifecycleMgr := modlifecycle.NewManager(reg, bus)
	lifecycleMgr.SetAuditLogger(auditLogger)
	modMgr := modulemgr.NewManager(cfg.GRPC.Addr, reg, lifecycleMgr)
	modMgr.RegisterModuleService(grpcSrv)
	if watchdogPath != "" {
		modMgr.SetWatchdogPath(watchdogPath)
		slog.Info("module watchdog enabled", "path", watchdogPath)
	}

	if metricsProvider != nil {
		metricsProvider.ModuleSpawnCount = modMgr.SpawnCount
		metricsProvider.ModuleRestartCount = modMgr.RestartCount
		metricsProvider.ModuleResolveCount = modMgr.ResolveCount
	}

	if len(cfg.Spool.AllowedHosts) > 0 {
		modMgr.SetAllowedRepoHosts(cfg.Spool.AllowedHosts)
	}

	if certAuth != nil {
		modMgr.SetCertAuthority(certAuth)
	}

	if err := modMgr.PruneCache(3); err != nil {
		slog.Warn("module cache prune failed", "error", err)
	}

	return modMgr, lifecycleMgr
}
