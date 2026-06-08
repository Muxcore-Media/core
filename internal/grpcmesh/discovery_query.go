package grpcmesh

import (
	"context"

	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/pkg/contracts"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SetRegistry attaches the module registry for sidecar module discovery queries.
func (s *DiscoveryServer) SetRegistry(reg *registry.Registry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reg = reg
}

// FindByCapability returns modules advertising the given capability string.
func (s *DiscoveryServer) FindByCapability(ctx context.Context, req *discoveryv1.FindByCapabilityRequest) (*discoveryv1.FindByCapabilityResponse, error) {
	if req.Capability == "" {
		return nil, status.Error(codes.InvalidArgument, "capability is required")
	}
	s.mu.RLock()
	reg := s.reg
	s.mu.RUnlock()
	if reg == nil {
		return nil, status.Error(codes.Unavailable, "registry not available")
	}
	entries := reg.FindByCapability(req.Capability)
	modules := make([]*discoveryv1.ModuleInfoProto, 0, len(entries))
	for _, e := range entries {
		modules = append(modules, moduleInfoToProto(e.Info))
	}
	return &discoveryv1.FindByCapabilityResponse{Modules: modules}, nil
}

// FindByRole returns modules with the given role string.
func (s *DiscoveryServer) FindByRole(ctx context.Context, req *discoveryv1.FindByRoleRequest) (*discoveryv1.FindByRoleResponse, error) {
	if req.Role == "" {
		return nil, status.Error(codes.InvalidArgument, "role is required")
	}
	s.mu.RLock()
	reg := s.reg
	s.mu.RUnlock()
	if reg == nil {
		return nil, status.Error(codes.Unavailable, "registry not available")
	}
	entries := reg.FindByRole(req.Role)
	modules := make([]*discoveryv1.ModuleInfoProto, 0, len(entries))
	for _, e := range entries {
		modules = append(modules, moduleInfoToProto(e.Info))
	}
	return &discoveryv1.FindByRoleResponse{Modules: modules}, nil
}

// Resolve returns a single module by ID.
func (s *DiscoveryServer) Resolve(ctx context.Context, req *discoveryv1.ResolveRequest) (*discoveryv1.ResolveResponse, error) {
	if req.ModuleId == "" {
		return nil, status.Error(codes.InvalidArgument, "module_id is required")
	}
	s.mu.RLock()
	reg := s.reg
	s.mu.RUnlock()
	if reg == nil {
		return nil, status.Error(codes.Unavailable, "registry not available")
	}
	entry, err := reg.Resolve(req.ModuleId)
	if err != nil {
		return &discoveryv1.ResolveResponse{Found: false}, nil
	}
	return &discoveryv1.ResolveResponse{
		Found:  true,
		Module: moduleInfoToProto(entry.Info),
	}, nil
}

func moduleInfoToProto(info contracts.ModuleInfo) *discoveryv1.ModuleInfoProto {
	return &discoveryv1.ModuleInfoProto{
		Id:           info.ID,
		Name:         info.Name,
		Version:      info.Version,
		Roles:        info.Roles,
		Description:  info.Description,
		Author:       info.Author,
		Capabilities: info.Capabilities,
		DependsOn:    info.DependsOn,
	}
}
