// Multi-region tenant placement: map tenants to preferred regions and pick
// cluster nodes by label (region=us-east, etc.).
//
// Env:
//
//	MUXCORE_TENANT_REGION_MAP — JSON {"tenant-a":"us-east","tenant-b":"eu-west"}
//	MUXCORE_NODE_REGION — this node's region label (advertised on mesh join)
package tenant

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// NodeFinder resolves cluster nodes by label (implemented by grpcmesh.TenantNodeFinder).
type NodeFinder interface {
	FindNodesByLabel(ctx context.Context, label, value string) []NodeRef
}

// NodeRef is the minimal node fields placement needs.
type NodeRef struct {
	ID       string
	GRPCAddr string
	Region   string
}

// Placer maps tenants to regions and selects remote gRPC endpoints.
type Placer struct {
	// RegionMap is tenant_id → preferred region label value.
	RegionMap map[string]string
	// LocalRegion is this process node's region (MUXCORE_NODE_REGION).
	LocalRegion string
	// LabelKey is the mesh node label for region (default "region").
	LabelKey string
}

// ParseRegionMap parses MUXCORE_TENANT_REGION_MAP JSON.
func ParseRegionMap(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]string{}, nil
	}
	var obj map[string]string
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return nil, fmt.Errorf("MUXCORE_TENANT_REGION_MAP: %w", err)
	}
	out := make(map[string]string, len(obj))
	for k, v := range obj {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k != "" && v != "" {
			out[k] = v
		}
	}
	return out, nil
}

// PlacerFromEnv loads region map and local region from the environment.
func PlacerFromEnv() (*Placer, error) {
	m, err := ParseRegionMap(os.Getenv("MUXCORE_TENANT_REGION_MAP"))
	if err != nil {
		return nil, err
	}
	return &Placer{
		RegionMap:   m,
		LocalRegion: strings.TrimSpace(os.Getenv("MUXCORE_NODE_REGION")),
		LabelKey:    "region",
	}, nil
}

// HasMap reports whether any tenant→region mappings exist.
func (p *Placer) HasMap() bool {
	return p != nil && len(p.RegionMap) > 0
}

// PreferredRegion returns the region for tenantID, or local/default fallback.
func (p *Placer) PreferredRegion(tenantID string) string {
	if p == nil {
		return ""
	}
	id := OrDefault(tenantID)
	if id == "" {
		id = strings.TrimSpace(tenantID)
	}
	if r, ok := p.RegionMap[id]; ok && r != "" {
		return r
	}
	if p.LocalRegion != "" {
		return p.LocalRegion
	}
	return "default"
}

// ResolveGRPC picks a remote muxcored gRPC address for the tenant's region.
// Returns ("", false) when placement cannot resolve a node.
func (p *Placer) ResolveGRPC(ctx context.Context, finder NodeFinder, tenantID string) (string, bool) {
	if !Enabled() || p == nil || finder == nil {
		return "", false
	}
	key := p.LabelKey
	if key == "" {
		key = "region"
	}
	region := p.PreferredRegion(tenantID)
	nodes := finder.FindNodesByLabel(ctx, key, region)
	for _, n := range nodes {
		addr := strings.TrimSpace(n.GRPCAddr)
		if addr != "" {
			return addr, true
		}
	}
	// Fallback: any node in local region when tenant unmapped
	if region != p.LocalRegion && p.LocalRegion != "" {
		for _, n := range finder.FindNodesByLabel(ctx, key, p.LocalRegion) {
			addr := strings.TrimSpace(n.GRPCAddr)
			if addr != "" {
				return addr, true
			}
		}
	}
	return "", false
}

// ResolveDial combines region placement with the tenant cluster map router.
// Placement wins when it resolves a node; otherwise falls back to Router.
func ResolveDial(ctx context.Context, finder NodeFinder, router *Router, tenantID string) (string, error) {
	if placer, err := PlacerFromEnv(); err != nil {
		return "", err
	} else if placer.HasMap() {
		if addr, ok := placer.ResolveGRPC(ctx, finder, tenantID); ok {
			return addr, nil
		}
	}
	if router != nil {
		return router.ResolveDial(WithID(ctx, tenantID))
	}
	return "", nil
}
