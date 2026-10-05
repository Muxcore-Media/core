// Package integsupport exposes a minimal in-process muxcore mesh for umbrella
// integration tests.
//
// NewCoreHarness starts the core mesh, event, storage and health gRPC services
// on an ephemeral loopback port with allow-all publish/call policies and TLS
// disabled. It is intended for tests only and needs no external services.
package integsupport

import (
	"context"
	"net"
	"testing"

	"github.com/Muxcore-Media/core/internal/audit"
	"github.com/Muxcore-Media/core/internal/events"
	"github.com/Muxcore-Media/core/internal/grpcmesh"
	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/internal/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type allowAllPublishPolicy struct{}

func (*allowAllPublishPolicy) CanPublish(_ context.Context, _, _ string) (bool, error) {
	return true, nil
}

type allowAllCallPolicy struct{}

func (*allowAllCallPolicy) AllowCall(_ context.Context, _, _, _ string) (bool, error) {
	return true, nil
}

// CoreHarness wires core gRPC services in-process for module integration tests.
type CoreHarness struct {
	Bus    *events.MemoryBus
	cancel context.CancelFunc
	ctx    context.Context //nolint:containedctx // harness lifetime context exposed via Context()
	grpc   *grpc.Server
	Addr   string
}

// NewCoreHarness starts core discovery, events, health, storage, and mesh on a random port.
func NewCoreHarness(t *testing.T) *CoreHarness {
	t.Helper()
	t.Setenv("MUXCORE_DEV_TLS_SKIP", "true")

	ctx, cancel := context.WithCancel(context.Background())
	bus := events.NewMemoryBus()
	bus.SetPublishPolicy(&allowAllPublishPolicy{})

	auditLogger, _ := audit.NewFileLogger("")
	bus.SetAuditLogger(auditLogger)

	reg := registry.New()
	store := storage.NewOrchestrator(reg)

	var lc net.ListenConfig
	lis, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		cancel()
		t.Fatalf("listen core: %v", err)
	}

	grpcSrv := grpc.NewServer(grpc.Creds(insecure.NewCredentials()))

	meshSrv := grpcmesh.NewServer()
	meshSrv.RegisterWithGRPC(grpcSrv)

	eventSrv := grpcmesh.NewEventServer(bus)
	eventSrv.RegisterWithGRPC(grpcSrv)

	storageGrpc := grpcmesh.NewStorageServer(store)
	storageGrpc.SetCallPolicy(&allowAllCallPolicy{})
	storageGrpc.RegisterWithGRPC(grpcSrv)

	healthSrv := grpcmesh.NewHealthServer(reg)
	healthSrv.RegisterWithGRPC(grpcSrv)

	go func() { _ = grpcSrv.Serve(lis) }()

	h := &CoreHarness{
		Addr:   lis.Addr().String(),
		Bus:    bus,
		cancel: cancel,
		ctx:    ctx,
		grpc:   grpcSrv,
	}
	t.Cleanup(h.Close)
	return h
}

// Close stops the harness gRPC server.
func (h *CoreHarness) Close() {
	if h.cancel != nil {
		h.cancel()
	}
	if h.grpc != nil {
		h.grpc.GracefulStop()
	}
}

// Context returns the harness lifetime context.
func (h *CoreHarness) Context() context.Context {
	return h.ctx
}
