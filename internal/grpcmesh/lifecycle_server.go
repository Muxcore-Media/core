//nolint:govet // struct field alignment
package grpcmesh

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Muxcore-Media/core/internal/callerid"
	modulemgr "github.com/Muxcore-Media/core/internal/module/mgr"
	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/pkg/contracts"
	lifecyclev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/lifecycle/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// LifecycleServer implements the ModuleLifecycleService gRPC service for
// runtime module lifecycle management. Admin tools use this to inspect,
// spawn, stop, and restart modules.
type LifecycleServer struct {
	lifecyclev1.UnimplementedModuleLifecycleServiceServer
	reg       *registry.Registry
	modMgr    *modulemgr.Manager
	nodeID    string
	discovery *DiscoveryServer
}

// NewLifecycleServer creates a gRPC server for module lifecycle management.
func NewLifecycleServer(reg *registry.Registry, modMgr *modulemgr.Manager, nodeID string, discovery *DiscoveryServer) *LifecycleServer {
	return &LifecycleServer{
		reg:       reg,
		modMgr:    modMgr,
		nodeID:    nodeID,
		discovery: discovery,
	}
}

// RegisterWithGRPC registers this server with a gRPC server.
func (s *LifecycleServer) RegisterWithGRPC(srv *grpc.Server) {
	lifecyclev1.RegisterModuleLifecycleServiceServer(srv, s)
}

// checkAuth enforces caller authentication for lifecycle operations.
func (s *LifecycleServer) checkAuth(ctx context.Context) error {
	callerID := callerid.Get(ctx)
	if callerID == "" || callerID == "_public" {
		return status.Error(codes.PermissionDenied, "lifecycle: authentication required")
	}
	return nil
}

// ListModules returns all modules known to this node, including state and
// health. Supports optional filtering by state and capability.
func (s *LifecycleServer) ListModules(ctx context.Context, req *lifecyclev1.ListModulesRequest) (*lifecyclev1.ListModulesResponse, error) {
	if err := s.checkAuth(ctx); err != nil {
		return nil, err
	}
	stateFilter := req.GetStateFilter()
	capFilter := req.GetCapabilityFilter()

	var entries []*registry.Entry
	if capFilter != "" {
		entries = s.reg.ListByCapability(capFilter)
	} else {
		entries = s.reg.List()
	}

	modules := make([]*lifecyclev1.ModuleStatusProto, 0, len(entries))
	for _, e := range entries {
		if stateFilter != "" && string(e.State) != stateFilter {
			continue
		}

		modState := string(e.State)
		if e.State == "" {
			modState = "running"
		}

		healthStr := ""
		if e.Health != nil {
			healthStr = e.Health.Error()
		}

		modules = append(modules, &lifecyclev1.ModuleStatusProto{
			ModuleId:      e.Info.ID,
			Name:          e.Info.Name,
			Version:       e.Info.Version,
			State:         modState,
			HealthError:   healthStr,
			Capabilities:  e.Info.Capabilities,
			NodeId:        s.nodeID,
			RestartPolicy: "never",
		})
	}

	return &lifecyclev1.ListModulesResponse{
		Modules: modules,
		Total:   int32(len(modules)), //nolint:gosec // bounded by module count
	}, nil
}

// SpawnModule resolves and spawns a module. The repo URL and version must
// be provided — no tag lookup is performed (use SpoolService.DeployTag
// for tag-based deployment).
func (s *LifecycleServer) SpawnModule(ctx context.Context, req *lifecyclev1.SpawnModuleRequest) (*lifecyclev1.SpawnModuleResponse, error) {
	if err := s.checkAuth(ctx); err != nil {
		return nil, err
	}
	if req.GetRepo() == "" {
		return nil, status.Error(codes.InvalidArgument, "repo is required")
	}

	instanceID := req.GetInstanceId()
	moduleID := modulemgr.ModuleIDFromRepoWithInstance(req.GetRepo(), instanceID)

	// Check not already running.
	_, err := s.reg.Get(moduleID)
	if err == nil {
		return &lifecyclev1.SpawnModuleResponse{
			ModuleId: moduleID,
			Accepted: false,
			Error:    fmt.Sprintf("module %q is already running", moduleID),
		}, nil
	}

	// Resolve the module binary from the given repo and version.
	// We require a version string since the tag is not used for direct spawns.
	if req.GetTagName() == "" {
		return nil, status.Error(codes.InvalidArgument, "tag_name is required for resolution")
	}

	tm := contracts.TagModule{
		Repo:       req.GetRepo(),
		Version:    req.GetVersion(),
		InstanceID: instanceID,
		Config:     req.GetConfig(),
	}

	bin, err := s.modMgr.ResolveTagModule(tm)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "resolve module: %v", err)
	}

	if err := s.modMgr.Spawn(ctx, bin); err != nil {
		return nil, status.Errorf(codes.Unavailable, "spawn module: %v", err)
	}

	slog.Info("module spawned via lifecycle service", "module", moduleID)
	return &lifecyclev1.SpawnModuleResponse{
		ModuleId: moduleID,
		Accepted: true,
	}, nil
}

// StopModule sends SIGTERM to a running module and waits for it to stop.
// The module manager handles graceful shutdown via the lifecycle manager.
func (s *LifecycleServer) StopModule(ctx context.Context, req *lifecyclev1.StopModuleRequest) (*lifecyclev1.StopModuleResponse, error) {
	if err := s.checkAuth(ctx); err != nil {
		return nil, err
	}
	if req.GetModuleId() == "" {
		return nil, status.Error(codes.InvalidArgument, "module_id is required")
	}

	// Verify the module exists.
	entry, err := s.reg.Get(req.GetModuleId())
	if err != nil {
		return &lifecyclev1.StopModuleResponse{
			Acknowledged: false,
			Error:        fmt.Sprintf("module %q not found", req.GetModuleId()),
		}, nil
	}

	// If the module implements the Module interface, call Stop.
	if entry.Module != nil {
		stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if stopErr := entry.Module.Stop(stopCtx); stopErr != nil {
			slog.Warn("module stop via lifecycle service returned error", "module", req.GetModuleId(), "error", stopErr)
		}
	}

	// For sidecar modules, the StopModule gRPC was already called by the
	// module itself in the old flow. In the new flow, the module manager
	// tracks processes — attempt to stop via the manager.
	// If the module manager has a process for this module, kill it.
	if unregErr := s.reg.Unregister(req.GetModuleId()); unregErr != nil {
		slog.Warn("lifecycle: unregister failed on stop", "module", req.GetModuleId(), "error", unregErr)
	}

	slog.Info("module stopped via lifecycle service", "module", req.GetModuleId())
	return &lifecyclev1.StopModuleResponse{
		Acknowledged: true,
	}, nil
}

// RestartModule kills the running process for a module and re-spawns it.
func (s *LifecycleServer) RestartModule(ctx context.Context, req *lifecyclev1.RestartModuleRequest) (*lifecyclev1.RestartModuleResponse, error) {
	if err := s.checkAuth(ctx); err != nil {
		return nil, err
	}
	if req.GetModuleId() == "" {
		return nil, status.Error(codes.InvalidArgument, "module_id is required")
	}

	if err := s.modMgr.RestartModule(ctx, req.GetModuleId()); err != nil {
		return nil, status.Errorf(codes.Unavailable, "restart module: %v", err)
	}

	slog.Info("module restarted via lifecycle service", "module", req.GetModuleId())
	return &lifecyclev1.RestartModuleResponse{
		Accepted: true,
	}, nil
}
