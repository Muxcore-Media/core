package tenant

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestParseClusterMap(t *testing.T) {
	m, err := ParseClusterMap(`{"tenant-a":"host:9090","tenant-b":"h2:9090, h3:9090"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(m["tenant-a"]) != 1 || m["tenant-a"][0] != "host:9090" {
		t.Fatalf("%v", m["tenant-a"])
	}
	if len(m["tenant-b"]) != 2 || m["tenant-b"][1] != "h3:9090" {
		t.Fatalf("%v", m["tenant-b"])
	}
	empty, err := ParseClusterMap("")
	if err != nil || len(empty) != 0 {
		t.Fatalf("%v %v", empty, err)
	}
	if _, err := ParseClusterMap("{"); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestResolveDialLocalAndRemote(t *testing.T) {
	t.Setenv("TENANT_MODE", "1")
	r := &Router{Map: map[string][]string{
		"tenant-a": {"remote.example:9090"},
	}}
	got, err := r.ResolveDial(WithID(context.Background(), "local-only"))
	if err != nil || got != "" {
		t.Fatalf("unmapped: got %q err=%v", got, err)
	}
	got, err = r.ResolveDial(WithID(context.Background(), "tenant-a"))
	if err != nil || got != "remote.example:9090" {
		t.Fatalf("mapped: got %q err=%v", got, err)
	}
}

func TestResolveDialOffOrEmptyMap(t *testing.T) {
	t.Setenv("TENANT_MODE", "")
	r := &Router{Map: map[string][]string{"a": {"h:1"}}}
	got, err := r.ResolveDial(WithID(context.Background(), "a"))
	if err != nil || got != "" {
		t.Fatalf("mode off: %q %v", got, err)
	}
	t.Setenv("TENANT_MODE", "1")
	got, err = (&Router{}).ResolveDial(WithID(context.Background(), "a"))
	if err != nil || got != "" {
		t.Fatalf("empty map: %q %v", got, err)
	}
}

func TestResolveDialStrict(t *testing.T) {
	t.Setenv("TENANT_MODE", "1")
	r := &Router{
		Map:    map[string][]string{"t1": {"bad:1", "good:2"}},
		Strict: true,
		Probe: func(network, address string, timeout time.Duration) (net.Conn, error) {
			if address == "good:2" {
				c1, c2 := net.Pipe()
				_ = c2.Close()
				return c1, nil
			}
			return nil, net.ErrClosed
		},
	}
	got, err := r.ResolveDial(WithID(context.Background(), "t1"))
	if err != nil || got != "good:2" {
		t.Fatalf("got %q err=%v", got, err)
	}

	r.Map = map[string][]string{"t1": {"bad:1"}}
	_, err = r.ResolveDial(WithID(context.Background(), "t1"))
	if err == nil {
		t.Fatal("expected strict unreachable error")
	}
}

func TestRouterFromEnvAndTenantIDFallback(t *testing.T) {
	ResetCachedRouter()
	t.Setenv("TENANT_MODE", "1")
	t.Setenv("MUXCORE_TENANT_CLUSTER_MAP", `{"acme":"core-acme:9090"}`)
	t.Setenv("MUXCORE_TENANT_CLUSTER_STRICT", "")
	t.Setenv("MUXCORE_TENANT_ID", "acme")
	r, err := RouterFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.ResolveDial(context.Background())
	if err != nil || got != "core-acme:9090" {
		t.Fatalf("got %q err=%v", got, err)
	}
	ResetCachedRouter()
}
