package spool

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

func TestFetchTag_HTTPS(t *testing.T) {
	tag := contracts.TagDefinition{
		Name:    "default",
		Version: "1.0.0",
		Modules: []contracts.TagModule{
			{Repo: "https://github.com/Muxcore-Media/admin-ui", Version: "v1.0.0", Required: true},
		},
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tags/default.json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(tag)
	}))
	defer srv.Close()

	// Use a client that trusts the test server's self-signed cert.
	saved := client
	t.Cleanup(func() { client = saved })
	client = srv.Client()

	// Explicitly allow the test server's host so private-IP blocking doesn't
	// reject the loopback address.
	u, _ := url.Parse(srv.URL)
	t.Cleanup(func() { SetAllowedHosts(nil) })
	SetAllowedHosts([]string{u.Host})

	got, err := FetchTag(context.Background(), srv.URL, "default")
	if err != nil {
		t.Fatalf("FetchTag failed: %v", err)
	}
	if got.Name != "default" {
		t.Errorf("expected name 'default', got %q", got.Name)
	}
	if len(got.Modules) != 1 {
		t.Errorf("expected 1 module, got %d", len(got.Modules))
	}
}

func TestFetchTag_HTTPRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, err := FetchTag(context.Background(), srv.URL, "test")
	if err == nil {
		t.Fatal("expected error for HTTP URL (non-TLS)")
	}
}

func TestFetchTag_NotFound(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	u, _ := url.Parse(srv.URL)
	t.Cleanup(func() { SetAllowedHosts(nil) })
	SetAllowedHosts([]string{u.Host})

	_, err := FetchTag(context.Background(), srv.URL, "nonexistent")
	if err == nil {
		t.Fatal("expected error for 404")
	}
}

func TestFetchTag_AllowedHosts(t *testing.T) {
	tag := contracts.TagDefinition{
		Name:    "test",
		Version: "1.0.0",
		Modules: []contracts.TagModule{},
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(tag)
	}))
	defer srv.Close()

	saved := client
	t.Cleanup(func() { client = saved })
	client = srv.Client()

	// Restore allow-list state after test.
	t.Cleanup(func() { SetAllowedHosts(nil) })

	// Set a non-matching host — should reject.
	SetAllowedHosts([]string{"github.com"})
	_, err := FetchTag(context.Background(), srv.URL, "test")
	if err == nil {
		t.Fatal("expected error when spool host is not in allow-list")
	}

	// Set a matching host — should succeed.
	u, _ := url.Parse(srv.URL)
	SetAllowedHosts([]string{u.Host})
	got, err := FetchTag(context.Background(), srv.URL, "test")
	if err != nil {
		t.Fatalf("FetchTag failed with matching host: %v", err)
	}
	if got.Name != "test" {
		t.Errorf("expected name 'test', got %q", got.Name)
	}
}

func TestFetchTag_RejectsMetadataEvenWhenAllowListed(t *testing.T) {
	t.Cleanup(func() { SetAllowedHosts(nil) })
	SetAllowedHosts([]string{"169.254.169.254"})
	_, err := FetchTag(context.Background(), "https://169.254.169.254/latest", "meta")
	if err == nil {
		t.Fatal("expected metadata host to stay blocked")
	}
}

func TestFetchTag_EmptyInput(t *testing.T) {
	_, err := FetchTag(context.Background(), "", "tag")
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
	_, err = FetchTag(context.Background(), "https://example.com", "")
	if err == nil {
		t.Fatal("expected error for empty tag name")
	}
}

func TestFetchTag_InvalidJSON(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	u, _ := url.Parse(srv.URL)
	t.Cleanup(func() { SetAllowedHosts(nil) })
	SetAllowedHosts([]string{u.Host})

	_, err := FetchTag(context.Background(), srv.URL, "bad")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestFetchTag_InvalidTagName(t *testing.T) {
	bad := []string{"../etc/passwd", "foo/bar", "tag name", "foo;bar", "tag$inject", "a]b"}
	for _, name := range bad {
		_, err := FetchTag(context.Background(), "https://example.com/spool", name)
		if err == nil {
			t.Errorf("expected error for tag name %q", name)
		}
	}
}

func TestFetchTag_RejectsNonHTTPS(t *testing.T) {
	schemes := []string{
		"ftp://example.com/spool",
		"file:///etc/passwd",
		"gopher://example.com/spool",
	}
	for _, u := range schemes {
		_, err := FetchTag(context.Background(), u, "test")
		if err == nil {
			t.Errorf("expected error for scheme in %q", u)
		}
	}
}

func TestSetAllowedHosts_NilClearsList(t *testing.T) {
	t.Cleanup(func() { SetAllowedHosts(nil) })

	SetAllowedHosts([]string{"example.com"})
	allowedHostsMu.RLock()
	if len(allowedHosts) == 0 {
		t.Fatal("expected non-empty allow-list after Set")
	}
	allowedHostsMu.RUnlock()

	SetAllowedHosts(nil)
	allowedHostsMu.RLock()
	if allowedHosts != nil {
		t.Errorf("expected nil allow-list after SetAllowedHosts(nil), got %v", allowedHosts)
	}
	allowedHostsMu.RUnlock()

	SetAllowedHosts([]string{"example.com"})
	SetAllowedHosts([]string{})
	allowedHostsMu.RLock()
	if allowedHosts != nil {
		t.Errorf("expected nil allow-list after SetAllowedHosts(empty), got %v", allowedHosts)
	}
	allowedHostsMu.RUnlock()
}

func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.0.1", true},
		{"192.168.255.255", true},
		{"127.0.0.1", true},
		{"::1", true},
		{"169.254.1.1", true},
		{"fe80::1", true},
		{"0.0.0.0", true},
		{"::", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"203.0.113.1", false},
		{"2606:4700:4700::1111", false},
		{"172.15.255.255", false},
		{"172.32.0.0", false},
	}
	for _, tt := range tests {
		ip := net.ParseIP(tt.ip)
		if ip == nil {
			t.Fatalf("failed to parse IP %q", tt.ip)
		}
		got := isPrivateIP(ip)
		if got != tt.want {
			t.Errorf("isPrivateIP(%s) = %v, want %v", tt.ip, got, tt.want)
		}
	}
}

func TestStripPort(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"example.com:443", "example.com"},
		{"127.0.0.1:8080", "127.0.0.1"},
		{"[::1]:443", "::1"},
		{"example.com", "example.com"},
		{"127.0.0.1", "127.0.0.1"},
	}
	for _, tt := range tests {
		got := stripPort(tt.in)
		if got != tt.want {
			t.Errorf("stripPort(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestBlockPrivateHost(t *testing.T) {
	ctx := context.Background()
	private := []string{"127.0.0.1", "10.0.0.1", "192.168.1.1", "172.16.0.1"}
	for _, ip := range private {
		err := blockPrivateHost(ctx, ip)
		if err == nil {
			t.Errorf("blockPrivateHost(%q) should have returned error", ip)
		}
	}

	err := blockPrivateHost(ctx, "127.0.0.1:443")
	if err == nil {
		t.Error("blockPrivateHost(127.0.0.1:443) should have returned error")
	}
}

func TestFetchTag_WithAllowedHostsAllowsMatchingHost(t *testing.T) {
	tag := contracts.TagDefinition{
		Name:    "allowed",
		Version: "2.0.0",
		Modules: []contracts.TagModule{},
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(tag)
	}))
	defer srv.Close()

	saved := client
	t.Cleanup(func() { client = saved })
	client = srv.Client()

	u, _ := url.Parse(srv.URL)
	t.Cleanup(func() { SetAllowedHosts(nil) })
	SetAllowedHosts([]string{u.Host})

	got, err := FetchTag(context.Background(), srv.URL, "allowed")
	if err != nil {
		t.Fatalf("FetchTag with allowed host failed: %v", err)
	}
	if got.Name != "allowed" {
		t.Errorf("expected name 'allowed', got %q", got.Name)
	}
	if got.Version != "2.0.0" {
		t.Errorf("expected version '2.0.0', got %q", got.Version)
	}
}

func TestFetchTag_ValidTagResponse(t *testing.T) {
	tag := contracts.TagDefinition{
		Name:        "production",
		Description: "Production modules",
		Version:     "3.1.0",
		Modules: []contracts.TagModule{
			{Repo: "https://github.com/Muxcore-Media/admin-ui", Version: "v2.0.0", Required: true},
			{Repo: "https://github.com/Muxcore-Media/analytics", Version: "v1.5.0", Required: false},
		},
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tags/production.json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(tag)
	}))
	defer srv.Close()

	saved := client
	t.Cleanup(func() { client = saved })
	client = srv.Client()

	u, _ := url.Parse(srv.URL)
	t.Cleanup(func() { SetAllowedHosts(nil) })
	SetAllowedHosts([]string{u.Host})

	got, err := FetchTag(context.Background(), srv.URL, "production")
	if err != nil {
		t.Fatalf("FetchTag failed: %v", err)
	}
	if got.Name != "production" {
		t.Errorf("Name = %q, want 'production'", got.Name)
	}
	if got.Description != "Production modules" {
		t.Errorf("Description = %q, want 'Production modules'", got.Description)
	}
	if got.Version != "3.1.0" {
		t.Errorf("Version = %q, want '3.1.0'", got.Version)
	}
	if len(got.Modules) != 2 {
		t.Fatalf("len(Modules) = %d, want 2", len(got.Modules))
	}
	if got.Modules[0].Repo != "https://github.com/Muxcore-Media/admin-ui" {
		t.Errorf("Modules[0].Repo = %q", got.Modules[0].Repo)
	}
	if !got.Modules[0].Required {
		t.Error("Modules[0].Required = false, want true")
	}
	if got.Modules[1].Required {
		t.Error("Modules[1].Required = true, want false")
	}
}
