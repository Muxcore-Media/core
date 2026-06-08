package grpcmesh

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// newDS builds a minimal DiscoveryServer for unit testing the election logic.
// It does not start the eviction goroutine.
func newDS(nodeID string, memberIDs ...string) *DiscoveryServer {
	ds := &DiscoveryServer{
		nodeID:            nodeID,
		grpcAddr:          ":9090",
		httpAddr:          ":8080",
		members:           make(map[string]*discoveryv1.NodeInfo),
		lastSeen:          make(map[string]time.Time),
		joinAttempts:      make(map[string]time.Time),
		joinAttemptCounts: make(map[string]int),
		watchers:          make(map[chan *discoveryv1.ClusterEvent]struct{}),
		stopCh:            make(chan struct{}),
		evictionTimeout:   30 * time.Second,
	}
	for _, id := range memberIDs {
		ds.members[id] = &discoveryv1.NodeInfo{Id: id, GrpcAddr: ":9090", HttpAddr: ":8080"}
		ds.lastSeen[id] = time.Now()
	}
	return ds
}

func TestElectLeaderLocked_NoMembers(t *testing.T) {
	ds := newDS("node-a")
	ds.electLeaderLocked()
	if ds.leaderID != "" {
		t.Errorf("expected empty leaderID with no members, got %q", ds.leaderID)
	}
	if ds.term != 1 {
		t.Errorf("expected term=1 after election with no members, got %d", ds.term)
	}
}

func TestElectLeaderLocked_SingleMember(t *testing.T) {
	ds := newDS("node-a", "node-a")
	ds.electLeaderLocked()
	if ds.leaderID != "node-a" {
		t.Errorf("expected leader node-a, got %q", ds.leaderID)
	}
	if ds.term != 1 {
		t.Errorf("expected term=1, got %d", ds.term)
	}
}

func TestElectLeaderLocked_LowestIDWins(t *testing.T) {
	ds := newDS("node-c", "node-c", "node-a", "node-b")
	ds.electLeaderLocked()
	if ds.leaderID != "node-a" {
		t.Errorf("expected lowest-ID node-a to win, got %q", ds.leaderID)
	}
}

func TestElectLeaderLocked_TermIncrements(t *testing.T) {
	ds := newDS("node-a", "node-a")
	ds.term = 5
	ds.electLeaderLocked()
	if ds.term != 6 {
		t.Errorf("expected term 6 after election, got %d", ds.term)
	}
}

func TestElectLeaderLocked_VotedFor(t *testing.T) {
	ds := newDS("node-x", "node-b", "node-a")
	ds.electLeaderLocked()
	if ds.votedFor != "node-a" {
		t.Errorf("expected votedFor=node-a, got %q", ds.votedFor)
	}
}

func TestElectLeaderLocked_Deterministic(t *testing.T) {
	// Run election 10 times; must always produce the same result.
	for i := 0; i < 10; i++ {
		ds := newDS("self", "node-c", "node-a", "node-b")
		ds.electLeaderLocked()
		if ds.leaderID != "node-a" {
			t.Errorf("iteration %d: expected node-a, got %q", i, ds.leaderID)
		}
	}
}

func TestIsLeader(t *testing.T) {
	ds := newDS("node-a")
	ds.leaderID = "node-a"
	if !ds.IsLeader() {
		t.Error("expected IsLeader()=true when nodeID==leaderID")
	}
	ds.leaderID = "node-b"
	if ds.IsLeader() {
		t.Error("expected IsLeader()=false when nodeID!=leaderID")
	}
	ds.leaderID = ""
	if ds.IsLeader() {
		t.Error("expected IsLeader()=false with empty leaderID")
	}
}

func TestLeaderID(t *testing.T) {
	ds := newDS("self")
	ds.leaderID = "node-x"
	if ds.LeaderID() != "node-x" {
		t.Errorf("LeaderID()=%q, want node-x", ds.LeaderID())
	}
}

func TestTerm(t *testing.T) {
	ds := newDS("self")
	ds.term = 42
	if ds.Term() != 42 {
		t.Errorf("Term()=%d, want 42", ds.Term())
	}
}

func TestMembersSnapshot(t *testing.T) {
	ds := newDS("self", "node-a", "node-b")
	snap := ds.MembersSnapshot()
	if len(snap) != 2 {
		t.Errorf("expected 2 members in snapshot, got %d", len(snap))
	}
}

func TestExtractTermFromMD_Empty(t *testing.T) {
	var md metadata.MD
	if extractTermFromMD(md) != 0 {
		t.Error("nil/empty MD should return term=0")
	}
}

func TestExtractTermFromMD_Valid(t *testing.T) {
	md := metadata.Pairs(mdKeyTerm, "7")
	if got := extractTermFromMD(md); got != 7 {
		t.Errorf("expected term=7, got %d", got)
	}
}

func TestExtractTermFromMD_Invalid(t *testing.T) {
	md := metadata.Pairs(mdKeyTerm, "not-a-number")
	if got := extractTermFromMD(md); got != 0 {
		t.Errorf("expected term=0 for invalid value, got %d", got)
	}
}

func TestExtractTermFromMD_Missing(t *testing.T) {
	md := metadata.Pairs("other-key", "42")
	if got := extractTermFromMD(md); got != 0 {
		t.Errorf("expected term=0 when key absent, got %d", got)
	}
}

func TestEvictDeadNodes_LeaderEvictionTriggersReelection(t *testing.T) {
	ds := newDS("node-b", "node-a", "node-b")
	ds.leaderID = "node-a"
	ds.term = 1

	// Simulate eviction: remove node-a, clear leader, re-elect.
	ds.mu.Lock()
	delete(ds.members, "node-a")
	delete(ds.lastSeen, "node-a")
	ds.leaderID = ""
	ds.votedFor = ""
	ds.electLeaderLocked()
	ds.mu.Unlock()

	if ds.leaderID != "node-b" {
		t.Errorf("expected node-b elected after node-a evicted, got %q", ds.leaderID)
	}
	if ds.term <= 1 {
		t.Errorf("expected term > 1 after re-election, got %d", ds.term)
	}
}

func TestSetRegistry(t *testing.T) {
	ds := newDS("self")
	if ds.reg != nil {
		t.Error("expected nil registry initially")
	}
	ds.SetRegistry(nil) // Should not panic.
}

func TestSetConnPool(t *testing.T) {
	ds := newDS("self")
	pool := NewConnPool()
	defer pool.Close()
	ds.SetConnPool(pool)
	if ds.connPool != pool {
		t.Error("expected connPool to be set")
	}
}

// --- Join error paths ---

func TestJoin_MissingNodeID(t *testing.T) {
	ds := NewDiscoveryServer("self", ":9090", ":8080", "", nil)
	defer ds.Close()
	_, err := ds.Join(context.Background(), &discoveryv1.JoinRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument for missing node ID, got %v", err)
	}
}

func TestJoin_WrongToken(t *testing.T) {
	ds := NewDiscoveryServer("self", ":9090", ":8080", "correct-token", nil)
	defer ds.Close()

	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("x-cluster-join-token", "wrong-token"))
	req := &discoveryv1.JoinRequest{
		Node: &discoveryv1.NodeInfo{Id: "node-b", GrpcAddr: ":9091", HttpAddr: ":8081"},
	}
	_, err := ds.Join(ctx, req)
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied for wrong token, got %v", err)
	}
}

func TestJoin_CorrectToken(t *testing.T) {
	ds := NewDiscoveryServer("self", ":9090", ":8080", "correct-token", nil)
	defer ds.Close()

	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("x-cluster-join-token", "correct-token"))
	req := &discoveryv1.JoinRequest{
		Node: &discoveryv1.NodeInfo{Id: "node-b", GrpcAddr: ":9091", HttpAddr: ":8081"},
	}
	resp, err := ds.Join(ctx, req)
	if err != nil {
		t.Fatalf("expected success with correct token, got %v", err)
	}
	if len(resp.Members) == 0 {
		t.Error("expected at least one member in response")
	}
}

func TestJoin_RateLimit(t *testing.T) {
	ds := NewDiscoveryServer("self", ":9090", ":8080", "", nil)
	defer ds.Close()

	// Inject a fake peer address so the rate limiter can key on it.
	ctx := peer.NewContext(context.Background(), &peer.Peer{
		Addr: &net.TCPAddr{IP: net.ParseIP("1.2.3.4"), Port: 1234},
	})
	req := &discoveryv1.JoinRequest{
		Node: &discoveryv1.NodeInfo{Id: "node-x", GrpcAddr: ":9091", HttpAddr: ":8081"},
	}

	// First 3 attempts in the rate-limit window should succeed.
	for i := 0; i < 3; i++ {
		if _, err := ds.Join(ctx, req); err != nil {
			t.Fatalf("attempt %d: expected success, got %v", i+1, err)
		}
	}
	// 4th attempt within the 20s window should be rate-limited.
	_, err := ds.Join(ctx, req)
	if status.Code(err) != codes.ResourceExhausted {
		t.Errorf("expected ResourceExhausted on 4th attempt, got %v", err)
	}
}

// --- Leave ---

func TestLeave_NonMember(t *testing.T) {
	ds := NewDiscoveryServer("self", ":9090", ":8080", "", nil)
	defer ds.Close()
	_, err := ds.Leave(context.Background(), &discoveryv1.LeaveRequest{NodeId: "never-joined"})
	if err != nil {
		t.Errorf("Leave for unknown node should be a no-op, got %v", err)
	}
}

func TestLeave_NonLeaderNode(t *testing.T) {
	ds := newDS("node-a", "node-a", "node-b")
	ds.leaderID = "node-a"
	ds.term = 1

	_, err := ds.Leave(context.Background(), &discoveryv1.LeaveRequest{NodeId: "node-b"})
	if err != nil {
		t.Fatalf("Leave non-leader: %v", err)
	}
	if ds.leaderID != "node-a" {
		t.Errorf("leader changed after non-leader left, got %q", ds.leaderID)
	}
	if _, ok := ds.members["node-b"]; ok {
		t.Error("node-b should have been removed from members")
	}
}

// --- Eviction ---

func TestEvictDeadNodes_EvictsStaleNode(t *testing.T) {
	ds := newDS("node-a", "node-a", "node-b")
	ds.leaderID = "node-a"
	ds.evictionTimeout = 10 * time.Second

	ds.mu.Lock()
	ds.lastSeen["node-b"] = time.Now().Add(-20 * time.Second) // older than timeout
	ds.mu.Unlock()

	ds.evictDeadNodes()

	ds.mu.RLock()
	_, still := ds.members["node-b"]
	ds.mu.RUnlock()

	if still {
		t.Error("expected node-b to be evicted after missing heartbeat deadline")
	}
}

func TestEvictDeadNodes_FreshNodeKept(t *testing.T) {
	ds := newDS("node-a", "node-a", "node-b")
	ds.leaderID = "node-a"
	ds.evictionTimeout = 30 * time.Second

	// Both nodes have a recent heartbeat — neither should be evicted.
	ds.evictDeadNodes()

	ds.mu.RLock()
	_, aOK := ds.members["node-a"]
	_, bOK := ds.members["node-b"]
	ds.mu.RUnlock()

	if !aOK || !bOK {
		t.Error("expected both fresh nodes to survive eviction check")
	}
}

// --- Watch event delivery ---

func TestWatch_EventDelivery(t *testing.T) {
	ds := NewDiscoveryServer("node-a", ":9090", ":8080", "", nil)
	defer ds.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	stream := &fakeWatchStream{ctx: ctx}
	watchDone := make(chan error, 1)
	go func() {
		watchDone <- ds.Watch(&discoveryv1.MembersRequest{}, stream)
	}()

	// Wait briefly for the watcher to register before triggering a join.
	time.Sleep(20 * time.Millisecond)

	ds.Join(context.Background(), &discoveryv1.JoinRequest{
		Node: &discoveryv1.NodeInfo{Id: "node-b", GrpcAddr: ":9091", HttpAddr: ":8081"},
	})

	// Allow event to be delivered, then cancel to stop the Watch loop.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-watchDone:
		if err != nil {
			t.Errorf("Watch returned unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Watch goroutine did not stop after context cancel")
	}

	stream.mu.Lock()
	n := len(stream.events)
	stream.mu.Unlock()
	if n == 0 {
		t.Error("expected at least one event delivered to Watch stream after Join")
	}
}

func TestWatch_TooManyWatchers(t *testing.T) {
	ds := newDS("self")
	ds.maxWatchers = 2

	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel1()
	defer cancel2()

	done1, done2 := make(chan error, 1), make(chan error, 1)
	go func() { done1 <- ds.Watch(&discoveryv1.MembersRequest{}, &fakeWatchStream{ctx: ctx1}) }()
	go func() { done2 <- ds.Watch(&discoveryv1.MembersRequest{}, &fakeWatchStream{ctx: ctx2}) }()
	time.Sleep(20 * time.Millisecond)

	// Third watcher should be rejected.
	ctx3, cancel3 := context.WithCancel(context.Background())
	defer cancel3()
	stream3 := &fakeWatchStream{ctx: ctx3}
	err := ds.Watch(&discoveryv1.MembersRequest{}, stream3)
	if status.Code(err) != codes.ResourceExhausted {
		t.Errorf("expected ResourceExhausted when watcher limit reached, got %v", err)
	}

	cancel1()
	cancel2()
}

// fakeWatchStream implements grpc.ServerStreamingServer[*discoveryv1.ClusterEvent]
// (≡ discoveryv1.DiscoveryService_WatchServer) for in-process testing.
type fakeWatchStream struct {
	ctx    context.Context
	events []*discoveryv1.ClusterEvent
	mu     sync.Mutex
}

func (f *fakeWatchStream) Send(e *discoveryv1.ClusterEvent) error {
	f.mu.Lock()
	f.events = append(f.events, e)
	f.mu.Unlock()
	return nil
}
func (f *fakeWatchStream) Context() context.Context       { return f.ctx }
func (f *fakeWatchStream) SetHeader(metadata.MD) error    { return nil }
func (f *fakeWatchStream) SendHeader(metadata.MD) error   { return nil }
func (f *fakeWatchStream) SetTrailer(metadata.MD)         {}
func (f *fakeWatchStream) SendMsg(any) error              { return nil }
func (f *fakeWatchStream) RecvMsg(any) error              { return nil }
