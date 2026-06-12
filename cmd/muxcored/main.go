package main

import (
	"context"
	"errors"
	"expvar"
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

func main() { //nolint:gocyclo // main initialization has many unavoidable branches
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
		slog.Info("dry-run: config and startup checks passed")
		if *tagName != "" {
			slog.Info("dry-run: fetching spool tag to verify connectivity", "spool", *spoolURL, "tag", *tagName)
			tag, err := spool.FetchTag(ctx, *spoolURL, *tagName)
			if err != nil {
				slog.Error("dry-run: spool connectivity failed", "error", err)
				os.Exit(1)
			}
			slog.Info("dry-run: spool OK", "tag", tag.Name, "modules", len(tag.Modules))
		}
		slog.Info("dry-run: all checks passed — ready to start")
		os.Exit(0)
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
	defer auditLogger.Close()

	storageGrpc := grpcmesh.NewStorageServer(store)
	storageGrpc.RegisterWithGRPC(grpcSrv)

	if err := bootstrap.WireCallPolicy(reg, meshClient, storageGrpc, creds, maxMsgBytes); err != nil {
		slog.Warn("call policy setup", "error", err)
	}
	if meshClient.CallPolicy() == nil {
		slog.Warn("no call policy provider registered — all inter-module mesh calls and storage operations are denied until a call.policy module is deployed")
	}

	if err := bootstrap.WirePublishPolicy(reg, bus, creds, maxMsgBytes); err != nil {
		slog.Warn("publish policy setup", "error", err)
	}
	if bus.PublishPolicy() == nil {
		slog.Info("no publish policy provider registered — event publication denied by default")
	}

	encryptionEntries := reg.FindByCapability("encryption")
	if len(encryptionEntries) == 0 {
		if !bootstrap.DevTLSSkipCheck() {
			slog.Warn("no encryption provider registered — data at rest will not be encrypted. Deploy an encryption module or set MUXCORE_DEV_TLS_SKIP for development")
		} else {
			slog.Info("no encryption provider registered — data at rest is not encrypted (development mode)")
		}
	} else {
		if ep, ok := encryptionEntries[0].Module.(contracts.EncryptionProvider); ok {
			if ep.Available() {
				slog.Info("encryption provider loaded from registry",
					"module", encryptionEntries[0].Info.ID)
			} else {
				slog.Warn("encryption provider registered but unavailable",
					"module", encryptionEntries[0].Info.ID)
			}
		}
	}

	if err := bootstrap.WireAuth(reg, srv, authInterceptor, creds, maxMsgBytes); err != nil {
		slog.Warn("auth setup", "error", err)
	}
	if len(reg.FindByCapability(contracts.CapabilityAuthorizer)) == 0 {
		slog.Error("no authorizer registered — HTTP API requests requiring authorization will be denied until an authorizer module is deployed")
	}
	if len(reg.FindByCapability(contracts.CapabilityAuth)) == 0 {
		slog.Info("no auth provider registered — HTTP API has no authentication. All requests except /health and public paths will be rejected")
	}

	discoveryGrpc.SetRegistry(reg)

	modMgr, lifecycleMgr := initModuleManager(cfg, reg, bus, auditLogger, grpcSrv, metricsProvider, *watchdogPath, authInterceptor, certAuth)
	modMgr.PostRegisterHook = func(moduleID string, caps []string) {
		bootstrap.WireCallPolicy(reg, meshClient, storageGrpc, creds, maxMsgBytes)
		bootstrap.WirePublishPolicy(reg, bus, creds, maxMsgBytes)
		bootstrap.WireAuth(reg, srv, authInterceptor, creds, maxMsgBytes)
	}

	lifecycleMgr.SetRestarter(modMgr)
	lifecycleMgr.StartHealthCheckLoop(ctx, modlifecycle.DefaultHealthCheckInterval)
	slog.Info("health check scheduler started")

	spoolGrpc := grpcmesh.NewSpoolServer(*spoolURL, cfg, &cfgMu, modMgr, reg)
	spoolGrpc.RegisterWithGRPC(grpcSrv)
	slog.Info("spool management gRPC service registered")

	lifecycleGrpc := grpcmesh.NewLifecycleServer(reg, modMgr, nodeID, discoveryGrpc)
	lifecycleGrpc.RegisterWithGRPC(grpcSrv)
	slog.Info("module lifecycle gRPC service registered")

	auditGrpc := grpcmesh.NewAuditServer(auditLogger)
	auditGrpc.RegisterWithGRPC(grpcSrv)
	slog.Info("audit gRPC service registered")

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

	fatalErr := make(chan error, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("api server panic recovered", "panic", r)
				fatalErr <- fmt.Errorf("api server panic: %v", r)
			}
		}()
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
		defer func() {
			if r := recover(); r != nil {
				slog.Error("grpc server panic recovered", "panic", r)
			}
		}()
		slog.Info("gRPC mesh listening", "addr", cfg.GRPC.Addr)
		if err := grpcSrv.Serve(grpcLis); err != nil {
			slog.Error("grpc server", "error", err)
		}
	}()

	for _, discover := range []struct {
		name string
		fn   func() error
	}{
		{"call.policy", func() error { return bootstrap.WireCallPolicy(reg, meshClient, storageGrpc, creds, maxMsgBytes) }},
		{"publish.policy", func() error { return bootstrap.WirePublishPolicy(reg, bus, creds, maxMsgBytes) }},
		{"auth", func() error { return bootstrap.WireAuth(reg, srv, authInterceptor, creds, maxMsgBytes) }},
	} {
		for i := 0; i < 20; i++ {
			if err := discover.fn(); err != nil {
				slog.Warn("sidecar policy discovery", "capability", discover.name, "error", err)
			}
			if (discover.name == "call.policy" && meshClient.CallPolicy() != nil) ||
				(discover.name == "publish.policy" && bus.PublishPolicy() != nil) {
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
	}

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

	select {
	case <-ctx.Done():
	case err := <-fatalErr:
		slog.Error("fatal error, shutting down", "error", err)
		cancel()
	}
	slog.Info("shutting down...")

	signal.Stop(sighupCh)

	drainCtx, drainCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer drainCancel()
	if err := srv.Drain(drainCtx); err != nil {
		slog.Error("api drain", "error", err)
	}

	grpcSrv.GracefulStop()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	watchCancel()
	if err := workerPool.Shutdown(shutdownCtx); err != nil {
		slog.Error("workerpool shutdown", "error", err)
	}
	bus.Close()
	if err := modMgr.StopAll(shutdownCtx); err != nil {
		slog.Error("module stop", "error", err)
	}

	connPool.Close()
	discoveryGrpc.Close()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("api shutdown", "error", err)
	}

	slog.Info("MuxCore stopped.")
}

func parseFlags() (tagName, spoolURL, watchdogPath, taskDir, idempotencyDir, deadletterDir *string, printVersion, dryRun *bool) {
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

func initIdempotency(ctx context.Context, reg *registry.Registry, nodeID string, dir string) *idempotency.Store {
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

func initDeadLetter(ctx context.Context, reg *registry.Registry, nodeID string, dir string) *deadletter.Store {
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

func initGRPCMesh(ctx context.Context, cfg *config.Config, bus *events.MemoryBus) (grpcSrv *grpc.Server, meshClient *grpcmesh.Client, discoveryGrpc *grpcmesh.DiscoveryServer, connPool *grpcmesh.ConnPool, reg *registry.Registry, creds credentials.TransportCredentials, authInterceptor *grpcmesh.AuthInterceptor, nodeID string, cluster contracts.Cluster, certAuth *grpcmesh.CertAuthority) {
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
			authInterceptor.StreamInterceptor(),
		),
	)

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
	cluster.Start(ctx)
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
