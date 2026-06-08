package grpcmesh

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"sync"
	"github.com/Muxcore-Media/core/internal/registry"
	"time"

	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/codes"
	"github.com/google/uuid"
	"google.golang.org/grpc/status"
)

type DiscoveryServer struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
	mu       sync.RWMutex
	joinMu   sync.Mutex
	nodeID   string
	grpcAddr string
	httpAddr string
	leaderID  string
	clusterID string
	members  map[string]*discoveryv1.NodeInfo
	lastSeen         map[string]time.Time
	joinAttempts     map[string]time.Time
	joinAttemptCounts map[string]int
	requireTLS       bool
	evictionTimeout  time.Duration
	watchers         map[chan *discoveryv1.ClusterEvent]struct{}
	stopCh           chan struct{}
	reg               *registry.Registry
	joinToken        string
	// moduleIDs returns the current list of module IDs running on this node.
	// Called by LocalNode() and heartbeat sender to advertise live module state.
	// Returns nil if no registry is wired in (standalone mode).
	moduleIDs        func() []string
	// dialOpts are gRPC dial options used by the heartbeat sender to connect
	// to peer nodes. Set via StartHeartbeatLoop or left nil.
	dialOpts         []grpc.DialOption
}

func NewDiscoveryServer(nodeID, grpcAddr, httpAddr, joinToken string, moduleIDs func() []string) *DiscoveryServer {
	ds := &DiscoveryServer{
		nodeID:          nodeID,
		grpcAddr:        grpcAddr,
		httpAddr:        httpAddr,
		members:         make(map[string]*discoveryv1.NodeInfo),
		lastSeen:        make(map[string]time.Time),
		joinAttempts:     make(map[string]time.Time),
		joinAttemptCounts: make(map[string]int),
		requireTLS:       joinToken != "",
		evictionTimeout: 30 * time.Second,
		watchers:        make(map[chan *discoveryv1.ClusterEvent]struct{}),
		stopCh:          make(chan struct{}),
		joinToken:       joinToken,
		moduleIDs:       moduleIDs,
	}
	go ds.evictLoop()
	return ds
}

func (s *DiscoveryServer) RegisterWithGRPC(srv *grpc.Server) {
	discoveryv1.RegisterDiscoveryServiceServer(srv, s)
}

func (s *DiscoveryServer) Join(ctx context.Context, req *discoveryv1.JoinRequest) (*discoveryv1.JoinResponse, error) {
	node := req.GetNode()
	if node == nil || node.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "node ID is required")
	}

	// Rate-limit join attempts per source IP (max 3 in 20s window).
	if p, ok := peer.FromContext(ctx); ok {
		src := p.Addr.String()
		s.joinMu.Lock()
		now := time.Now()
		last, exists := s.joinAttempts[src]
		if exists && now.Sub(last) < 20*time.Second {
			s.joinAttemptCounts[src]++
			if s.joinAttemptCounts[src] > 3 {
				s.joinMu.Unlock()
				return nil, status.Error(codes.ResourceExhausted, "too many join attempts")
			}
		} else {
			s.joinAttemptCounts[src] = 1
		}
		s.joinAttempts[src] = now
		s.joinMu.Unlock()
	}

	// Reject token-based joins over non-TLS connections.
	if s.joinToken != "" && s.requireTLS {
		if p, ok := peer.FromContext(ctx); ok {
			if _, isTLS := p.AuthInfo.(credentials.TLSInfo); !isTLS {
				return nil, status.Error(codes.Unauthenticated, "join token authentication requires TLS")
			}
		}
	}

	if s.joinToken != "" {
		md, ok := metadata.FromIncomingContext(ctx)
		token := ""
		if ok {
			vals := md.Get("x-cluster-join-token")
			if len(vals) > 0 {
				token = vals[0]
			}
		}
		// Constant-time comparison prevents timing side-channel attacks (CWE-208).
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.joinToken)) != 1 {
			return nil, status.Error(codes.PermissionDenied, "invalid join token")
		}
	}
	s.mu.Lock()
	s.members[node.GetId()] = node
	s.lastSeen[node.GetId()] = time.Now()
	if s.leaderID == "" {
		s.leaderID = node.GetId()
	}
	// Generate cluster ID if this is the first node joining
	if s.clusterID == "" {
		s.clusterID = uuid.New().String()
	}
	event := &discoveryv1.ClusterEvent{
		Type:     discoveryv1.ClusterEvent_TYPE_NODE_JOINED,
		Node:     node,
		LeaderId: s.leaderID,
	}
	for ch := range s.watchers {
		select {
		case ch <- event:
		default:
		}
	}
	leaderID := s.leaderID
	s.mu.Unlock()

	s.mu.RLock()
	memberList := make([]*discoveryv1.NodeInfo, 0, len(s.members))
	for _, m := range s.members {
		memberList = append(memberList, m)
	}
	s.mu.RUnlock()

	return &discoveryv1.JoinResponse{
		Members:   memberList,
		LeaderId:  leaderID,
		ClusterId: s.clusterID,
	}, nil
}

func (s *DiscoveryServer) Leave(ctx context.Context, req *discoveryv1.LeaveRequest) (*discoveryv1.LeaveResponse, error) {
	nodeID := req.GetNodeId()
	s.mu.Lock()
	node, existed := s.members[nodeID]
	delete(s.members, nodeID)
	delete(s.lastSeen, nodeID)
	if s.leaderID == nodeID {
		s.leaderID = ""
	}
	if existed && node != nil {
		event := &discoveryv1.ClusterEvent{
			Type:     discoveryv1.ClusterEvent_TYPE_NODE_LEFT,
			Node:     node,
			LeaderId: s.leaderID,
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
	nodeID := req.GetNodeId()
	if nodeID == "" {
		return nil, status.Error(codes.InvalidArgument, "node ID is required")
	}

	s.mu.Lock()
	s.lastSeen[nodeID] = time.Now()

	if node, exists := s.members[nodeID]; exists && len(req.GetModules()) > 0 {
		// Sync the module list from the heartbeat so this node's view
		// of the remote node stays current as modules register/unregister.
		node.Modules = req.GetModules()
	}

	if _, exists := s.members[nodeID]; !exists {
		s.mu.Unlock()
		return nil, status.Error(codes.NotFound, "unknown node ID — join the cluster before sending heartbeats")
	}
	leaderID := s.leaderID
	s.mu.Unlock()

	return &discoveryv1.HeartbeatResponse{
		LeaderId: leaderID,
	}, nil
}

func (s *DiscoveryServer) Members(ctx context.Context, req *discoveryv1.MembersRequest) (*discoveryv1.MembersResponse, error) {
	s.mu.RLock()
	memberList := make([]*discoveryv1.NodeInfo, 0, len(s.members))
	for _, m := range s.members {
		memberList = append(memberList, m)
	}
	leaderID := s.leaderID
	s.mu.RUnlock()
	return &discoveryv1.MembersResponse{
		Members:  memberList,
		LeaderId: leaderID,
	}, nil
}

func (s *DiscoveryServer) Watch(req *discoveryv1.MembersRequest, stream discoveryv1.DiscoveryService_WatchServer) error {
	ch := make(chan *discoveryv1.ClusterEvent, 16)
	s.mu.Lock()
	if len(s.watchers) >= 100 {
		s.mu.Unlock()
		return status.Error(codes.ResourceExhausted, "too many watchers")
	}
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
	node := &discoveryv1.NodeInfo{
		Id:       s.nodeID,
		GrpcAddr: s.grpcAddr,
		HttpAddr: s.httpAddr,
	}
	if s.moduleIDs != nil {
		node.Modules = s.moduleIDs()
	}
	return node
}

// StartHeartbeatLoop begins sending periodic heartbeats to all known cluster
// members. Heartbeats carry the current module list so peers learn about
// module registrations and unregistrations on this node.
//
// dialOpts are the gRPC dial options for connecting to peers (TLS config, etc.).
// The loop stops when ctx is cancelled.
func (s *DiscoveryServer) StartHeartbeatLoop(ctx context.Context, dialOpts []grpc.DialOption) {
	s.dialOpts = dialOpts
	go s.heartbeatLoop(ctx)
}

func (s *DiscoveryServer) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.sendHeartbeats(ctx)
		}
	}
}

func (s *DiscoveryServer) sendHeartbeats(ctx context.Context) {
	s.mu.RLock()
	// Snapshot members and module list under read lock.
	type peer struct {
		id   string
		addr string
	}
	peers := make([]peer, 0, len(s.members))
	for _, m := range s.members {
		if m.GetId() == s.nodeID {
			continue // don't heartbeat ourselves
		}
		peers = append(peers, peer{id: m.GetId(), addr: m.GetGrpcAddr()})
	}
	var moduleIDs []string
	if s.moduleIDs != nil {
		moduleIDs = s.moduleIDs()
	}
	s.mu.RUnlock()

	for _, p := range peers {
		dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		conn, err := grpc.NewClient(p.addr, s.dialOpts...)
		if err != nil {
			slog.Warn("heartbeat: dial peer", "peer", p.id, "addr", p.addr, "error", err)
			cancel()
			continue
		}
		client := discoveryv1.NewDiscoveryServiceClient(conn)
		_, err = client.Heartbeat(dialCtx, &discoveryv1.HeartbeatRequest{
			NodeId:  s.nodeID,
			Modules: moduleIDs,
		})
		if err != nil {
			slog.Warn("heartbeat: rpc failed", "peer", p.id, "error", err)
		}
		conn.Close()
		cancel()
	}
}

// evictLoop periodically scans for dead nodes and evicts them.
func (s *DiscoveryServer) evictLoop() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.evictDeadNodes()
		}
	}
}

// evictDeadNodes removes nodes that haven't sent a heartbeat within the
// eviction timeout window.  Caller must NOT hold the lock.
func (s *DiscoveryServer) evictDeadNodes() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for id, seen := range s.lastSeen {
		if now.Sub(seen) <= s.evictionTimeout {
			continue
		}
		node, existed := s.members[id]
		delete(s.members, id)
		delete(s.lastSeen, id)
		if s.leaderID == id {
			s.leaderID = ""
		}
		if existed && node != nil {
			event := &discoveryv1.ClusterEvent{
				Type:     discoveryv1.ClusterEvent_TYPE_NODE_LEFT,
				Node:     node,
				LeaderId: s.leaderID,
			}
			for ch := range s.watchers {
				select {
				case ch <- event:
				default:
				}
			}
		}
	}
}

// Close cleanly stops the eviction goroutine.
func (s *DiscoveryServer) Close() {
	close(s.stopCh)
}
