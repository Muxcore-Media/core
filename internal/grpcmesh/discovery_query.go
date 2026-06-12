package grpcmesh

import (
	"context"
	"log/slog"
	"slices"
	"sync"

	"github.com/Muxcore-Media/core/internal/registry"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// SetRegistry attaches the module registry for sidecar module discovery queries.
func (s *DiscoveryServer) SetRegistry(reg *registry.Registry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reg = reg
}

// fanOutConcurrency limits how many peer queries run simultaneously.
const fanOutConcurrency = 10

// fanOutQuery sends the same query function to all known peer nodes and
// collects responses. Errors from individual peers are logged and skipped.
func (s *DiscoveryServer) fanOutQuery(ctx context.Context, queryFn func(discoveryv1.DiscoveryServiceClient) ([]*discoveryv1.ModuleInfoProto, error)) []*discoveryv1.ModuleInfoProto {
	s.mu.RLock()
	members := make([]*discoveryv1.NodeInfo, 0, len(s.members))
	for _, m := range s.members {
		if m.GetId() != s.nodeID {
			members = append(members, m)
		}
	}
	dialOpts := s.dialOpts
	connPool := s.connPool
	s.mu.RUnlock()

	if len(members) == 0 {
		return nil
	}

	var mu sync.Mutex
	var all []*discoveryv1.ModuleInfoProto
	var wg sync.WaitGroup
	sem := make(chan struct{}, fanOutConcurrency)

	for _, member := range members {
		wg.Add(1)
		sem <- struct{}{}
		go func(addr string) {
			defer func() {
				<-sem
				if r := recover(); r != nil {
					slog.Error("discovery fan-out query panic recovered", "addr", addr, "panic", r)
				}
				wg.Done()
			}()
			client, err := s.peerClient(ctx, addr, dialOpts, connPool)
			if err != nil {
				slog.Debug("discovery: fan-out dial peer", "addr", addr, "error", err)
				return
			}
			results, err := queryFn(client)
			if err != nil {
				slog.Debug("discovery: fan-out query peer", "addr", addr, "error", err)
				return
			}
			mu.Lock()
			all = append(all, results...)
			mu.Unlock()
		}(member.GetGrpcAddr())
	}
	wg.Wait()
	return all
}

// fanOutListAll sends ListAll to all known peer nodes and collects responses.
func (s *DiscoveryServer) fanOutListAll(ctx context.Context) []*discoveryv1.ModuleEntryProto {
	s.mu.RLock()
	members := make([]*discoveryv1.NodeInfo, 0, len(s.members))
	for _, m := range s.members {
		if m.GetId() != s.nodeID {
			members = append(members, m)
		}
	}
	dialOpts := s.dialOpts
	connPool := s.connPool
	s.mu.RUnlock()

	if len(members) == 0 {
		return nil
	}

	var mu sync.Mutex
	var all []*discoveryv1.ModuleEntryProto
	var wg sync.WaitGroup
	sem := make(chan struct{}, fanOutConcurrency)

	for _, member := range members {
		wg.Add(1)
		sem <- struct{}{}
		go func(addr string) {
			defer func() {
				<-sem
				if r := recover(); r != nil {
					slog.Error("discovery fan-out ListAll panic recovered", "addr", addr, "panic", r)
				}
				wg.Done()
			}()
			client, err := s.peerClient(ctx, addr, dialOpts, connPool)
			if err != nil {
				slog.Debug("discovery: fan-out dial peer", "addr", addr, "error", err)
				return
			}
			resp, err := client.ListAll(ctx, &discoveryv1.ListAllRequest{})
			if err != nil {
				slog.Debug("discovery: fan-out ListAll peer", "addr", addr, "error", err)
				return
			}
			mu.Lock()
			all = append(all, resp.GetEntries()...)
			mu.Unlock()
		}(member.GetGrpcAddr())
	}
	wg.Wait()
	return all
}

// peerClient creates or reuses a gRPC connection to a peer node.
func (s *DiscoveryServer) peerClient(ctx context.Context, addr string, dialOpts []grpc.DialOption, connPool *ConnPool) (discoveryv1.DiscoveryServiceClient, error) {
	if connPool != nil {
		conn, err := connPool.Get(addr)
		if err != nil {
			return nil, err
		}
		return discoveryv1.NewDiscoveryServiceClient(conn), nil
	}
	opts := dialOpts
	if opts == nil {
		opts = []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithDefaultCallOptions(
				grpc.MaxCallRecvMsgSize(maxClientMsgSize),
				grpc.MaxCallSendMsgSize(maxClientMsgSize),
			),
		}
	}
	conn, err := grpc.NewClient(addr, opts...)
	if err != nil {
		return nil, err
	}
	return discoveryv1.NewDiscoveryServiceClient(conn), nil
}

// FindByCapability returns modules advertising the given capability string.
// Queries both the local registry and all known peer nodes.
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

	// Local results.
	entries := reg.ListByCapability(req.Capability)
	seen := make(map[string]bool, len(entries))
	var modules []*discoveryv1.ModuleInfoProto
	for _, e := range entries {
		seen[e.Info.ID] = true
		modules = append(modules, moduleEntryToProto(e))
	}

	// Remote results (deduplicated by module ID).
	remote := s.fanOutQuery(ctx, func(client discoveryv1.DiscoveryServiceClient) ([]*discoveryv1.ModuleInfoProto, error) {
		resp, err := client.FindByCapability(ctx, req)
		if err != nil {
			return nil, err
		}
		return resp.GetModules(), nil
	})
	for _, m := range remote {
		if !seen[m.GetId()] {
			seen[m.GetId()] = true
			modules = append(modules, m)
		}
	}

	// Sort by module ID for deterministic ordering.
	slices.SortFunc(modules, func(a, b *discoveryv1.ModuleInfoProto) int {
		switch {
		case a.GetId() < b.GetId():
			return -1
		case a.GetId() > b.GetId():
			return 1
		default:
			return 0
		}
	})

	return &discoveryv1.FindByCapabilityResponse{Modules: modules}, nil
}

// FindByRole returns modules with the given role string.
// Queries both the local registry and all known peer nodes.
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

	// Local results.
	entries := reg.ListByRole(req.Role)
	seen := make(map[string]bool, len(entries))
	var modules []*discoveryv1.ModuleInfoProto
	for _, e := range entries {
		seen[e.Info.ID] = true
		modules = append(modules, moduleEntryToProto(e))
	}

	// Remote results (deduplicated by module ID).
	remote := s.fanOutQuery(ctx, func(client discoveryv1.DiscoveryServiceClient) ([]*discoveryv1.ModuleInfoProto, error) {
		resp, err := client.FindByRole(ctx, req)
		if err != nil {
			return nil, err
		}
		return resp.GetModules(), nil
	})
	for _, m := range remote {
		if !seen[m.GetId()] {
			seen[m.GetId()] = true
			modules = append(modules, m)
		}
	}

	slices.SortFunc(modules, func(a, b *discoveryv1.ModuleInfoProto) int {
		switch {
		case a.GetId() < b.GetId():
			return -1
		case a.GetId() > b.GetId():
			return 1
		default:
			return 0
		}
	})

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
	entry, err := reg.Get(req.ModuleId)
	if err != nil {
		return &discoveryv1.ResolveResponse{Found: false}, nil
	}
	return &discoveryv1.ResolveResponse{
		Found:  true,
		Module: moduleEntryToProto(entry),
	}, nil
}

// ListAll returns all registered modules on this node and all known peer nodes.
func (s *DiscoveryServer) ListAll(ctx context.Context, req *discoveryv1.ListAllRequest) (*discoveryv1.ListAllResponse, error) {
	s.mu.RLock()
	reg := s.reg
	nodeID := s.nodeID
	s.mu.RUnlock()
	if reg == nil {
		return nil, status.Error(codes.Unavailable, "registry not available")
	}

	// Local results.
	localEntries := reg.List()
	seen := make(map[string]bool, len(localEntries))
	var entries []*discoveryv1.ModuleEntryProto
	for _, e := range localEntries {
		seen[e.Info.ID] = true
		entries = append(entries, &discoveryv1.ModuleEntryProto{
			Info:        moduleEntryToProto(e),
			State:       string(e.State),
			HealthError: healthErrorString(e.Health),
			NodeId:      nodeID,
		})
	}

	// Remote results (deduplicated by module ID).
	remote := s.fanOutListAll(ctx)
	for _, m := range remote {
		if !seen[m.GetInfo().GetId()] {
			seen[m.GetInfo().GetId()] = true
			entries = append(entries, m)
		}
	}

	return &discoveryv1.ListAllResponse{Entries: entries}, nil
}

// moduleEntryToProto converts a registry entry to a ModuleInfoProto, including
// state and health info.
func moduleEntryToProto(e *registry.Entry) *discoveryv1.ModuleInfoProto {
	health := healthErrorString(e.Health)
	return &discoveryv1.ModuleInfoProto{
		Id:             e.Info.ID,
		Name:           e.Info.Name,
		Version:        e.Info.Version,
		Roles:          e.Info.Roles,
		Description:    e.Info.Description,
		Author:         e.Info.Author,
		Capabilities:   e.Info.Capabilities,
		DependsOn:      e.Info.DependsOn,
		MinCoreVersion: e.Info.MinCoreVersion,
		HttpAddr:       e.Info.HTTPAddr,
		State:          string(e.State),
		HealthError:    health,
	}
}

func healthErrorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
