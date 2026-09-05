package tenant

import (
	"context"
	"testing"
)

type stubFinder struct {
	byRegion map[string][]NodeRef
}

func (s stubFinder) FindNodesByLabel(_ context.Context, label, value string) []NodeRef {
	if label != "region" {
		return nil
	}
	return s.byRegion[value]
}

func TestParseRegionMap(t *testing.T) {
	m, err := ParseRegionMap(`{"tenant-a":"us-east","tenant-b":"eu-west"}`)
	if err != nil {
		t.Fatal(err)
	}
	if m["tenant-a"] != "us-east" || m["tenant-b"] != "eu-west" {
		t.Fatalf("%v", m)
	}
	if _, err := ParseRegionMap("{"); err == nil {
		t.Fatal("expected error")
	}
}

func TestPlacerResolveGRPC(t *testing.T) {
	t.Setenv("TENANT_MODE", "1")
	p := &Placer{
		RegionMap:   map[string]string{"acme": "us-east"},
		LocalRegion: "eu-west",
	}
	f := stubFinder{byRegion: map[string][]NodeRef{
		"us-east": {{ID: "n1", GRPCAddr: "10.0.0.1:9090", Region: "us-east"}},
	}}
	addr, ok := p.ResolveGRPC(context.Background(), f, "acme")
	if !ok || addr != "10.0.0.1:9090" {
		t.Fatalf("got %q ok=%v", addr, ok)
	}
	fLocal := stubFinder{byRegion: map[string][]NodeRef{
		"us-east": {{ID: "n1", GRPCAddr: "10.0.0.1:9090", Region: "us-east"}},
		"eu-west": {{ID: "n2", GRPCAddr: "10.0.0.2:9090", Region: "eu-west"}},
	}}
	addr, ok = p.ResolveGRPC(context.Background(), fLocal, "unknown")
	if !ok || addr != "10.0.0.2:9090" {
		t.Fatalf("unmapped tenant should prefer local region, got %q ok=%v", addr, ok)
	}
}

func TestResolveDialWithPlacement(t *testing.T) {
	t.Setenv("TENANT_MODE", "1")
	t.Setenv("MUXCORE_TENANT_REGION_MAP", `{"acme":"us-east"}`)
	t.Setenv("MUXCORE_TENANT_CLUSTER_MAP", `{"acme":"cluster-map:9090"}`)
	f := stubFinder{byRegion: map[string][]NodeRef{
		"us-east": {{GRPCAddr: "placed:9090"}},
	}}
	r := &Router{Map: map[string][]string{"acme": {"cluster-map:9090"}}}
	got, err := ResolveDial(context.Background(), f, r, "acme")
	if err != nil || got != "placed:9090" {
		t.Fatalf("placement should win: got %q err=%v", got, err)
	}
	got, err = ResolveDial(context.Background(), stubFinder{}, r, "acme")
	if err != nil || got != "cluster-map:9090" {
		t.Fatalf("router fallback: got %q err=%v", got, err)
	}
}

func TestClusterAdapterNil(t *testing.T) {
	if ClusterAdapter(nil) != nil {
		t.Fatal("expected nil")
	}
}
