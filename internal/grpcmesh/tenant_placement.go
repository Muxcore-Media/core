package grpcmesh

import (
	"context"

	"github.com/Muxcore-Media/core/pkg/contracts"
	"github.com/Muxcore-Media/core/pkg/tenant"
)

// TenantNodeFinder adapts contracts.Cluster to tenant.NodeFinder.
func TenantNodeFinder(c contracts.Cluster) tenant.NodeFinder {
	if c == nil {
		return nil
	}
	return tenantNodeFinder{c: c}
}

type tenantNodeFinder struct {
	c contracts.Cluster
}

func (a tenantNodeFinder) FindNodesByLabel(ctx context.Context, label, value string) []tenant.NodeRef {
	nodes := a.c.FindNodesByLabel(ctx, label, value)
	out := make([]tenant.NodeRef, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, tenant.NodeRef{ID: n.ID, GRPCAddr: n.GRPCAddr, Region: n.Labels["region"]})
	}
	return out
}
