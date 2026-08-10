package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/Muxcore-Media/core/internal/api"
	"github.com/Muxcore-Media/core/internal/audit"
	"github.com/Muxcore-Media/core/internal/bootstrap"
	"github.com/Muxcore-Media/core/internal/config"
	"github.com/Muxcore-Media/core/internal/events"
	"github.com/Muxcore-Media/core/internal/grpcmesh"
	modulemgr "github.com/Muxcore-Media/core/internal/module/mgr"
	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/internal/spool"
	"github.com/Muxcore-Media/core/internal/workerpool"
	"github.com/Muxcore-Media/core/pkg/contracts"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// Tunables for sidecar policy discovery (overridden in tests).
var (
	sidecarPolicyAttempts = 20
	sidecarPolicySleep    = 250 * time.Millisecond
)

func runDryRun(ctx context.Context, tagName, spoolURL string) {
	slog.Info("dry-run: config and startup checks passed")
	if tagName != "" {
		slog.Info("dry-run: fetching spool tag to verify connectivity", "spool", spoolURL, "tag", tagName)
		tag, err := spool.FetchTag(ctx, spoolURL, tagName)
		if err != nil {
			slog.Error("dry-run: spool connectivity failed", "error", err)
			os.Exit(1)
		}
		slog.Info("dry-run: spool OK", "tag", tag.Name, "modules", len(tag.Modules))
	}
	slog.Info("dry-run: all checks passed — ready to start")
	os.Exit(0)
}

func warnEncryptionProviders(reg *registry.Registry) {
	encryptionEntries := reg.FindByCapability("encryption")
	if len(encryptionEntries) == 0 {
		if !bootstrap.DevTLSSkipCheck() {
			slog.Warn("no encryption provider registered — data at rest will not be encrypted. Deploy an encryption module or set MUXCORE_DEV_TLS_SKIP for development")
		} else {
			slog.Info("no encryption provider registered — data at rest is not encrypted (development mode)")
		}
		return
	}
	if ep, ok := encryptionEntries[0].Module.(contracts.EncryptionProvider); ok {
		if ep.Available() {
			slog.Info("encryption provider loaded from registry", "module", encryptionEntries[0].Info.ID)
		} else {
			slog.Warn("encryption provider registered but unavailable", "module", encryptionEntries[0].Info.ID)
		}
	}
}

func wirePoliciesAndAuth(
	reg *registry.Registry,
	meshClient *grpcmesh.Client,
	storageGrpc *grpcmesh.StorageServer,
	bus *events.MemoryBus,
	srv *api.Server,
	authInterceptor *grpcmesh.AuthInterceptor,
	creds credentials.TransportCredentials,
	maxMsgBytes int,
) {
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

	warnEncryptionProviders(reg)

	if err := bootstrap.WireAuth(reg, srv, authInterceptor, creds, maxMsgBytes); err != nil {
		slog.Warn("auth setup", "error", err)
	}
	if len(reg.FindByCapability(contracts.CapabilityAuthorizer)) == 0 {
		slog.Error("no authorizer registered — HTTP API requests requiring authorization will be denied until an authorizer module is deployed")
	}
	if len(reg.FindByCapability(contracts.CapabilityAuth)) == 0 {
		slog.Info("no auth provider registered — HTTP API has no authentication. All requests except /health and public paths will be rejected")
	}
}

func registerManagementGRPC(
	grpcSrv *grpc.Server,
	spoolURL string,
	cfg *config.Config,
	cfgMu *sync.Mutex,
	modMgr *modulemgr.Manager,
	reg *registry.Registry,
	nodeID string,
	discoveryGrpc *grpcmesh.DiscoveryServer,
	auditLogger *audit.FileLogger,
) {
	spoolGrpc := grpcmesh.NewSpoolServer(spoolURL, cfg, cfgMu, modMgr, reg)
	spoolGrpc.RegisterWithGRPC(grpcSrv)
	slog.Info("spool management gRPC service registered")

	lifecycleGrpc := grpcmesh.NewLifecycleServer(reg, modMgr, nodeID, discoveryGrpc)
	lifecycleGrpc.RegisterWithGRPC(grpcSrv)
	slog.Info("module lifecycle gRPC service registered")

	auditGrpc := grpcmesh.NewAuditServer(auditLogger)
	auditGrpc.RegisterWithGRPC(grpcSrv)
	slog.Info("audit gRPC service registered")
}

func startHTTPAndGRPC(cfg *config.Config, srv *api.Server, grpcSrv *grpc.Server) (fatalErr chan error) {
	fatalErr = make(chan error, 1)

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
	return fatalErr
}

func waitForSidecarPolicies(
	reg *registry.Registry,
	meshClient *grpcmesh.Client,
	storageGrpc *grpcmesh.StorageServer,
	bus *events.MemoryBus,
	srv *api.Server,
	authInterceptor *grpcmesh.AuthInterceptor,
	creds credentials.TransportCredentials,
	maxMsgBytes int,
) {
	for _, discover := range []struct {
		fn   func() error
		name string
	}{
		{func() error { return bootstrap.WireCallPolicy(reg, meshClient, storageGrpc, creds, maxMsgBytes) }, "call.policy"},
		{func() error { return bootstrap.WirePublishPolicy(reg, bus, creds, maxMsgBytes) }, "publish.policy"},
		{func() error { return bootstrap.WireAuth(reg, srv, authInterceptor, creds, maxMsgBytes) }, "auth"},
	} {
		for i := 0; i < sidecarPolicyAttempts; i++ {
			if err := discover.fn(); err != nil {
				slog.Warn("sidecar policy discovery", "capability", discover.name, "error", err)
			}
			if (discover.name == "call.policy" && meshClient.CallPolicy() != nil) ||
				(discover.name == "publish.policy" && bus.PublishPolicy() != nil) ||
				(discover.name == "auth" && len(reg.FindByCapability(contracts.CapabilityAuth)) > 0) {
				break
			}
			if sidecarPolicySleep > 0 {
				time.Sleep(sidecarPolicySleep)
			}
		}
	}
}

func awaitAndShutdown(
	ctx context.Context,
	cancel context.CancelFunc,
	sighupCh chan os.Signal,
	fatalErr <-chan error,
	srv *api.Server,
	grpcSrv *grpc.Server,
	watchCancel context.CancelFunc,
	workerPool *workerpool.Pool,
	bus *events.MemoryBus,
	modMgr *modulemgr.Manager,
	connPool *grpcmesh.ConnPool,
	discoveryGrpc *grpcmesh.DiscoveryServer,
) {
	select {
	case <-ctx.Done():
	case err := <-fatalErr:
		slog.Error("fatal error, shutting down", "error", err)
		cancel()
	}
	slog.Info("shutting down...")

	signal.Stop(sighupCh)

	// Parent ctx is already cancelled; WithoutCancel keeps a live deadline for drain/stop.
	drainCtx, drainCancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer drainCancel()
	if err := srv.Drain(drainCtx); err != nil {
		slog.Error("api drain", "error", err)
	}

	grpcSrv.GracefulStop()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer shutdownCancel()

	watchCancel()
	if err := workerPool.Shutdown(shutdownCtx); err != nil {
		slog.Error("workerpool shutdown", "error", err)
	}
	bus.Close() //nolint:contextcheck // MemoryBus.Close has no context parameter
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
