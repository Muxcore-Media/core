package workerpool

import "github.com/Muxcore-Media/core/pkg/contracts"

// ClusterNodeResolver resolves executor module IDs to host node IDs via
// the cluster membership list.
type ClusterNodeResolver struct {
	cluster contracts.Cluster
}

// NewClusterNodeResolver wraps a Cluster as a NodeResolver.
func NewClusterNodeResolver(c contracts.Cluster) *ClusterNodeResolver {
	return &ClusterNodeResolver{cluster: c}
}

// NodeForModule returns the member node ID that advertises moduleID, or ""
// if no current member hosts it.
func (r *ClusterNodeResolver) NodeForModule(moduleID string) string {
	if r == nil || r.cluster == nil || moduleID == "" {
		return ""
	}
	// The local node's module list is read live; the member snapshot can lag
	// behind modules that registered after the last heartbeat.
	local := r.cluster.LocalNode()
	for _, id := range local.ModuleIDs {
		if id == moduleID {
			return local.ID
		}
	}
	for _, m := range r.cluster.Members() {
		for _, id := range m.ModuleIDs {
			if id == moduleID {
				return m.ID
			}
		}
	}
	return ""
}
