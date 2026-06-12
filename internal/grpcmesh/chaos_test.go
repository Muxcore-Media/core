package grpcmesh

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/internal/callerid"
	"github.com/Muxcore-Media/core/pkg/contracts"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
)

// stubCluster implements contracts.Cluster for testing remote routing paths.
type stubCluster struct {
	members []contracts.NodeInfo
}

func (s *stubCluster) Start(context.Context) error           { return nil }
func (s *stubCluster) Stop(context.Context) error            { return nil }
func (s *stubCluster) Members() []contracts.NodeInfo         { return s.members }
func (s *stubCluster) Leader() *contracts.NodeInfo           { return nil }
func (s *stubCluster) LocalNode() contracts.NodeInfo         { return contracts.NodeInfo{} }
func (s *stubCluster) Events() <-chan contracts.ClusterEvent { return nil }
func (s *stubCluster) Health(context.Context) error          { return nil }
func (s *stubCluster) FindNodesByLabel(context.Context, string, string) []contracts.NodeInfo {
	return nil
}
func (s *stubCluster) FindNodesByModule(context.Context, string) []contracts.NodeInfo { return nil }

func TestClient_Call_RemoteNoTransportCreds_ReturnsErr(t *testing.T) {
	srv := NewServer()
	client := NewClient(srv)
	client.SetCallPolicy(stubCallPolicy{allow: true})

	client.SetCluster(&stubCluster{
		members: []contracts.NodeInfo{
			{ID: "node-2", GRPCAddr: "10.0.0.2:9090", ModuleIDs: []string{"target-mod"}},
		},
	})

	_, err := client.Call(context.Background(), "target-mod", "Echo", []byte("hello"))
	if err == nil {
		t.Fatal("expected error for remote call without transport creds")
	}
	if !errors.Is(err, ErrRemoteRoutingUnavailable) {
		t.Errorf("expected ErrRemoteRoutingUnavailable, got: %v", err)
	}
}

func TestClient_Call_RemoteModuleNotInCluster_ReturnsErr(t *testing.T) {
	srv := NewServer()
	client := NewClient(srv)
	client.SetCallPolicy(stubCallPolicy{allow: true})

	client.SetCluster(&stubCluster{
		members: []contracts.NodeInfo{
			{ID: "node-2", GRPCAddr: "10.0.0.2:9090", ModuleIDs: []string{"other-mod"}},
		},
	})

	_, err := client.Call(context.Background(), "target-mod", "Echo", []byte("hello"))
	if err == nil {
		t.Fatal("expected error when module not found in cluster")
	}
	if !errors.Is(err, ErrRemoteRoutingUnavailable) {
		t.Errorf("expected ErrRemoteRoutingUnavailable, got: %v", err)
	}
}

func TestClient_Call_NoCluster_ReturnsErr(t *testing.T) {
	srv := NewServer()
	client := NewClient(srv)
	client.SetCallPolicy(stubCallPolicy{allow: true})

	_, err := client.Call(context.Background(), "target-mod", "Echo", []byte("hello"))
	if err == nil {
		t.Fatal("expected error when no cluster is configured")
	}
	if !errors.Is(err, ErrRemoteRoutingUnavailable) {
		t.Errorf("expected ErrRemoteRoutingUnavailable, got: %v", err)
	}
}

func TestClient_Call_NoCallPolicy_ReturnsErr(t *testing.T) {
	srv := NewServer()
	client := NewClient(srv)

	// No call policy set — should be denied.
	_, err := client.Call(context.Background(), "target-mod", "Echo", []byte("hello"))
	if err == nil {
		t.Fatal("expected error when no call policy is configured")
	}
}

// TestChaos_LeaderElection_UnderPartition verifies that when the leader
// node is evicted (simulating a network partition), the surviving members
// elect a new leader with an incremented term.
func TestChaos_LeaderElection_UnderPartition(t *testing.T) {
	ds := newDS("node-c", "node-a", "node-b", "node-c")
	ds.leaderID = "node-a"
	ds.term = 1

	// Subscribe to watch events. Use callerid.Set to pass auth.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ctx = callerid.Set(ctx, "test")
	stream := &fakeWatchStream{ctx: ctx}
	watchDone := make(chan error, 1)
	go func() {
		watchDone <- ds.Watch(&discoveryv1.MembersRequest{}, stream)
	}()
	time.Sleep(50 * time.Millisecond) // let watcher register

	// Simulate partition: node-a (leader) and node-b go silent.
	ds.mu.Lock()
	ds.lastSeen["node-a"] = time.Now().Add(-40 * time.Second)
	ds.lastSeen["node-b"] = time.Now().Add(-40 * time.Second)
	ds.lastSeen["node-c"] = time.Now() // node-c is alive
	ds.mu.Unlock()

	ds.evictDeadNodes()

	// node-c should be the only remaining member and thus the new leader.
	if ds.leaderID != "node-c" {
		t.Fatalf("expected leader node-c after partition, got %q", ds.leaderID)
	}
	if ds.term <= 1 {
		t.Errorf("expected term > 1 after re-election, got %d", ds.term)
	}

	// Verify a LeaderChanged event was delivered. Poll with timeout
	// since the Watch goroutine may not have flushed to stream yet.
	var found bool
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stream.mu.Lock()
		for _, event := range stream.events {
			if event.GetType() == discoveryv1.ClusterEvent_TYPE_LEADER_CHANGED {
				found = true
				if event.GetLeaderId() != "node-c" {
					t.Errorf("expected new leader node-c, got %q", event.GetLeaderId())
				}
				stream.mu.Unlock()
				goto done
			}
		}
		stream.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
done:
	if !found {
		t.Error("expected LEADER_CHANGED event, not found in recorded events")
	}

	cancel()
	<-watchDone
}

// TestChaos_LeaderElection_MajoritySurvives verifies that after a minority
// node is partitioned away, the leader and majority survive without
// unnecessary re-elections.
func TestChaos_LeaderElection_MajoritySurvives(t *testing.T) {
	ds := newDS("node-a", "node-a", "node-b", "node-c")
	ds.leaderID = "node-a"
	ds.term = 3

	// node-b goes silent (minority partition).
	ds.mu.Lock()
	ds.lastSeen["node-b"] = time.Now().Add(-40 * time.Second)
	ds.mu.Unlock()

	ds.evictDeadNodes()

	// node-b should be evicted.
	if _, ok := ds.members["node-b"]; ok {
		t.Error("expected node-b to be evicted")
	}

	// node-a should remain leader (still the lowest ID).
	if ds.leaderID != "node-a" {
		t.Errorf("expected leader node-a to survive, got %q", ds.leaderID)
	}

	// node-a and node-c should still be members.
	if _, ok := ds.members["node-a"]; !ok {
		t.Error("expected node-a to remain a member")
	}
	if _, ok := ds.members["node-c"]; !ok {
		t.Error("expected node-c to remain a member")
	}
}

// TestChaos_ClockSkew_Forward_Safe verifies that a node with a slightly
// skewed clock (future timestamp) is not evicted — it's treated as
// recently seen.
func TestChaos_ClockSkew_Forward_Safe(t *testing.T) {
	ds := newDS("node-a", "node-a", "node-b")
	ds.evictionTimeout = 30 * time.Second

	// node-b has a lastSeen 5 seconds in the future (clock skew).
	ds.mu.Lock()
	ds.lastSeen["node-b"] = time.Now().Add(5 * time.Second)
	ds.mu.Unlock()

	ds.evictDeadNodes()

	if _, ok := ds.members["node-b"]; !ok {
		t.Error("expected node-b to be kept despite forward clock skew")
	}
}

// TestChaos_ClockSkew_Backward_StillKept verifies that a node with a
// moderately skewed clock (behind by less than the eviction timeout)
// is still kept.
func TestChaos_ClockSkew_Backward_StillKept(t *testing.T) {
	ds := newDS("node-a", "node-a", "node-b")
	ds.evictionTimeout = 30 * time.Second

	// node-b has a lastSeen 25 seconds ago (within 30s timeout).
	ds.mu.Lock()
	ds.lastSeen["node-b"] = time.Now().Add(-25 * time.Second)
	ds.mu.Unlock()

	ds.evictDeadNodes()

	if _, ok := ds.members["node-b"]; !ok {
		t.Error("expected node-b to be kept — 25s < 30s eviction timeout")
	}
}

// TestChaos_ClockSkew_Backward_Evicted verifies that a node with a
// clock behind the eviction timeout is evicted.
func TestChaos_ClockSkew_Backward_Evicted(t *testing.T) {
	ds := newDS("node-a", "node-a", "node-b")
	ds.evictionTimeout = 10 * time.Second

	// node-b has a lastSeen 15 seconds ago (beyond 10s timeout).
	ds.mu.Lock()
	ds.lastSeen["node-b"] = time.Now().Add(-15 * time.Second)
	ds.mu.Unlock()

	ds.evictDeadNodes()

	if _, ok := ds.members["node-b"]; ok {
		t.Error("expected node-b to be evicted — 15s > 10s eviction timeout")
	}
}
