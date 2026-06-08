package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Muxcore-Media/core/internal/api"
	"github.com/Muxcore-Media/core/internal/config"
	"github.com/Muxcore-Media/core/internal/events"
	"github.com/Muxcore-Media/core/internal/grpcmesh"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"github.com/Muxcore-Media/core/internal/module"
	_ "github.com/Muxcore-Media/core/internal/presets"
	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/internal/storage"
	"github.com/Muxcore-Media/core/pkg/contracts"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func main() {
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

	// Build gRPC server options with optional TLS credentials.
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
		slog.Warn("gRPC TLS is disabled — all inter-node communication is plaintext. Set MUXCORE_GRPC_TLS_CERT and MUXCORE_GRPC_TLS_KEY to enable encryption.")
	}
	grpcSrv := grpc.NewServer(grpcOpts...)
	meshSrv.RegisterWithGRPC(grpcSrv)


	reg := registry.New()
	healthGrpc := grpcmesh.NewHealthServer(reg)
	healthGrpc.RegisterWithGRPC(grpcSrv)

	eventGrpc := grpcmesh.NewEventServer(bus)
	eventGrpc.RegisterWithGRPC(grpcSrv)

	discoveryGrpc := grpcmesh.NewDiscoveryServer(
		"muxcore-"+cfg.GRPC.Addr,
		cfg.GRPC.Addr,
		cfg.Server.Addr,
		cfg.GRPC.JoinToken,
	)
	discoveryGrpc.RegisterWithGRPC(grpcSrv)
	mgr := module.NewManager(reg, bus)

	srv := api.NewServer(cfg.Server.Addr, cfg.Server.CertFile, cfg.Server.KeyFile)

	store := storage.NewOrchestrator(reg)
	if err := store.DiscoverStorage(); err != nil {
		slog.Warn("storage discover", "error", err)
	}
	store.DiscoverCache()
	slog.Info("storage orchestrator ready", "providers", store.ProviderCount())
	watchCancel := store.WatchModules(bus)

	deps := contracts.Fabric{
		Registry: reg,
		EventBus: bus,
		Routes:   srv,
		Cluster:  nil,
		WorkerPool: nil,
		Audit:      nil,
		Storage:  store,
		Mesh:     meshClient,
	}

	modules := contracts.LoadRegistered(deps)

	// Discover infrastructure modules
	var cl contracts.Cluster
	var wp contracts.WorkerPool
	var al contracts.AuditLogger
	var rl contracts.RateLimiterProvider
	var authz contracts.Authorizer
	var cp contracts.CallPolicyProvider
	var hm contracts.HealthMonitor
	for _, mod := range modules {
		if c, ok := mod.(contracts.Cluster); ok {
			cl = c
		}
		if w, ok := mod.(contracts.WorkerPool); ok {
			wp = w
		}
		if a, ok := mod.(contracts.AuditLogger); ok {
			al = a
		}
		if r, ok := mod.(contracts.RateLimiterProvider); ok {
			rl = r
		}
		if cpp, ok := mod.(contracts.CallPolicyProvider); ok {
			cp = cpp
		}
		if az, ok := mod.(contracts.Authorizer); ok {
			authz = az
		}
		if h, ok := mod.(contracts.HealthMonitor); ok {
			hm = h
		}
	}
	deps.Cluster = cl
	deps.WorkerPool = wp
	deps.Audit = al
	meshClient.SetCluster(cl)
	if cp != nil {
		meshClient.SetCallPolicy(cp)
		slog.Info("call policy enabled")
	}

	// Re-inject discovered infrastructure deps into modules that need them.
	// Modules implementing InfrastructureAware receive the actual
	// Cluster, WorkerPool, and AuditLogger after they have been discovered.
	for _, mod := range modules {
		if aware, ok := mod.(contracts.InfrastructureAware); ok {
			aware.SetInfrastructure(deps.Cluster, deps.WorkerPool, deps.Audit)
		}
	}

	// Wire rate limiter into API server
	if rl != nil {
		srv.SetRateLimiter(rl)
		slog.Info("rate limiter enabled", "module", "ratelimit-tokenbucket")
	}

	// Wire audit logger into API server
	if al != nil {
		srv.SetAuditLogger(al)
		slog.Info("audit logger enabled")
	}

	// Wire authorizer into API server
	if authz != nil {
		srv.SetAuthorizer(authz)
		slog.Info("authorizer enabled")
	}

	for _, mod := range modules {
		if err := mgr.Register(mod, nil); err != nil {
			slog.Error("register module", "id", mod.Info().ID, "error", err)
			cancel()
			return
		}
	}

	slog.Info("module registry ready", "count", reg.Count())

	// Wire auth
	authModules := reg.FindByRole("auth")
	if len(authModules) > 0 {
		if provider, ok := authModules[0].Module.(contracts.AuthProvider); ok {
			srv.SetAuthFunc(func(r *http.Request) (*contracts.Session, error) {
				token := r.Header.Get("Authorization")
				if token == "" {
					return nil, errMissingAuth
				}
				token = strings.TrimPrefix(token, "Bearer ")
				session, err := provider.Validate(r.Context(), token)
				if err != nil {
					return nil, err
				}
				return &session, nil
			})
			slog.Info("auth middleware enabled", "provider", authModules[0].Info.ID)
		}
	}

	srv.SetHealthChecker(func() map[string]error {
		return mgr.HealthCheck(context.Background())
	})

	go func() {
		if err := srv.Start(); err != nil && err != http.ErrServerClosed {
			slog.Error("api server fatal", "error", err)
			cancel()
		}
	}()

	grpcLis, err := net.Listen("tcp", cfg.GRPC.Addr)
	if err != nil {
		slog.Error("grpc listen", "error", err)
		cancel()
	}
	go func() {
		slog.Info("gRPC mesh listening", "addr", cfg.GRPC.Addr)
		if err := grpcSrv.Serve(grpcLis); err != nil {
			slog.Error("grpc server", "error", err)
		}
	}()

	// Auto-join seed nodes if configured.
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
				md := metadata.New(map[string]string{"x-cluster-join-token": cfg.GRPC.JoinToken})
				joinCtx := metadata.NewOutgoingContext(ctx, md)
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

	if cl != nil {
		if err := cl.Start(ctx); err != nil {
			slog.Error("cluster start", "error", err)
			cancel()
		}
		slog.Info("cluster started", "node_id", cl.LocalNode().ID)
	}

	if err := mgr.InitAll(ctx); err != nil {
		slog.Error("init modules", "error", err)
		cancel()
	}
	if err := mgr.StartAll(ctx); err != nil {
		slog.Error("start modules", "error", err)
		cancel()
	}

	// Start health monitor if available
	if hm != nil {
		if err := hm.StartMonitoring(ctx, reg, bus); err != nil {
			slog.Error("health monitor start", "error", err)
		} else {
			slog.Info("health monitor started")
		}
	}

	slog.Info("MuxCore running", "addr", cfg.Server.Addr)

	<-ctx.Done()
	watchCancel()
	slog.Info("shutting down...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if hm != nil {
		hm.Stop(shutdownCtx)
	}
	grpcSrv.GracefulStop()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("api shutdown", "error", err)
	}
	if err := mgr.StopAll(shutdownCtx); err != nil {
		slog.Error("module shutdown", "error", err)
	}
	if cl != nil {
		if err := cl.Stop(shutdownCtx); err != nil {
			slog.Error("cluster shutdown", "error", err)
		}
	}
	slog.Info("MuxCore stopped.")
}

var errMissingAuth = errStr("missing Authorization header")

type errStr string

func (e errStr) Error() string { return string(e) }

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
