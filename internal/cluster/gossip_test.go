package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/internal/events"
	"github.com/Muxcore-Media/core/pkg/contracts"
)

func TestGossipCluster_NewSingleNode(t *testing.T) {
	bus := events.NewMemoryBus()
	cfg := Config{
		NodeID:           "node-1",
		GRPCAddr:         ":0",
		HTTPAddr:         ":0",
		GossipInterval:   100 * time.Millisecond,
		HeartbeatTimeout: 500 * time.Millisecond,
	}
	gc := NewGossipCluster(cfg, bus)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := gc.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer gc.Stop(ctx)

	members := gc.Members()
	if len(members) != 1 {
		t.Fatalf("expected 1 member, got %d", len(members))
	}
	if members[0].ID != "node-1" {
		t.Errorf("expected node-1, got %s", members[0].ID)
	}

	leader := gc.Leader()
	if leader == nil {
		t.Fatal("expected leader, got nil")
	}
	if leader.ID != "node-1" {
		t.Errorf("expected leader node-1, got %s", leader.ID)
	}
}

func TestGossipCluster_Health(t *testing.T) {
	bus := events.NewMemoryBus()
	cfg := Config{
		NodeID:           "node-1",
		GRPCAddr:         ":0",
		HTTPAddr:         ":0",
		GossipInterval:   100 * time.Millisecond,
		HeartbeatTimeout: 500 * time.Millisecond,
	}
	gc := NewGossipCluster(cfg, bus)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := gc.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer gc.Stop(ctx)

	if err := gc.Health(ctx); err != nil {
		t.Errorf("health: %v", err)
	}
}

func TestGossipCluster_LeaderElection(t *testing.T) {
	bus := events.NewMemoryBus()

	cfg1 := Config{
		NodeID:           "node-bbb",
		GRPCAddr:         ":0",
		HTTPAddr:         ":0",
		GossipInterval:   100 * time.Millisecond,
		HeartbeatTimeout: 500 * time.Millisecond,
	}
	gc1 := NewGossipCluster(cfg1, bus)

	cfg2 := Config{
		NodeID:           "node-aaa",
		GRPCAddr:         ":0",
		HTTPAddr:         ":0",
		GossipInterval:   100 * time.Millisecond,
		HeartbeatTimeout: 500 * time.Millisecond,
	}
	gc2 := NewGossipCluster(cfg2, bus)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Start both to register themselves in their member maps.
	if err := gc1.Start(ctx); err != nil {
		t.Fatalf("gc1 start: %v", err)
	}
	defer gc1.Stop(ctx)
	if err := gc2.Start(ctx); err != nil {
		t.Fatalf("gc2 start: %v", err)
	}
	defer gc2.Stop(ctx)

	// Exchange member lists (simulating gossip)
	members1 := gc1.Members()
	members2 := gc2.Members()
	gc1.MergeMemberList("node-aaa", members2)
	gc2.MergeMemberList("node-bbb", members1)

	// Both should agree on leader (lowest ID = "node-aaa")
	if l := gc1.Leader(); l == nil || l.ID != "node-aaa" {
		t.Errorf("gc1 leader: got %v, want node-aaa", l)
	}
	if l := gc2.Leader(); l == nil || l.ID != "node-aaa" {
		t.Errorf("gc2 leader: got %v, want node-aaa", l)
	}
}

func TestGossipCluster_MergeMemberList(t *testing.T) {
	bus := events.NewMemoryBus()
	cfg := Config{
		NodeID:           "node-1",
		GRPCAddr:         ":0",
		HTTPAddr:         ":0",
		GossipInterval:   100 * time.Millisecond,
		HeartbeatTimeout: 500 * time.Millisecond,
	}
	gc := NewGossipCluster(cfg, bus)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Start as single node
	if err := gc.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer gc.Stop(ctx)

	// Merge in two peers
	peers := []contracts.NodeInfo{
		{ID: "node-2", GRPCAddr: "10.0.0.2:9090", HTTPAddr: "10.0.0.2:8080"},
		{ID: "node-3", GRPCAddr: "10.0.0.3:9090", HTTPAddr: "10.0.0.3:8080"},
	}
	gc.MergeMemberList("node-2", peers)

	got := gc.Members()
	if len(got) != 3 {
		t.Errorf("expected 3 members, got %d: %v", len(got), got)
	}
}

func TestGossipCluster_EventsChannel(t *testing.T) {
	bus := events.NewMemoryBus()
	cfg := Config{
		NodeID:           "node-1",
		GRPCAddr:         ":0",
		HTTPAddr:         ":0",
		GossipInterval:   100 * time.Millisecond,
		HeartbeatTimeout: 500 * time.Millisecond,
	}
	gc := NewGossipCluster(cfg, bus)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := gc.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer gc.Stop(ctx)

	// Drain the initial event from Start() (the node joining itself).
	<-gc.Events()

	// Merge a peer to trigger a join event
	peer := []contracts.NodeInfo{
		{ID: "node-2", GRPCAddr: "10.0.0.2:9090", HTTPAddr: "10.0.0.2:8080"},
	}
	gc.MergeMemberList("node-2", peer)

	select {
	case ev := <-gc.Events():
		if ev.Type != contracts.ClusterNodeJoined {
			t.Errorf("expected NodeJoined, got %s", ev.Type)
		}
		if ev.Node.ID != "node-2" {
			t.Errorf("expected node-2, got %s", ev.Node.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for cluster event")
	}
}
