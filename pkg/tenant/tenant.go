// Package tenant provides multi-tenant scaffolding (P3).
//
// When TENANT_MODE=1, request context carries a tenant_id and storage
// backends use per-tenant partitions under data/tenants/{id}/. Auth-local
// binds users/invites to tenant_id and BFFs forward X-Tenant-ID / claims.
// Multi-cluster SaaS routing is optional via MUXCORE_TENANT_CLUSTER_MAP
// (see cluster.go). Default (unset) is single-tenant household mode.
package tenant

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var safeSeg = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

type ctxKey struct{}

// ErrCrossTenant is returned when a non-admin actor targets another tenant.
var ErrCrossTenant = errors.New("cross-tenant action denied")

// Enabled reports whether TENANT_MODE=1.
func Enabled() bool {
	return os.Getenv("TENANT_MODE") == "1"
}

// WithID attaches tenant_id to ctx.
func WithID(ctx context.Context, id string) context.Context {
	id = strings.TrimSpace(id)
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, id)
}

// IDFrom returns tenant_id from ctx (empty if unset).
func IDFrom(ctx context.Context) string {
	v, _ := ctx.Value(ctxKey{}).(string)
	return v
}

// FromClaims extracts tenant_id from auth claim maps (common keys).
func FromClaims(claims map[string]any) string {
	if claims == nil {
		return ""
	}
	for _, k := range []string{"tenant_id", "tenantId", "tid", "org_id"} {
		if v, ok := claims[k]; ok {
			switch t := v.(type) {
			case string:
				if s := strings.TrimSpace(t); s != "" {
					return s
				}
			}
		}
	}
	return ""
}

// OrDefault returns a storage tenant id when TENANT_MODE=1: trimmed id, or
// "default" when empty. When tenant mode is off, returns "".
func OrDefault(id string) string {
	if !Enabled() {
		return ""
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return "default"
	}
	return id
}

// ResolveFromRequest picks tenant_id from context, headers, query, or claims
// header. When TENANT_MODE=1 and none are set, returns "default".
func ResolveFromRequest(r *http.Request) string {
	if !Enabled() || r == nil {
		return ""
	}
	if id := IDFrom(r.Context()); id != "" {
		return id
	}
	if id := strings.TrimSpace(r.Header.Get("X-Tenant-ID")); id != "" {
		return id
	}
	if id := strings.TrimSpace(r.Header.Get("X-Auth-Claims-Tenant")); id != "" {
		return id
	}
	if id := strings.TrimSpace(r.URL.Query().Get("tenant_id")); id != "" {
		return id
	}
	return "default"
}

// ResolveFromClaims returns OrDefault(FromClaims(claims)).
func ResolveFromClaims(claims map[string]any) string {
	return OrDefault(FromClaims(claims))
}

// HasAdminRole reports whether roles include admin (case-insensitive).
func HasAdminRole(roles []string) bool {
	for _, r := range roles {
		if strings.EqualFold(strings.TrimSpace(r), "admin") {
			return true
		}
	}
	return false
}

// GuardCrossTenant rejects actions where the actor's tenant differs from the
// resource/target tenant unless the actor has the admin role. No-op when
// TENANT_MODE is off.
func GuardCrossTenant(actorTenant, resourceTenant string, roles []string) error {
	if !Enabled() {
		return nil
	}
	actor := OrDefault(actorTenant)
	resource := OrDefault(resourceTenant)
	if actor == resource {
		return nil
	}
	if HasAdminRole(roles) {
		return nil
	}
	return fmt.Errorf("%w: actor=%s resource=%s", ErrCrossTenant, actor, resource)
}

// SafeID sanitizes a tenant id for filesystem path segments.
func SafeID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		id = "default"
	}
	id = safeSeg.ReplaceAllString(id, "_")
	if id == "" || id == "." || id == ".." {
		return "default"
	}
	return id
}

// DataDir returns the storage root for a tenant.
// When TENANT_MODE=1: {root}/tenants/{safeID}. Otherwise: root unchanged.
func DataDir(root, tenantID string) string {
	if !Enabled() {
		return root
	}
	return filepath.Join(root, "tenants", SafeID(OrDefault(tenantID)))
}

// ScopeKey builds a logical partition key (tenant__user when enabled).
// Prefer DataDir + per-user files for on-disk isolation.
func ScopeKey(tenantID, userID string) string {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		userID = "anonymous"
	}
	userID = safeSeg.ReplaceAllString(userID, "_")
	if !Enabled() {
		return userID
	}
	return SafeID(OrDefault(tenantID)) + "__" + userID
}

// Middleware injects tenant_id from X-Tenant-ID or claim header when TENANT_MODE=1.
// Claim header: X-Auth-Claims-Tenant (set by BFF after verifying session/JWT claims).
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !Enabled() {
			next.ServeHTTP(w, r)
			return
		}
		id := strings.TrimSpace(r.Header.Get("X-Tenant-ID"))
		if id == "" {
			id = strings.TrimSpace(r.Header.Get("X-Auth-Claims-Tenant"))
		}
		if id == "" {
			id = "default"
		}
		ctx := WithID(r.Context(), id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
