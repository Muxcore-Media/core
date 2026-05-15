package cluster

import "time"

// Config holds cluster configuration.
type Config struct {
	// NodeID is a unique identifier for this node. Auto-generated if empty.
	NodeID string `json:"node_id"`

	// GRPCAddr is the address for the gRPC cluster mesh (e.g., ":9090").
	GRPCAddr string `json:"grpc_addr"`

	// HTTPAddr is the HTTP API address, shared with peers for discovery.
	HTTPAddr string `json:"http_addr"`

	// SeedNodes is a list of initial peer addresses to join.
	// If empty and no peers are reachable, the node forms a new cluster.
	SeedNodes []string `json:"seed_nodes"`

	// GossipInterval controls how often membership gossip occurs.
	GossipInterval time.Duration `json:"gossip_interval"`

	// HeartbeatTimeout is the duration after which a silent node is suspected dead.
	HeartbeatTimeout time.Duration `json:"heartbeat_timeout"`

	// Labels are arbitrary key-value pairs attached to this node.
	Labels map[string]string `json:"labels"`
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		GRPCAddr:         ":9090",
		HTTPAddr:         ":8080",
		GossipInterval:   2 * time.Second,
		HeartbeatTimeout: 10 * time.Second,
	}
}
