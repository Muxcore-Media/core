package grpcmesh

import (
	"context"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"
	healthv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/health/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// RegisterStandardHealthProbe registers the standard gRPC Health Checking
// Protocol service on the given server. This enables K8s liveness/readiness
// probes and grpc-health-probing tools to check MuxCore health over gRPC.
// The probe always reports SERVING — granular module health is available
// via the custom HealthService (HealthServer).
func RegisterStandardHealthProbe(srv *grpc.Server) {
	grpc_health_v1.RegisterHealthServer(srv, &standardHealthProbe{})
}

// standardHealthProbe implements grpc_health_v1.HealthServer with a static
// SERVING response. The custom HealthServer provides per-module health.
type standardHealthProbe struct {
	grpc_health_v1.UnimplementedHealthServer
}

func (p *standardHealthProbe) Check(_ context.Context, _ *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	return &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING}, nil
}

func (p *standardHealthProbe) Watch(_ *grpc_health_v1.HealthCheckRequest, stream grpc_health_v1.Health_WatchServer) error {
	resp := &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		if err := stream.Send(resp); err != nil {
			return err
		}
		select {
		case <-stream.Context().Done():
			return nil
		case <-ticker.C:
		}
	}
}

// HealthServer implements the HealthService gRPC service.
// It exposes module and node health over gRPC, using the core registry.
type HealthServer struct {
	healthv1.UnimplementedHealthServiceServer
	reg contracts.Registry
}

// NewHealthServer creates a health gRPC server.
func NewHealthServer(reg contracts.Registry) *HealthServer {
	return &HealthServer{reg: reg}
}

// RegisterWithGRPC registers this server with a gRPC server.
func (s *HealthServer) RegisterWithGRPC(srv *grpc.Server) {
	healthv1.RegisterHealthServiceServer(srv, s)
}

// Check returns health status for a node or specific module.
func (s *HealthServer) Check(ctx context.Context, req *healthv1.HealthCheckRequest) (*healthv1.HealthCheckResponse, error) {
	if req.GetModuleId() != "" {
		return s.checkModule(ctx, req.GetModuleId()), nil
	}
	return s.checkNode(ctx), nil
}

func (s *HealthServer) checkModule(ctx context.Context, moduleID string) *healthv1.HealthCheckResponse {
	entry, err := s.reg.Resolve(moduleID)
	if err != nil {
		return &healthv1.HealthCheckResponse{
			ModuleId: moduleID,
			Status:   healthv1.HealthCheckResponse_STATUS_UNHEALTHY,
			Error:    err.Error(),
		}
	}

	if err := entry.Module.Health(ctx); err != nil {
		return &healthv1.HealthCheckResponse{
			ModuleId: moduleID,
			Status:   healthv1.HealthCheckResponse_STATUS_DEGRADED,
			Error:    err.Error(),
		}
	}

	return &healthv1.HealthCheckResponse{
		ModuleId: moduleID,
		Status:   healthv1.HealthCheckResponse_STATUS_HEALTHY,
	}
}

func (s *HealthServer) checkNode(ctx context.Context) *healthv1.HealthCheckResponse {
	entries := s.reg.ListAll()
	healthy := true
	for _, entry := range entries {
		if err := entry.Module.Health(ctx); err != nil {
			healthy = false
			break
		}
	}

	status := healthv1.HealthCheckResponse_STATUS_HEALTHY
	if !healthy {
		status = healthv1.HealthCheckResponse_STATUS_DEGRADED
	}
	return &healthv1.HealthCheckResponse{Status: status}
}

// Watch streams health status changes for a set of modules.
func (s *HealthServer) Watch(req *healthv1.HealthWatchRequest, stream healthv1.HealthService_WatchServer) error {
	// Streaming health watch: use client-requested interval, clamped to [1s, 300s].
	interval := req.GetIntervalSeconds()
	if interval < 1 {
		interval = 30 // default
	}
	if interval > 300 {
		interval = 300 // max 5 minutes — prevents open-forever streams
	}
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case <-ticker.C:
		}

		for _, moduleID := range req.GetModuleIds() {
			resp := s.checkModule(stream.Context(), moduleID)
			if err := stream.Send(resp); err != nil {
				return err
			}
		}
		// Also send node-level health
		if err := stream.Send(s.checkNode(stream.Context())); err != nil {
			return err
		}
	}
}
