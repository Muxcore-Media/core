package grpcmesh

import (
	"testing"
	"time"

	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"google.golang.org/grpc/metadata"
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
