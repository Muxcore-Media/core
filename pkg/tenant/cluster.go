// Multi-cluster SaaS networking scaffold: map tenant_id → remote core gRPC endpoint(s).
//
// Env:
//
//	MUXCORE_TENANT_CLUSTER_MAP — JSON object, e.g. {"tenant-a":"host:9090","tenant-b":"h2:9090,h3:9090"}
//	MUXCORE_TENANT_CLUSTER_STRICT=1 — fail closed when a mapped remote is unreachable
//	MUXCORE_TENANT_ID — process-scoped tenant when ctx has no tenant_id (module dials)
package tenant

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// Router maps tenant IDs to remote muxcored gRPC addresses.
type Router struct { //nolint:govet // router config groups tenant dial settings
	// Map is tenant_id → one or more "host:port" (comma-separated allowed in values).
	Map map[string][]string
	// Strict fails ResolveDial when a mapped remote cannot be dialed (TCP).
	Strict bool
	// DialTimeout for strict reachability checks (default 2s).
	DialTimeout time.Duration
	// Probe optionally overrides net.DialTimeout (tests).
	Probe func(network, address string, timeout time.Duration) (net.Conn, error)
}

var (
	routerOnce sync.Once
	routerEnv  *Router
)

// ParseClusterMap parses JSON {"tenant-a":"host:9090"} or {"t":"a:1,b:2"}.
func ParseClusterMap(raw string) (map[string][]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string][]string{}, nil
	}
	var obj map[string]string
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return nil, fmt.Errorf("MUXCORE_TENANT_CLUSTER_MAP: %w", err)
	}
	out := make(map[string][]string, len(obj))
	for k, v := range obj {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		addrs := splitAddrs(v)
		if len(addrs) == 0 {
			continue
		}
		out[k] = addrs
	}
	return out, nil
}

func splitAddrs(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// RouterFromEnv loads the cluster map from MUXCORE_TENANT_CLUSTER_MAP.
func RouterFromEnv() (*Router, error) {
	m, err := ParseClusterMap(os.Getenv("MUXCORE_TENANT_CLUSTER_MAP"))
	if err != nil {
		return nil, err
	}
	return &Router{
		Map:    m,
		Strict: os.Getenv("MUXCORE_TENANT_CLUSTER_STRICT") == "1",
	}, nil
}

// CachedRouter returns a process-wide router from env (re-parsed once).
func CachedRouter() *Router {
	routerOnce.Do(func() {
		r, err := RouterFromEnv()
		if err != nil {
			routerEnv = &Router{Map: map[string][]string{}}
			return
		}
		routerEnv = r
	})
	return routerEnv
}

// ResetCachedRouter clears the process cache (tests).
func ResetCachedRouter() {
	routerOnce = sync.Once{}
	routerEnv = nil
}

// HasMap reports whether any tenant→cluster mappings are configured.
func (r *Router) HasMap() bool {
	return r != nil && len(r.Map) > 0
}

// Endpoints returns configured remote addresses for tenantID (empty if local/unmapped).
func (r *Router) Endpoints(tenantID string) []string {
	if r == nil || len(r.Map) == 0 {
		return nil
	}
	id := OrDefault(tenantID)
	if id == "" {
		id = strings.TrimSpace(tenantID)
	}
	if id == "" {
		return nil
	}
	return append([]string(nil), r.Map[id]...)
}

// ResolveDial returns the remote gRPC address for the tenant in ctx (or
// MUXCORE_TENANT_ID), or "" when the caller should keep the local/default addr.
//
// When TENANT_MODE is off or the map is empty, returns ("", nil).
// When Strict and the tenant is mapped, probes TCP reachability and errors if
// no endpoint accepts a connection.
func (r *Router) ResolveDial(ctx context.Context) (string, error) {
	if !Enabled() || r == nil || !r.HasMap() {
		return "", nil
	}
	tid := IDFrom(ctx)
	if tid == "" {
		tid = strings.TrimSpace(os.Getenv("MUXCORE_TENANT_ID"))
	}
	tid = OrDefault(tid)
	addrs := r.Endpoints(tid)
	if len(addrs) == 0 {
		return "", nil
	}
	if !r.Strict {
		return addrs[0], nil
	}
	timeout := r.DialTimeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	probe := r.Probe
	if probe == nil {
		probe = net.DialTimeout
	}
	var lastErr error
	for _, addr := range addrs {
		conn, err := probe("tcp", addr, timeout)
		if err != nil {
			lastErr = err
			continue
		}
		_ = conn.Close()
		return addr, nil
	}
	return "", fmt.Errorf("tenant cluster strict: remote unreachable for tenant %q: %w", tid, lastErr)
}

// ResolveDialFromEnv is ResolveDial on CachedRouter (module client dial hook).
func ResolveDialFromEnv(ctx context.Context) (string, error) {
	return CachedRouter().ResolveDial(ctx)
}
