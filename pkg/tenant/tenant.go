// Package tenant provides multi-tenant scaffolding (P3).
//
// When TENANT_MODE=1, request context carries a tenant_id and storage
// backends use per-tenant partitions under data/tenants/{id}/. Auth-local
// binds users/invites to tenant_id.
//
// Tenant identity comes from the authenticated caller (ADR-0019 §3): a
// caller that resolved the user through the identity provider records the
// tenant with WithAuthenticatedTenant, and Middleware / ResolveFromRequest
// use it. Client-supplied X-Tenant-ID, X-Auth-Claims-Tenant and ?tenant_id=
// are ignored unless TENANT_TRUST_HEADERS=1 (legacy, for deployments where a
// trusted proxy sets them).
//
// Multi-cluster SaaS routing is optional via MUXCORE_TENANT_CLUSTER_MAP
// (see cluster.go). Default (unset) is single-tenant household mode.
package tenant

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

var safeSeg = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

type ctxKey struct{}

type authCtxKey struct{}

// EnvTrustHeaders is the opt-in that makes Middleware and ResolveFromRequest
// honour client-supplied tenant headers and the tenant_id query parameter.
const EnvTrustHeaders = "TENANT_TRUST_HEADERS"

var untrustedHeaderWarn sync.Once

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

// TrustHeaders reports whether TENANT_TRUST_HEADERS=1: client-supplied
// X-Tenant-ID, X-Auth-Claims-Tenant and ?tenant_id= are honoured. Off by
// default (ADR-0019 §3).
func TrustHeaders() bool {
	return os.Getenv(EnvTrustHeaders) == "1"
}

// WithAuthenticatedTenant records a tenant that the caller resolved from an
// authenticated identity (for example the tenant claim returned by the
// identity provider for the request's bearer token). It also sets the
// tenant_id returned by IDFrom. Middleware and ResolveFromRequest prefer it
// over any header. An empty tenant leaves ctx unchanged.
func WithAuthenticatedTenant(ctx context.Context, tenant string) context.Context {
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return ctx
	}
	ctx = context.WithValue(ctx, authCtxKey{}, tenant)
	return WithID(ctx, tenant)
}

// AuthenticatedTenantFrom returns the tenant recorded by
// WithAuthenticatedTenant.
func AuthenticatedTenantFrom(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(authCtxKey{}).(string)
	return v, ok && v != ""
}

// headerTenant returns the tenant from client-supplied headers (and, when
// withQuery is set, the tenant_id query parameter) if TENANT_TRUST_HEADERS=1.
// Otherwise it returns "" and logs once when such a header was ignored.
func headerTenant(r *http.Request, withQuery bool) string {
	h := strings.TrimSpace(r.Header.Get("X-Tenant-ID"))
	if h == "" {
		h = strings.TrimSpace(r.Header.Get("X-Auth-Claims-Tenant"))
	}
	if h == "" && withQuery && r.URL != nil {
		h = strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	}
	if h == "" {
		return ""
	}
	if !TrustHeaders() {
		untrustedHeaderWarn.Do(func() {
			slog.Warn("tenant: ignoring client-supplied tenant header; tenant comes from the authenticated identity " +
				"(set TENANT_TRUST_HEADERS=1 only behind a proxy that sets it)")
		})
		return ""
	}
	return h
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

// ResolveFromRequest returns the request's tenant_id when TENANT_MODE=1, in
// order: the authenticated tenant (WithAuthenticatedTenant), a tenant_id
// already on the context (WithID), then — only with TENANT_TRUST_HEADERS=1 —
// X-Tenant-ID, X-Auth-Claims-Tenant and ?tenant_id=. Falls back to
// "default". Returns "" when tenant mode is off.
func ResolveFromRequest(r *http.Request) string {
	if !Enabled() || r == nil {
		return ""
	}
	if id, ok := AuthenticatedTenantFrom(r.Context()); ok {
		return id
	}
	if id := IDFrom(r.Context()); id != "" {
		return id
	}
	if id := headerTenant(r, true); id != "" {
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

// Middleware injects tenant_id into the request context when TENANT_MODE=1:
// the authenticated tenant (WithAuthenticatedTenant, set by an outer
// authentication middleware) when present; otherwise X-Tenant-ID or
// X-Auth-Claims-Tenant, but only with TENANT_TRUST_HEADERS=1; otherwise
// "default". Client-supplied tenant headers are ignored by default
// (ADR-0019 §3).
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !Enabled() {
			next.ServeHTTP(w, r)
			return
		}
		id, ok := AuthenticatedTenantFrom(r.Context())
		if !ok {
			id = headerTenant(r, false)
		}
		if id == "" {
			id = "default"
		}
		ctx := WithID(r.Context(), id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
