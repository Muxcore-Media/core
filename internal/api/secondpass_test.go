package api

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Muxcore-Media/core/internal/health"
)

func TestVersionHeadersMiddleware(t *testing.T) {
	handler := versionHeadersMiddleware("v1", "1.2.3")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get(headerXAPIVersion); got != "v1" {
		t.Errorf("X-API-Version = %q, want v1", got)
	}
	if got := rec.Header().Get(headerXMuxCoreVer); got != "1.2.3" {
		t.Errorf("X-MuxCore-Version = %q, want 1.2.3", got)
	}
}

func TestIsConfigMutation(t *testing.T) {
	cases := []struct {
		method string
		path   string
		want   bool
	}{
		{http.MethodGet, "/api/v1/config", false},
		{http.MethodPost, "/api/v1/config", true},
		{http.MethodPut, "/api/v1/modules/foo/config", true},
		{http.MethodDelete, "/api/v1/config/keys/foo", true},
		{http.MethodPost, "/api/v1/tasks", false},
	}
	for _, tc := range cases {
		req := httptest.NewRequestWithContext(context.Background(), tc.method, tc.path, nil)
		if got := isConfigMutation(req); got != tc.want {
			t.Errorf("isConfigMutation(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}

func TestConfigMutationRateLimitMiddleware(t *testing.T) {
	os.Setenv("MUXCORE_DEV_TLS_SKIP", "true")
	limiter := NewDefaultRateLimiter(1, 1)
	publicPaths := map[string]bool{}
	trusted := []net.IPNet{{IP: net.IPv4(127, 0, 0, 0), Mask: net.CIDRMask(8, 32)}}

	handler := configMutationRateLimitMiddleware(limiter, publicPaths, trusted)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)

	doPost := func() int {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/config", nil)
		req.RemoteAddr = "127.0.0.1:1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := doPost(); code != http.StatusOK {
		t.Fatalf("first config mutation: expected 200, got %d", code)
	}
	if code := doPost(); code != http.StatusTooManyRequests {
		t.Fatalf("second config mutation: expected 429, got %d", code)
	}
}

func TestHealthEndpoint_IncludesResources(t *testing.T) {
	os.Setenv("MUXCORE_DEV_TLS_SKIP", "true")
	srv := NewServer(":0", "", "")
	srv.SetHealthChecker(func() map[string]error {
		return map[string]error{"mod-a": nil}
	})
	srv.SetResourceChecker(func() map[string]health.ModuleResourceStats {
		return map[string]health.ModuleResourceStats{
			"mod-a": {MemoryBytes: 4096, Goroutines: 3},
		}
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"resources"`) {
		t.Fatalf("expected resources in body, got %s", rec.Body.String())
	}
}
