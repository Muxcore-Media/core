package grpcmesh

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/Muxcore-Media/core/internal/callerid"
	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/google/uuid"

	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// Metadata keys for term propagation over gRPC.
const (
	mdKeyTerm = "x-cluster-term"
)

// DiscoveryServer implements the DiscoveryService gRPC service and manages
// cluster membership with term-based leader election.
//
// Leader election is deterministic and rank-based: the member with the
// alphabetically lowest node ID among surviving members is always the leader.
// Each node independently computes the same leader from the shared member set.
//
// Terms are monotonically increasing counters that prevent stale leadership
// claims. A node that detects a leader change increments its term and propagates
// the new term to peers via gRPC metadata on heartbeats and responses.
type DiscoveryServer struct {
	discoveryv1.UnimplementedDiscoveryServiceServer
	mu       sync.RWMutex
	joinMu   sync.Mutex
	nodeID   string
	grpcAddr string
	httpAddr string
	leaderID string
	// term is the current leader term. Monotonically increasing.
	// Incremented on each leader election. Used to reject messages
	// from nodes with stale terms.
	term uint64
	// votedFor records who this node last elected as leader in the current term.
	// Used to prevent double-voting within a single term.
	votedFor          string
	clusterID         string
	members           map[string]*discoveryv1.NodeInfo
	lastSeen          map[string]time.Time
	joinAttempts      map[string]time.Time
	joinAttemptCounts map[string]int
	requireTLS        bool
	evictionTimeout   time.Duration
	watchers          map[chan *discoveryv1.ClusterEvent]struct{}
	stopCh            chan struct{}
	reg               *registry.Registry
	joinToken         string
	// moduleIDs returns the current list of module IDs running on this node.
	moduleIDs func() []string
	// dialOpts are gRPC dial options used by the heartbeat sender to connect
	// to peer nodes. Set via StartHeartbeatLoop or left nil.
	dialOpts []grpc.DialOption
	// connPool is an optional gRPC connection pool for heartbeat efficiency.
	// When set, heartbeats reuse connections instead of creating new ones.
	connPool *ConnPool
	// requireAuth gates query methods (FindByCapability/FindByRole/Resolve/
	// Members/Watch) behind caller authentication. When false (default),
	// these methods are open. When true, requests without a caller identity
	// in context are rejected with codes.Unauthenticated.
	requireAuth bool
	// maxWatchers is the maximum number of concurrent Watch streams. Default 100.
	maxWatchers int
}

// SetMaxWatchers configures the maximum number of concurrent Watch streams.
// 0 or negative uses the default of 100.
func (s *DiscoveryServer) SetMaxWatchers(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maxWatchers = n
}

// SetConnPool attaches a connection pool for heartbeat and cross-node routing.
func (s *DiscoveryServer) SetConnPool(p *ConnPool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connPool = p
}

// SetRequireAuth controls whether discovery query methods require an
// authenticated caller. When true, Watch/Members/FindBy* return
// Unauthenticated for requests with no caller identity.
func (s *DiscoveryServer) SetRequireAuth(require bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requireAuth = require
}

// checkAuth returns an error if requireAuth is set and the caller has no
// identity in context (no x-caller-id metadata and no mTLS identity).
func (s *DiscoveryServer) checkAuth(ctx context.Context) error {
	s.mu.RLock()
	requireAuth := s.requireAuth
	s.mu.RUnlock()
	if !requireAuth {
		return nil
	}
	callerID := callerid.Get(ctx)
	if callerID == "" {
		return status.Error(codes.Unauthenticated,
			"discovery: authentication required — set MUXCORE_GRPC_REQUIRE_DISCOVERY_AUTH=false or deploy an auth module")
	}
	return nil
}

// NewDiscoveryServer creates a discovery server. The leader is initially empty;
// it is set when the first node joins or when this node forms a cluster.
func NewDiscoveryServer(nodeID, grpcAddr, httpAddr, joinToken string, moduleIDs func() []string) *DiscoveryServer {
	ds := &DiscoveryServer{
		nodeID:            nodeID,
		grpcAddr:          grpcAddr,
		httpAddr:          httpAddr,
		members:           make(map[string]*discoveryv1.NodeInfo),
		lastSeen:          make(map[string]time.Time),
		joinAttempts:      make(map[string]time.Time),
		joinAttemptCounts: make(map[string]int),
		requireTLS:        joinToken != "",
		evictionTimeout:   30 * time.Second,
		watchers:          make(map[chan *discoveryv1.ClusterEvent]struct{}),
		stopCh:            make(chan struct{}),
		joinToken:         joinToken,
		moduleIDs:         moduleIDs,
	}
	go ds.evictLoop()
	go ds.joinCleanupLoop()
	return ds
}

// IsLeader reports whether this node is currently the cluster leader.
func (s *DiscoveryServer) IsLeader() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.leaderID == s.nodeID && s.leaderID != ""
}

// LeaderID returns the current leader's node ID, or "" if none.
func (s *DiscoveryServer) LeaderID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.leaderID
}

// Term returns the current leader term.
func (s *DiscoveryServer) Term() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.term
}

func (s *DiscoveryServer) RegisterWithGRPC(srv *grpc.Server) {
	discoveryv1.RegisterDiscoveryServiceServer(srv, s)
}

// Join handles a cluster join request. The first node to join becomes the
// initial leader. If the joining node has a lower ID than the current leader,
// leadership transfers to the new node (deterministic rank-based election).
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
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.joinToken)) != 1 {
			return nil, status.Error(codes.PermissionDenied, "invalid join token")
		}
	}

	s.mu.Lock()
	s.members[node.GetId()] = node
	s.lastSeen[node.GetId()] = time.Now()

	wasLeader := s.leaderID
	s.electLeaderLocked()

	if s.clusterID == "" {
		s.clusterID = uuid.New().String()
	}

	event := &discoveryv1.ClusterEvent{
		Type:     discoveryv1.ClusterEvent_TYPE_NODE_JOINED,
		Node:     node,
		LeaderId: s.leaderID,
	}
	s.broadcastEventLocked(event)

	if s.leaderID != wasLeader && s.leaderID != "" {
		leaderEvent := &discoveryv1.ClusterEvent{
			Type:     discoveryv1.ClusterEvent_TYPE_LEADER_CHANGED,
			Node:     s.members[s.leaderID],
			LeaderId: s.leaderID,
		}
		s.broadcastEventLocked(leaderEvent)
		slog.Info("leader elected",
			"leader_id", s.leaderID,
			"term", s.term,
			"trigger", "join",
			"joined_node", node.GetId(),
		)
	}

	currentTerm := s.term
	leaderID := s.leaderID

	memberList := make([]*discoveryv1.NodeInfo, 0, len(s.members))
	for _, m := range s.members {
		memberList = append(memberList, m)
	}
	s.mu.Unlock()

	resp := &discoveryv1.JoinResponse{
		Members:   memberList,
		LeaderId:  leaderID,
		ClusterId: s.clusterID,
	}

	// Propagate term via gRPC response metadata.
	if err := grpc.SetHeader(ctx, metadata.Pairs(mdKeyTerm, fmt.Sprintf("%d", currentTerm))); err != nil {
		slog.Warn("discovery: failed to set term header", "error", err)
	}

	return resp, nil
}

// Leave gracefully removes a node from the cluster and triggers leader
// re-election if the departing node was the leader.
func (s *DiscoveryServer) Leave(ctx context.Context, req *discoveryv1.LeaveRequest) (*discoveryv1.LeaveResponse, error) {
	nodeID := req.GetNodeId()
	s.mu.Lock()
	node, existed := s.members[nodeID]
	delete(s.members, nodeID)
	delete(s.lastSeen, nodeID)

	wasLeader := s.leaderID
	if s.leaderID == nodeID {
		s.leaderID = ""
		s.votedFor = ""
		s.electLeaderLocked()
	}

	if existed && node != nil {
		event := &discoveryv1.ClusterEvent{
			Type:     discoveryv1.ClusterEvent_TYPE_NODE_LEFT,
			Node:     node,
			LeaderId: s.leaderID,
		}
		s.broadcastEventLocked(event)

		if s.leaderID != wasLeader && s.leaderID != "" {
			leaderEvent := &discoveryv1.ClusterEvent{
				Type:     discoveryv1.ClusterEvent_TYPE_LEADER_CHANGED,
				Node:     s.members[s.leaderID],
				LeaderId: s.leaderID,
			}
			s.broadcastEventLocked(leaderEvent)
			slog.Info("leader re-elected after node departure",
				"leader_id", s.leaderID,
				"term", s.term,
				"departed_node", nodeID,
			)
		}
	}
	s.mu.Unlock()
	return &discoveryv1.LeaveResponse{}, nil
}

// Heartbeat processes a peer heartbeat. It updates lastSeen, syncs the module
// list, and reconciles the term. If the peer has a higher term, this node
// recognises the peer's leader. If the peer has a stale term, the response
// carries the current term so the peer can catch up.
func (s *DiscoveryServer) Heartbeat(ctx context.Context, req *discoveryv1.HeartbeatRequest) (*discoveryv1.HeartbeatResponse, error) {
	nodeID := req.GetNodeId()
	if nodeID == "" {
		return nil, status.Error(codes.InvalidArgument, "node ID is required")
	}

	// Extract peer's term from gRPC metadata.
	peerTerm := extractTermFromMetadata(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.lastSeen[nodeID] = time.Now()

	if node, exists := s.members[nodeID]; exists && len(req.GetModules()) > 0 {
		node.Modules = req.GetModules()
	}

	if _, exists := s.members[nodeID]; !exists {
		return nil, status.Error(codes.NotFound, "unknown node ID — join the cluster before sending heartbeats")
	}

	// Term reconciliation: if the peer claims a higher term, recognise its
	// leader and update our term. This handles the case where another node
	// initiated an election and we haven't caught up via watchers yet.
	if peerTerm > s.term {
		slog.Info("heartbeat with higher term — updating term",
			"peer", nodeID,
			"peer_term", peerTerm,
			"local_term", s.term,
		)
		s.term = peerTerm
		// Reset vote since we're in a new term.
		s.votedFor = ""
		// Run election to converge on the correct leader for this term.
		s.electLeaderLocked()
	}

	leaderID := s.leaderID
	currentTerm := s.term

	resp := &discoveryv1.HeartbeatResponse{
		LeaderId: leaderID,
	}

	// Propagate term via gRPC response metadata.
	if err := grpc.SetHeader(ctx, metadata.Pairs(mdKeyTerm, fmt.Sprintf("%d", currentTerm))); err != nil {
		slog.Warn("discovery: failed to set term header on heartbeat response", "error", err)
	}

	return resp, nil
}

func (s *DiscoveryServer) Members(ctx context.Context, req *discoveryv1.MembersRequest) (*discoveryv1.MembersResponse, error) {
	if err := s.checkAuth(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	memberList := make([]*discoveryv1.NodeInfo, 0, len(s.members))
	for _, m := range s.members {
		memberList = append(memberList, m)
	}
	leaderID := s.leaderID
	currentTerm := s.term
	s.mu.RUnlock()

	if err := grpc.SetHeader(ctx, metadata.Pairs(mdKeyTerm, fmt.Sprintf("%d", currentTerm))); err != nil {
		slog.Warn("discovery: failed to set term header on members response", "error", err)
	}

	return &discoveryv1.MembersResponse{
		Members:  memberList,
		LeaderId: leaderID,
	}, nil
}

func (s *DiscoveryServer) Watch(req *discoveryv1.MembersRequest, stream discoveryv1.DiscoveryService_WatchServer) error {
	if err := s.checkAuth(stream.Context()); err != nil {
		return err
	}
	ch := make(chan *discoveryv1.ClusterEvent, 16)
	s.mu.Lock()
	maxWatchers := s.maxWatchers
	if maxWatchers <= 0 {
		maxWatchers = 100
	}
	if len(s.watchers) >= maxWatchers {
		s.mu.Unlock()
		return status.Errorf(codes.ResourceExhausted, "too many watchers (max %d)", maxWatchers)
	}
	s.watchers[ch] = struct{}{}
	// Send current leader info on watch start so clients get initial state.
	currentLeader := s.leaderID
	currentTerm := s.term
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.watchers, ch)
		s.mu.Unlock()
	}()

	// Send initial snapshot via metadata.
	if err := stream.SetHeader(metadata.Pairs(
		mdKeyTerm, fmt.Sprintf("%d", currentTerm),
		"x-cluster-leader-id", currentLeader,
	)); err != nil {
		slog.Warn("discovery: failed to set watch stream header", "error", err)
	}

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
// members. Heartbeats carry the current module list and term so peers learn
// about module registrations and leadership changes.
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
	currentTerm := s.term
	s.mu.RUnlock()

	// Use connection pool if available for heartbeat efficiency.
	pool := s.connPool

	for _, p := range peers {
		dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)

		// Propagate term via outgoing metadata on each heartbeat.
		dialCtx = metadata.NewOutgoingContext(dialCtx,
			metadata.Pairs(mdKeyTerm, fmt.Sprintf("%d", currentTerm)),
		)

		var conn *grpc.ClientConn
		var dialErr error
		if pool != nil {
			conn, dialErr = pool.Get(p.addr)
		} else {
			conn, dialErr = grpc.NewClient(p.addr, s.dialOpts...)
		}
		if dialErr != nil {
			slog.Warn("heartbeat: dial peer", "peer", p.id, "addr", p.addr, "error", dialErr)
			cancel()
			continue
		}
		client := discoveryv1.NewDiscoveryServiceClient(conn)
		var respHeader metadata.MD
		resp, err := client.Heartbeat(dialCtx, &discoveryv1.HeartbeatRequest{
			NodeId:  s.nodeID,
			Modules: moduleIDs,
		}, grpc.Header(&respHeader))
		if err != nil {
			slog.Warn("heartbeat: rpc failed", "peer", p.id, "error", err)
		} else if resp != nil {
			// Reconcile term from heartbeat response headers — the peer may
			// have a higher term, indicating a leader change we haven't seen.
			respTerm := extractTermFromMD(respHeader)
			if respTerm > currentTerm {
				s.mu.Lock()
				if respTerm > s.term {
					slog.Info("heartbeat response with higher term — updating",
						"peer", p.id,
						"peer_term", respTerm,
						"local_term", s.term,
					)
					s.term = respTerm
					s.votedFor = ""
					s.electLeaderLocked()
				}
				s.mu.Unlock()
			}
			// Also reconcile leader: if the peer reports a different leader,
			// and the peer is at an equal or higher term, adopt their leader.
			if resp.LeaderId != "" {
				s.mu.Lock()
				if s.leaderID != resp.LeaderId {
					slog.Info("heartbeat response indicates different leader — reconciling",
						"peer", p.id,
						"peer_leader", resp.LeaderId,
						"local_leader", s.leaderID,
					)
					s.leaderID = resp.LeaderId
				}
				s.mu.Unlock()
			}
		}
		// Don't close the conn — the pool manages its lifecycle.
		if pool == nil {
			conn.Close()
		}
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

// joinCleanupLoop periodically purges stale join attempt records.
func (s *DiscoveryServer) joinCleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.joinMu.Lock()
			cutoff := time.Now().Add(-10 * time.Minute)
			for addr, t := range s.joinAttempts {
				if t.Before(cutoff) {
					delete(s.joinAttempts, addr)
					delete(s.joinAttemptCounts, addr)
				}
			}
			s.joinMu.Unlock()
		}
	}
}

// evictDeadNodes removes nodes that haven't sent a heartbeat within the
// eviction timeout window. If the evicted node was the leader, a new
// leader election is triggered. Caller must NOT hold the lock.
func (s *DiscoveryServer) evictDeadNodes() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	leaderEvicted := false

	for id, seen := range s.lastSeen {
		if now.Sub(seen) <= s.evictionTimeout {
			continue
		}
		node, existed := s.members[id]
		delete(s.members, id)
		delete(s.lastSeen, id)
		if s.leaderID == id {
			s.leaderID = ""
			s.votedFor = ""
			leaderEvicted = true
		}
		if existed && node != nil {
			event := &discoveryv1.ClusterEvent{
				Type:     discoveryv1.ClusterEvent_TYPE_NODE_LEFT,
				Node:     node,
				LeaderId: s.leaderID,
			}
			s.broadcastEventLocked(event)
			slog.Warn("node evicted — missed heartbeat deadline",
				"node_id", id,
				"last_seen", seen.Format(time.RFC3339),
			)
		}
	}

	// If the leader was evicted, elect a new one.
	if leaderEvicted {
		oldTerm := s.term
		s.electLeaderLocked()
		if s.leaderID != "" {
			leaderEvent := &discoveryv1.ClusterEvent{
				Type:     discoveryv1.ClusterEvent_TYPE_LEADER_CHANGED,
				Node:     s.members[s.leaderID],
				LeaderId: s.leaderID,
			}
			s.broadcastEventLocked(leaderEvent)
			slog.Info("leader re-elected after eviction",
				"leader_id", s.leaderID,
				"old_term", oldTerm,
				"new_term", s.term,
			)
		}
	}
}

// --- Leader election ---

// electLeaderLocked runs a deterministic rank-based election.
// The member with the alphabetically lowest node ID wins.
// Increments the term. Caller MUST hold s.mu write lock.
func (s *DiscoveryServer) electLeaderLocked() {
	if len(s.members) == 0 {
		s.leaderID = ""
		s.term++
		return
	}

	// Collect surviving member IDs and sort.
	ids := make([]string, 0, len(s.members))
	for id := range s.members {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	newLeader := ids[0] // lowest ID wins (deterministic)

	// Increment term for this election.
	s.term++
	s.votedFor = newLeader
	s.leaderID = newLeader
}

// broadcastEventLocked sends a cluster event to all registered watchers.
// Non-blocking: slow watchers are skipped (event dropped for them).
// Caller MUST hold s.mu.
func (s *DiscoveryServer) broadcastEventLocked(event *discoveryv1.ClusterEvent) {
	for ch := range s.watchers {
		select {
		case ch <- event:
		default:
			// Slow watcher — drop event rather than blocking the cluster.
		}
	}
}

// --- Term propagation helpers ---

// extractTermFromMetadata reads the cluster term from incoming gRPC context
// metadata (used server-side to read terms sent by peers).
func extractTermFromMetadata(ctx context.Context) uint64 {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return 0
	}
	return extractTermFromMD(md)
}

// extractTermFromMD reads the cluster term from a raw metadata.MD map.
// Used client-side to read terms from response headers returned by peers.
func extractTermFromMD(md metadata.MD) uint64 {
	vals := md.Get(mdKeyTerm)
	if len(vals) == 0 {
		return 0
	}
	t, err := strconv.ParseUint(vals[0], 10, 64)
	if err != nil {
		return 0
	}
	return t
}

// MembersSnapshot returns a copy of the current member list.
// Safe for external consumers that need a stable view of the cluster.
func (s *DiscoveryServer) MembersSnapshot() []*discoveryv1.NodeInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	members := make([]*discoveryv1.NodeInfo, 0, len(s.members))
	for _, m := range s.members {
		members = append(members, m)
	}
	return members
}

// Close cleanly stops the eviction goroutine.
func (s *DiscoveryServer) Close() {
	close(s.stopCh)
}
