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
	"syscall"
	"time"

	"github.com/Muxcore-Media/core/internal/api"
	"github.com/Muxcore-Media/core/internal/audit"
	"github.com/Muxcore-Media/core/internal/config"
	"github.com/Muxcore-Media/core/internal/events"
	"github.com/Muxcore-Media/core/internal/grpcmesh"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	modulemgr "github.com/Muxcore-Media/core/internal/module/mgr"
	modlifecycle "github.com/Muxcore-Media/core/internal/module"
	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/internal/spool"
	"github.com/Muxcore-Media/core/internal/storage"
	"github.com/Muxcore-Media/core/pkg/contracts"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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
	flag.Parse()

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

	logger := setupLogger(cfg.Log)
	slog.SetDefault(logger)

	slog.Info("MuxCore starting...")

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
	var grpcOpts []grpc.ServerOption
	if creds != nil {
		grpcOpts = append(grpcOpts, grpc.Creds(creds))
		slog.Info("gRPC TLS enabled",
			"cert", cfg.GRPC.CertFile,
			"mtls", cfg.GRPC.MTLSEnabled,
		)
	} else {
		slog.Warn("gRPC TLS is disabled — insecure mode")
	}
	// Add unary interceptor for gRPC authorization.
	// Provides the interception point for future per-method authorization checks
	// when an Authorizer and IdentityProvider are registered.
	grpcOpts = append(grpcOpts, grpc.UnaryInterceptor(grpcmesh.AuthUnaryInterceptor()))

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

	store := storage.NewOrchestrator(reg)
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

	// Attach registry for sidecar module discovery queries
	discoveryGrpc.SetRegistry(reg)

	// Set up runtime module manager
	lifecycleMgr := modlifecycle.NewManager(reg, bus)
	lifecycleMgr.SetAuditLogger(auditLogger)
	modMgr := modulemgr.NewManager(cfg.GRPC.Addr, reg, lifecycleMgr)
	modMgr.RegisterModuleService(grpcSrv)

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

	// Build fabric (infrastructure services nil for sidecar mode)
	deps := contracts.Fabric{
		Registry:   reg,
		EventBus:   bus,
		Routes:     srv,
		Cluster:    nil,
		WorkerPool: nil,
		Audit:      nil,
		Storage:    store,
		Mesh:       meshClient,
	}

	_ = deps // used when modules are loaded in-process

	slog.Info("module registry ready", "count", reg.Count())

	srv.SetHealthChecker(func() map[string]error {
		return nil // sidecar modules report health via mesh
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

	// Auto-join seed nodes
	if len(cfg.GRPC.SeedNodes) > 0 {
		slog.Info("auto-joining seed nodes", "seeds", cfg.GRPC.SeedNodes)
		localNode := discoveryGrpc.LocalNode()
		for _, seed := range cfg.GRPC.SeedNodes {
			go func(seedAddr string) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				var dialOpts []grpc.DialOption
				dialOpts = append(dialOpts, grpc.WithTransportCredentials(creds))
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
	var hbDialOpts []grpc.DialOption
	if creds != nil {
		hbDialOpts = append(hbDialOpts, grpc.WithTransportCredentials(creds))
	} else {
		hbDialOpts = append(hbDialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	discoveryGrpc.StartHeartbeatLoop(ctx, hbDialOpts)

	slog.Info("MuxCore running", "addr", cfg.Server.Addr, "tag", *tagName)

	select {
	case <-ctx.Done():
	case err := <-fatalErr:
		slog.Error("fatal error, shutting down", "error", err)
		cancel()
	}
	slog.Info("shutting down...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	watchCancel()

	if err := modMgr.StopAll(shutdownCtx); err != nil {
		slog.Error("module stop", "error", err)
	}
	grpcSrv.GracefulStop()
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