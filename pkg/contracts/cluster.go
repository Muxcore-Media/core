package contracts

import "context"

// Cluster manages node membership, leader election, and peer discovery.
type Cluster interface {
	// Start joins or forms a cluster. If no seed nodes are reachable,
	// the node starts a new single-node cluster.
	Start(ctx context.Context) error

	// Stop gracefully leaves the cluster.
	Stop(ctx context.Context) error

	// Members returns all current cluster members.
	Members() []NodeInfo

	// Leader returns the current leader, or nil if unknown.
	Leader() *NodeInfo

	// LocalNode returns information about this node.
	LocalNode() NodeInfo

	// Events returns a channel that emits cluster membership changes.
	Events() <-chan ClusterEvent

	// Health checks whether the cluster is operational.
	Health(ctx context.Context) error
}

// NodeInfo describes a node in the cluster.
type NodeInfo struct {
	ID        string
	GRPCAddr  string
	HTTPAddr  string
	Labels    map[string]string
	ModuleIDs []string
}

// ClusterEvent is emitted when cluster membership changes.
type ClusterEvent struct {
	Type     ClusterEventType
	Node     NodeInfo
	LeaderID string
}

// ClusterEventType describes the kind of membership change.
type ClusterEventType string

const (
	ClusterNodeJoined    ClusterEventType = "node.joined"
	ClusterNodeLeft      ClusterEventType = "node.left"
	ClusterNodeDegraded  ClusterEventType = "node.degraded"
	ClusterLeaderChanged ClusterEventType = "leader.changed"
)

// Cluster event types for the event bus.
const (
	EventClusterNodeJoined    = "cluster.node.joined"
	EventClusterNodeLeft      = "cluster.node.left"
	EventClusterNodeDegraded  = "cluster.node.degraded"
	EventClusterLeaderChanged = "cluster.leader.changed"
)

// NodeJoinedPayload is the payload for cluster.node.joined events.
type NodeJoinedPayload struct {
	NodeID   string `json:"node_id"`
	GRPCAddr string `json:"grpc_addr"`
	HTTPAddr string `json:"http_addr"`
}

// NodeLeftPayload is the payload for cluster.node.left events.
type NodeLeftPayload struct {
	NodeID string `json:"node_id"`
}

// LeaderChangedPayload is the payload for cluster.leader.changed events.
type LeaderChangedPayload struct {
	PreviousLeader string `json:"previous_leader"`
	NewLeader      string `json:"new_leader"`
}
