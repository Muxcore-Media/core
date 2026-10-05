package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestIsLoopbackAddr(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:9464": true,
		"[::1]:9464":     true,
		"localhost:9464": true,
		"127.0.0.1:0":    true,
		":9464":          false,
		"0.0.0.0:9464":   false,
		"[::]:9464":      false,
		"10.0.0.5:9464":  false,
		"example.com:80": false,
	}
	for addr, want := range cases {
		got, err := IsLoopbackAddr(addr)
		if err != nil || got != want {
			t.Errorf("IsLoopbackAddr(%q) = %v, %v; want %v", addr, got, err, want)
		}
	}
	if _, err := IsLoopbackAddr("nonsense"); err == nil {
		t.Error("expected error for address without port")
	}
}

func TestNewMetricsServer_NonLoopbackWithoutTokenRefused(t *testing.T) {
	for _, addr := range []string{":0", "0.0.0.0:0", "[::]:0"} {
		ms, err := NewMetricsServer(addr, "", &MetricsProvider{})
		if err == nil {
			_ = ms.Shutdown(context.Background())
			t.Fatalf("expected refusal for %q without token", addr)
		}
		if !strings.Contains(err.Error(), "MUXCORE_METRICS_TOKEN") {
			t.Errorf("error should name the token env var: %v", err)
		}
	}
}

func TestDefaultMetricsAddrIsLoopback(t *testing.T) {
	if loop, err := IsLoopbackAddr(DefaultMetricsAddr); err != nil || !loop {
		t.Fatalf("DefaultMetricsAddr %q must be loopback", DefaultMetricsAddr)
	}
	if err := ValidateMetricsBind(DefaultMetricsAddr, ""); err != nil {
		t.Fatal(err)
	}
}

func startMetrics(t *testing.T, addr, token string) *MetricsServer {
	t.Helper()
	ms, err := NewMetricsServer(addr, token, &MetricsProvider{})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = ms.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = ms.Shutdown(ctx)
	})
	return ms
}

func get(t *testing.T, url, auth string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestMetricsServer_LoopbackNoTokenServes(t *testing.T) {
	ms := startMetrics(t, "127.0.0.1:0", "")
	resp := get(t, "http://"+ms.Addr()+"/metrics", "")
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") {
		t.Fatalf("status %d content-type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}

func TestMetricsServer_TokenEnforced(t *testing.T) {
	ms := startMetrics(t, "127.0.0.1:0", "s3cret-token")
	url := "http://" + ms.Addr() + "/metrics"
	if r := get(t, url, ""); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("no auth: %d", r.StatusCode)
	}
	if r := get(t, url, "Bearer wrong"); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong token: %d", r.StatusCode)
	}
	if r := get(t, url, "Basic s3cret-token"); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong scheme: %d", r.StatusCode)
	}
	if r := get(t, url, "Bearer s3cret-token"); r.StatusCode != http.StatusOK {
		t.Errorf("good token: %d", r.StatusCode)
	}
}

func TestValidateMetricsBind_NonLoopbackWithTokenAllowed(t *testing.T) {
	if err := ValidateMetricsBind("0.0.0.0:9464", "tok"); err != nil {
		t.Fatal(err)
	}
}

func TestServer_ShutdownRunsHooks(t *testing.T) {
	s := NewServer("127.0.0.1:0", "", "")
	called := false
	s.AddShutdownHook(func(context.Context) error { called = true; return nil })
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("hook not called")
	}
}
