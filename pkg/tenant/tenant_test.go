package tenant

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiddlewareInjectsTenant(t *testing.T) {
	t.Setenv("TENANT_MODE", "1")
	t.Setenv(EnvTrustHeaders, "1")
	var got string
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = IDFrom(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Tenant-ID", "acme")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if got != "acme" {
		t.Fatalf("got %q", got)
	}
}

func TestMiddlewareOffIsPassthrough(t *testing.T) {
	t.Setenv("TENANT_MODE", "0")
	var got string
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = IDFrom(r.Context())
	}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Tenant-ID", "acme")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if got != "" {
		t.Fatalf("expected empty when disabled, got %q", got)
	}
}

func TestFromClaimsAndScopeKey(t *testing.T) {
	if FromClaims(map[string]any{"tenant_id": "org1"}) != "org1" {
		t.Fatal("claims")
	}
	t.Setenv("TENANT_MODE", "1")
	if ScopeKey("t1", "u1") != "t1__u1" {
		t.Fatal(ScopeKey("t1", "u1"))
	}
	if OrDefault("") != "default" || OrDefault("acme") != "acme" {
		t.Fatal("OrDefault")
	}
	if ResolveFromClaims(nil) != "default" {
		t.Fatal("ResolveFromClaims default")
	}
	if ResolveFromClaims(map[string]any{"tenant_id": "org1"}) != "org1" {
		t.Fatal("ResolveFromClaims")
	}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if ResolveFromRequest(r) != "default" {
		t.Fatal("ResolveFromRequest default")
	}
	r.Header.Set("X-Tenant-ID", "t9")
	if ResolveFromRequest(r) != "default" {
		t.Fatal("ResolveFromRequest must ignore X-Tenant-ID without TENANT_TRUST_HEADERS")
	}
	t.Setenv(EnvTrustHeaders, "1")
	if ResolveFromRequest(r) != "t9" {
		t.Fatal("ResolveFromRequest header")
	}
	t.Setenv(EnvTrustHeaders, "")
	t.Setenv("TENANT_MODE", "")
	if ScopeKey("t1", "u1") != "u1" {
		t.Fatal("single-tenant should ignore tenant in key")
	}
	if OrDefault("x") != "" {
		t.Fatal("OrDefault off")
	}
	ctx := WithID(context.Background(), "x")
	if IDFrom(ctx) != "x" {
		t.Fatal(IDFrom(ctx))
	}
}

func TestDataDirPartitions(t *testing.T) {
	t.Setenv("TENANT_MODE", "1")
	a := DataDir("/data", "tenant-a")
	b := DataDir("/data", "tenant-b")
	if a == b {
		t.Fatalf("expected distinct dirs, got %q", a)
	}
	if a != "/data/tenants/tenant-a" || b != "/data/tenants/tenant-b" {
		t.Fatalf("a=%q b=%q", a, b)
	}
	if DataDir("/data", "") != "/data/tenants/default" {
		t.Fatal(DataDir("/data", ""))
	}
	if SafeID("../evil") != "_evil" && SafeID("../evil") != "evil" {
		// path separators become underscores
		got := SafeID("../evil")
		if got == ".." || got == "../evil" {
			t.Fatalf("unsafe SafeID %q", got)
		}
	}
	t.Setenv("TENANT_MODE", "")
	if DataDir("/data", "tenant-a") != "/data" {
		t.Fatal("single-tenant DataDir should be root")
	}
}

func TestGuardCrossTenant(t *testing.T) {
	t.Setenv("TENANT_MODE", "1")
	if err := GuardCrossTenant("a", "a", nil); err != nil {
		t.Fatal(err)
	}
	if err := GuardCrossTenant("a", "b", []string{"user"}); err == nil {
		t.Fatal("expected deny")
	}
	if err := GuardCrossTenant("a", "b", []string{"admin"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TENANT_MODE", "")
	if err := GuardCrossTenant("a", "b", nil); err != nil {
		t.Fatal("disabled should allow")
	}
	if !HasAdminRole([]string{"Admin"}) || HasAdminRole([]string{"user"}) {
		t.Fatal("HasAdminRole")
	}
}

func middlewareTenant(t *testing.T, ctx context.Context, headers map[string]string) string {
	t.Helper()
	var got string
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = IDFrom(r.Context())
	}))
	r := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	h.ServeHTTP(httptest.NewRecorder(), r)
	return got
}

// ADR-0019 §3: client tenant headers are ignored unless TENANT_TRUST_HEADERS=1.
func TestMiddlewareIgnoresUntrustedHeaders(t *testing.T) {
	t.Setenv("TENANT_MODE", "1")
	t.Setenv(EnvTrustHeaders, "")
	bg := context.Background()
	for _, hdr := range []string{"X-Tenant-ID", "X-Auth-Claims-Tenant"} {
		if got := middlewareTenant(t, bg, map[string]string{hdr: "evil"}); got != "default" {
			t.Fatalf("%s honoured without opt-in: %q", hdr, got)
		}
	}
	t.Setenv(EnvTrustHeaders, "true") // only "1" opts in
	if got := middlewareTenant(t, bg, map[string]string{"X-Tenant-ID": "evil"}); got != "default" {
		t.Fatalf("TENANT_TRUST_HEADERS=true must not opt in: %q", got)
	}
	t.Setenv(EnvTrustHeaders, "1")
	if got := middlewareTenant(t, bg, map[string]string{"X-Auth-Claims-Tenant": "claims"}); got != "claims" {
		t.Fatalf("claims header with opt-in: %q", got)
	}
}

func TestAuthenticatedTenantWins(t *testing.T) {
	t.Setenv("TENANT_MODE", "1")
	ctx := WithAuthenticatedTenant(context.Background(), " acme ")
	if id, ok := AuthenticatedTenantFrom(ctx); !ok || id != "acme" {
		t.Fatalf("AuthenticatedTenantFrom = %q, %v", id, ok)
	}
	if IDFrom(ctx) != "acme" {
		t.Fatalf("IDFrom = %q", IDFrom(ctx))
	}
	if _, ok := AuthenticatedTenantFrom(context.Background()); ok {
		t.Fatal("no authenticated tenant expected")
	}
	if WithAuthenticatedTenant(context.Background(), "  ") != context.Background() {
		t.Fatal("empty tenant must leave ctx unchanged")
	}

	for _, trust := range []string{"", "1"} {
		t.Setenv(EnvTrustHeaders, trust)
		if got := middlewareTenant(t, ctx, map[string]string{"X-Tenant-ID": "evil"}); got != "acme" {
			t.Fatalf("trust=%q: middleware tenant %q, want authenticated acme", trust, got)
		}
		r := httptest.NewRequest(http.MethodGet, "/?tenant_id=evil", nil).WithContext(ctx)
		r.Header.Set("X-Tenant-ID", "evil")
		if got := ResolveFromRequest(r); got != "acme" {
			t.Fatalf("trust=%q: ResolveFromRequest %q, want acme", trust, got)
		}
	}
}

func TestResolveFromRequestQueryNeedsOptIn(t *testing.T) {
	t.Setenv("TENANT_MODE", "1")
	t.Setenv(EnvTrustHeaders, "")
	r := httptest.NewRequest(http.MethodGet, "/?tenant_id=q1", nil)
	if got := ResolveFromRequest(r); got != "default" {
		t.Fatalf("query tenant honoured without opt-in: %q", got)
	}
	t.Setenv(EnvTrustHeaders, "1")
	if got := ResolveFromRequest(r); got != "q1" {
		t.Fatalf("query tenant with opt-in: %q", got)
	}
	// A tenant already on the context (WithID) is used without opt-in.
	t.Setenv(EnvTrustHeaders, "")
	r = r.WithContext(WithID(context.Background(), "ctx1"))
	if got := ResolveFromRequest(r); got != "ctx1" {
		t.Fatalf("context tenant: %q", got)
	}
}
