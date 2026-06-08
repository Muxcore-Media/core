package grpcmesh

import (
	"context"
	"sync"
	"time"

	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type DiscoveryServer struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
	mu       sync.RWMutex
	nodeID   string
	grpcAddr string
	httpAddr string
	members  map[string]*discoveryv1.NodeInfo
	watchers map[chan *discoveryv1.ClusterEvent]struct{}
}

func NewDiscoveryServer(nodeID, grpcAddr, httpAddr string) *DiscoveryServer {
	return &DiscoveryServer{
		nodeID:   nodeID,
		grpcAddr: grpcAddr,
		httpAddr: httpAddr,
		members:  make(map[string]*discoveryv1.NodeInfo),
		watchers: make(map[chan *discoveryv1.ClusterEvent]struct{}),
	}
}

func (s *DiscoveryServer) RegisterWithGRPC(srv *grpc.Server) {
	discoveryv1.RegisterDiscoveryServiceServer(srv, s)
}

func (s *DiscoveryServer) Join(ctx context.Context, req *discoveryv1.JoinRequest) (*discoveryv1.JoinResponse, error) {
	node := req.GetNode()
	if node == nil || node.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "node ID is required")
	}
	s.mu.Lock()
	s.members[node.GetId()] = node
	event := &discoveryv1.ClusterEvent{
		Type: discoveryv1.ClusterEvent_TYPE_NODE_JOINED,
		Node: node,
	}
	for ch := range s.watchers {
		select {
		case ch <- event:
		default:
		}
	}
	s.mu.Unlock()
	s.mu.RLock()
	memberList := make([]*discoveryv1.NodeInfo, 0, len(s.members))
	for _, m := range s.members {
		memberList = append(memberList, m)
	}
	s.mu.RUnlock()
	return &discoveryv1.JoinResponse{Members: memberList}, nil
}

func (s *DiscoveryServer) Leave(ctx context.Context, req *discoveryv1.LeaveRequest) (*discoveryv1.LeaveResponse, error) {
	nodeID := req.GetNodeId()
	s.mu.Lock()
	node, existed := s.members[nodeID]
	delete(s.members, nodeID)
	if existed && node != nil {
		event := &discoveryv1.ClusterEvent{
			Type: discoveryv1.ClusterEvent_TYPE_NODE_LEFT,
			Node: node,
		}
		for ch := range s.watchers {
			select {
			case ch <- event:
			default:
			}
		}
	}
	s.mu.Unlock()
	return &discoveryv1.LeaveResponse{}, nil
}

func (s *DiscoveryServer) Heartbeat(ctx context.Context, req *discoveryv1.HeartbeatRequest) (*discoveryv1.HeartbeatResponse, error) {
	// Heartbeat received; node is alive.
	return &discoveryv1.HeartbeatResponse{}, nil
}

func (s *DiscoveryServer) Members(ctx context.Context, req *discoveryv1.MembersRequest) (*discoveryv1.MembersResponse, error) {
	s.mu.RLock()
	memberList := make([]*discoveryv1.NodeInfo, 0, len(s.members))
	for _, m := range s.members {
		memberList = append(memberList, m)
	}
	s.mu.RUnlock()
	return &discoveryv1.MembersResponse{Members: memberList}, nil
}

func (s *DiscoveryServer) Watch(req *discoveryv1.MembersRequest, stream discoveryv1.DiscoveryService_WatchServer) error {
	ch := make(chan *discoveryv1.ClusterEvent, 16)
	s.mu.Lock()
	s.watchers[ch] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.watchers, ch)
		s.mu.Unlock()
	}()
	for {
		select {
		case <-stream.Context().Done():
			return nil
		case event := <-ch:
			if err := stream.Send(event); err != nil {
				return err
			}
		}
	}
}

func (s *DiscoveryServer) LocalNode() *discoveryv1.NodeInfo {
	return &discoveryv1.NodeInfo{
		Id:       s.nodeID,
		GprcAddr: s.grpcAddr,
		HttpAddr: s.httpAddr,
	}
}

func (s *DiscoveryServer) CleanupStale(maxAge time.Duration) {
	_ = maxAge
}
