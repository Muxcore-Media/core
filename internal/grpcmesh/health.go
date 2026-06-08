package grpcmesh

import (
	"context"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"
	healthv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/health/v1"
	"google.golang.org/grpc"
)

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
		return s.checkModule(req.GetModuleId()), nil
	}
	return s.checkNode(), nil
}

func (s *HealthServer) checkModule(moduleID string) *healthv1.HealthCheckResponse {
	entry, err := s.reg.Resolve(moduleID)
	if err != nil {
		return &healthv1.HealthCheckResponse{
			ModuleId: moduleID,
			Status:   healthv1.HealthCheckResponse_STATUS_UNHEALTHY,
			Error:    err.Error(),
		}
	}

	if err := entry.Module.Health(context.Background()); err != nil {
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

func (s *HealthServer) checkNode() *healthv1.HealthCheckResponse {
	entries := s.reg.ListAll()
	healthy := true
	for _, entry := range entries {
		if err := entry.Module.Health(context.Background()); err != nil {
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
	// Streaming health watch: use client-requested interval or default to 30s.
	interval := req.GetIntervalSeconds()
	if interval < 1 {
		interval = 30
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
			resp := s.checkModule(moduleID)
			if err := stream.Send(resp); err != nil {
				return err
			}
		}
		// Also send node-level health
		if err := stream.Send(s.checkNode()); err != nil {
			return err
		}
	}
}
