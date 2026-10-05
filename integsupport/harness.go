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
	"google.golang.org/grpc/metadata"

	"github.com/Muxcore-Media/core/internal/callerid"
)

type allowAllPublishPolicy struct{}

func (*allowAllPublishPolicy) CanPublish(_ context.Context, _, _ string) (bool, error) {
	return true, nil
}

// harnessCaller stands in for core's auth interceptor: it trusts the
// x-caller-id metadata the module SDK sends. Test-only — production core
// never trusts client-supplied caller IDs.
func harnessCaller(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx
	}
	if ids := md.Get("x-caller-id"); len(ids) > 0 && ids[0] != "" {
		return callerid.Set(ctx, ids[0])
	}
	return ctx
}

type callerStream struct {
	grpc.ServerStream
	ctx context.Context //nolint:containedctx // carries the stamped caller for the stream
}

func (s *callerStream) Context() context.Context { return s.ctx }

func unaryCaller(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	return handler(harnessCaller(ctx), req)
}

func streamCaller(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	return handler(srv, &callerStream{ServerStream: ss, ctx: harnessCaller(ss.Context())})
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

	grpcSrv := grpc.NewServer(
		grpc.Creds(insecure.NewCredentials()),
		grpc.ChainUnaryInterceptor(unaryCaller),
		grpc.ChainStreamInterceptor(streamCaller),
	)

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
		// Modules hold long-lived event Subscribe streams that GracefulStop would
		// wait on forever, so stop hard: Stop closes listeners and open streams and
		// is idempotent.
		h.grpc.Stop()
	}
}

// Context returns the harness lifetime context.
func (h *CoreHarness) Context() context.Context {
	return h.ctx
}
