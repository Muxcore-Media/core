//go:build integration

package integration

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/internal/grpcmesh"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// startDiscoveryNode starts a gRPC server with a DiscoveryService and
// returns the node. The node self-joins so it appears in its own member list.
func startDiscoveryNode(t *testing.T, id string) *discoveryNode {
	t.Helper()
	var lc net.ListenConfig

	ds := grpcmesh.NewDiscoveryServer(id, "127.0.0.1:0", "127.0.0.1:0", "", nil)
	srv := grpc.NewServer(grpc.Creds(insecure.NewCredentials()))
	ds.RegisterWithGRPC(srv)
	lis, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen %s: %v", id, err)
	}
	go srv.Serve(lis)
	t.Cleanup(srv.GracefulStop)

	// Self-join so this node appears in its own member list.
	selfCtx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("x-caller-id", id))
	ds.Join(selfCtx, &discoveryv1.JoinRequest{
		Node: &discoveryv1.NodeInfo{Id: id, GrpcAddr: lis.Addr().String()},
	})

	return &discoveryNode{
		id:       id,
		grpcAddr: lis.Addr().String(),
		ds:       ds,
	}
}

type discoveryNode struct {
	id       string
	grpcAddr string
	ds       *grpcmesh.DiscoveryServer
}

// TestE2E_ThreeNodeCluster_JoinDiscover verifies a 3-node cluster: nodes
// join via gRPC, all members are visible to the seed, and the lowest-ID
// node is elected leader.
func TestE2E_ThreeNodeCluster_JoinDiscover(t *testing.T) {
	seed := startDiscoveryNode(t, "node-a")
	nb := startDiscoveryNode(t, "node-b")
	nc := startDiscoveryNode(t, "node-c")

	// Helper: join a node to the seed via gRPC.
	joinViaGRPC := func(n *discoveryNode) {
		conn, err := grpc.NewClient(seed.grpcAddr,
			grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatalf("dial seed from %s: %v", n.id, err)
		}
		defer conn.Close()
		client := discoveryv1.NewDiscoveryServiceClient(conn)
		ctx := metadata.NewOutgoingContext(context.Background(),
			metadata.Pairs("x-caller-id", n.id))
		joinCtx, joinCancel := context.WithTimeout(ctx, 5*time.Second)
		defer joinCancel()
		_, err = client.Join(joinCtx, &discoveryv1.JoinRequest{
			Node: &discoveryv1.NodeInfo{Id: n.id, GrpcAddr: n.grpcAddr},
		})
		if err != nil {
			t.Fatalf("join %s: %v", n.id, err)
		}
	}

	joinViaGRPC(nb)
	joinViaGRPC(nc)
	time.Sleep(100 * time.Millisecond)

	// Seed should see all 3 members (itself + b + c).
	members := seed.ds.MembersSnapshot()
	if len(members) != 3 {
		t.Fatalf("expected 3 members on seed, got %d", len(members))
	}
	seen := make(map[string]bool)
	for _, m := range members {
		seen[m.Id] = true
	}
	for _, id := range []string{"node-a", "node-b", "node-c"} {
		if !seen[id] {
			t.Errorf("member %q not found on seed", id)
		}
	}

	// node-a should be leader on the seed (lowest ID among seed's members).
	if leader := seed.ds.LeaderID(); leader != "node-a" {
		t.Errorf("expected leader node-a on seed, got %q", leader)
	}
}

// TestE2E_NodeLeave_UpdatesMemberList verifies that when a node leaves,
// the remaining members remove it from their member list.
func TestE2E_NodeLeave_UpdatesMemberList(t *testing.T) {
	seed := startDiscoveryNode(t, "node-a")
	nb := startDiscoveryNode(t, "node-b")

	// Join node-b to node-a via direct API.
	joinCtx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("x-caller-id", "node-b"))
	seed.ds.Join(joinCtx, &discoveryv1.JoinRequest{
		Node: &discoveryv1.NodeInfo{Id: "node-b", GrpcAddr: nb.grpcAddr},
	})

	if len(seed.ds.MembersSnapshot()) != 2 {
		t.Fatalf("expected 2 members, got %d", len(seed.ds.MembersSnapshot()))
	}

	// node-b leaves via direct API call.
	leaveCtx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("x-caller-id", "node-b"))
	_, err := seed.ds.Leave(leaveCtx, &discoveryv1.LeaveRequest{NodeId: "node-b"})
	if err != nil {
		t.Fatalf("Leave: %v", err)
	}

	members := seed.ds.MembersSnapshot()
	if len(members) != 1 {
		t.Fatalf("expected 1 member after leave, got %d", len(members))
	}
	for _, m := range members {
		if m.Id == "node-b" {
			t.Error("node-b should not be in members after Leave")
		}
	}

	// Leader should still be node-a (only remaining member).
	if leader := seed.ds.LeaderID(); leader != "node-a" {
		t.Errorf("expected leader node-a after node-b leaves, got %q", leader)
	}
}

// TestE2E_LeaderLeave_TriggersReElection verifies that when the leader
// leaves, the remaining nodes elect a new leader.
func TestE2E_LeaderLeave_TriggersReElection(t *testing.T) {
	seed := startDiscoveryNode(t, "node-a")
	nb := startDiscoveryNode(t, "node-b")

	// Create a 2-node cluster: node-a (leader/lowest) and node-b.
	joinCtx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("x-caller-id", "node-b"))
	seed.ds.Join(joinCtx, &discoveryv1.JoinRequest{
		Node: &discoveryv1.NodeInfo{Id: "node-b", GrpcAddr: nb.grpcAddr},
	})

	if leader := seed.ds.LeaderID(); leader != "node-a" {
		t.Fatalf("expected leader node-a, got %q", leader)
	}

	// Leader (node-a) leaves.
	leaveCtx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("x-caller-id", "node-a"))
	_, err := seed.ds.Leave(leaveCtx, &discoveryv1.LeaveRequest{NodeId: "node-a"})
	if err != nil {
		t.Fatalf("Leave: %v", err)
	}

	// node-b should be the new leader (only remaining member).
	if leader := seed.ds.LeaderID(); leader != "node-b" {
		t.Errorf("expected new leader node-b, got %q", leader)
	}

	if len(seed.ds.MembersSnapshot()) != 1 {
		t.Errorf("expected 1 member after leader leaves, got %d", len(seed.ds.MembersSnapshot()))
	}
}
