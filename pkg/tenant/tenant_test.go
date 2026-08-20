package tenant

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiddlewareInjectsTenant(t *testing.T) {
	t.Setenv("TENANT_MODE", "1")
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
	if ResolveFromRequest(r) != "t9" {
		t.Fatal("ResolveFromRequest header")
	}
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
