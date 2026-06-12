package grpcmesh

import (
	"context"
	"errors"
	"testing"

	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/pkg/contracts"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func newDSWithRegistry(t *testing.T, nodeID string) *DiscoveryServer {
	t.Helper()
	ds := newDS(nodeID)
	ds.reg = registry.New()
	return ds
}

func registerTestModule(t *testing.T, reg *registry.Registry, id, role string, caps []string) {
	t.Helper()
	mod := &mockModule{
		info: contracts.ModuleInfo{
			ID:           id,
			Name:         "Test " + id,
			Version:      "1.0.0",
			Roles:        []string{role},
			Capabilities: caps,
		},
	}
	if err := reg.Register(mod, nil); err != nil {
		t.Fatalf("register %s: %v", id, err)
	}
}

func TestFindByCapability(t *testing.T) {
	ds := newDSWithRegistry(t, "node-a")
	registerTestModule(t, ds.reg, "mod-a", "provider", []string{"storage.local", "cache.memory"})
	registerTestModule(t, ds.reg, "mod-b", "provider", []string{"storage.s3"})
	registerTestModule(t, ds.reg, "mod-c", "worker", []string{"transcode"})

	t.Run("found", func(t *testing.T) {
		resp, err := ds.FindByCapability(context.Background(), &discoveryv1.FindByCapabilityRequest{
			Capability: "storage.local",
		})
		if err != nil {
			t.Fatalf("FindByCapability: %v", err)
		}
		if len(resp.Modules) != 1 {
			t.Fatalf("expected 1 module, got %d", len(resp.Modules))
		}
		if resp.Modules[0].Id != "mod-a" {
			t.Errorf("expected mod-a, got %s", resp.Modules[0].Id)
		}
	})

	t.Run("multiple exact matches", func(t *testing.T) {
		// Register another module with an overlapping capability.
		registerTestModule(t, ds.reg, "mod-d", "provider", []string{"storage.s3", "compute"})
		resp, err := ds.FindByCapability(context.Background(), &discoveryv1.FindByCapabilityRequest{
			Capability: "storage.s3",
		})
		if err != nil {
			t.Fatalf("FindByCapability: %v", err)
		}
		if len(resp.Modules) != 2 {
			t.Fatalf("expected 2 modules with storage.s3 capability, got %d", len(resp.Modules))
		}
	})

	t.Run("not found", func(t *testing.T) {
		resp, err := ds.FindByCapability(context.Background(), &discoveryv1.FindByCapabilityRequest{
			Capability: "nonexistent",
		})
		if err != nil {
			t.Fatalf("FindByCapability: %v", err)
		}
		if len(resp.Modules) != 0 {
			t.Errorf("expected 0 modules, got %d", len(resp.Modules))
		}
	})

	t.Run("empty capability", func(t *testing.T) {
		_, err := ds.FindByCapability(context.Background(), &discoveryv1.FindByCapabilityRequest{
			Capability: "",
		})
		if err == nil {
			t.Fatal("expected error for empty capability")
		}
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("expected InvalidArgument, got %v", status.Code(err))
		}
	})

	t.Run("no registry", func(t *testing.T) {
		ds2 := newDS("node-b")
		_, err := ds2.FindByCapability(context.Background(), &discoveryv1.FindByCapabilityRequest{
			Capability: "storage.local",
		})
		if err == nil {
			t.Fatal("expected error when registry is nil")
		}
		if status.Code(err) != codes.Unavailable {
			t.Errorf("expected Unavailable, got %v", status.Code(err))
		}
	})
}

func TestFindByRole(t *testing.T) {
	ds := newDSWithRegistry(t, "node-a")
	registerTestModule(t, ds.reg, "mod-a", "provider", nil)
	registerTestModule(t, ds.reg, "mod-b", "worker", nil)
	registerTestModule(t, ds.reg, "mod-c", "provider", nil)

	t.Run("found", func(t *testing.T) {
		resp, err := ds.FindByRole(context.Background(), &discoveryv1.FindByRoleRequest{
			Role: "provider",
		})
		if err != nil {
			t.Fatalf("FindByRole: %v", err)
		}
		if len(resp.Modules) != 2 {
			t.Fatalf("expected 2 modules, got %d", len(resp.Modules))
		}
	})

	t.Run("single match", func(t *testing.T) {
		resp, err := ds.FindByRole(context.Background(), &discoveryv1.FindByRoleRequest{
			Role: "worker",
		})
		if err != nil {
			t.Fatalf("FindByRole: %v", err)
		}
		if len(resp.Modules) != 1 || resp.Modules[0].Id != "mod-b" {
			t.Errorf("expected mod-b, got %v", resp.Modules)
		}
	})

	t.Run("empty role", func(t *testing.T) {
		_, err := ds.FindByRole(context.Background(), &discoveryv1.FindByRoleRequest{
			Role: "",
		})
		if err == nil {
			t.Fatal("expected error for empty role")
		}
	})
}

func TestResolveModule(t *testing.T) {
	ds := newDSWithRegistry(t, "node-a")
	registerTestModule(t, ds.reg, "mod-a", "provider", nil)

	t.Run("found", func(t *testing.T) {
		resp, err := ds.Resolve(context.Background(), &discoveryv1.ResolveRequest{
			ModuleId: "mod-a",
		})
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if !resp.Found {
			t.Fatal("expected module to be found")
		}
		if resp.Module.Id != "mod-a" {
			t.Errorf("expected mod-a, got %s", resp.Module.Id)
		}
	})

	t.Run("not found", func(t *testing.T) {
		resp, err := ds.Resolve(context.Background(), &discoveryv1.ResolveRequest{
			ModuleId: "nonexistent",
		})
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if resp.Found {
			t.Fatal("expected module not to be found")
		}
	})

	t.Run("empty module id", func(t *testing.T) {
		_, err := ds.Resolve(context.Background(), &discoveryv1.ResolveRequest{
			ModuleId: "",
		})
		if err == nil {
			t.Fatal("expected error for empty module_id")
		}
	})
}

func TestModuleEntryToProto(t *testing.T) {
	entry := &registry.Entry{
		Info: contracts.ModuleInfo{
			ID:           "test-mod",
			Name:         "Test Module",
			Version:      "2.0.0",
			Roles:        []string{"provider", "worker"},
			Description:  "A test module",
			Author:       "test@example.com",
			Capabilities: []string{"storage.local", "cache.memory"},
			DependsOn:    []string{"base"},
		},
		State: contracts.ModuleStateRunning,
	}
	proto := moduleEntryToProto(entry)
	if proto.Id != "test-mod" {
		t.Errorf("expected test-mod, got %s", proto.Id)
	}
	if proto.Version != "2.0.0" {
		t.Errorf("expected 2.0.0, got %s", proto.Version)
	}
	if len(proto.Roles) != 2 {
		t.Errorf("expected 2 roles, got %d", len(proto.Roles))
	}
	if len(proto.Capabilities) != 2 {
		t.Errorf("expected 2 capabilities, got %d", len(proto.Capabilities))
	}
	if len(proto.DependsOn) != 1 {
		t.Errorf("expected 1 depends_on, got %d", len(proto.DependsOn))
	}
	if proto.State != "running" {
		t.Errorf("expected state running, got %s", proto.State)
	}
}

// mockModule implements contracts.Module for testing.
type mockModule struct {
	info contracts.ModuleInfo
}

func (m *mockModule) Info() contracts.ModuleInfo       { return m.info }
func (m *mockModule) Init(ctx context.Context) error   { return nil }
func (m *mockModule) Start(ctx context.Context) error  { return nil }
func (m *mockModule) Stop(ctx context.Context) error   { return nil }
func (m *mockModule) Health(ctx context.Context) error { return nil }

func TestListAll(t *testing.T) {
	ds := newDSWithRegistry(t, "node-a")
	registerTestModule(t, ds.reg, "mod-a", "provider", []string{"storage"})
	registerTestModule(t, ds.reg, "mod-b", "worker", []string{"compute"})

	t.Run("returns local entries", func(t *testing.T) {
		resp, err := ds.ListAll(context.Background(), &discoveryv1.ListAllRequest{})
		if err != nil {
			t.Fatalf("ListAll: %v", err)
		}
		if len(resp.Entries) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(resp.Entries))
		}
		ids := map[string]bool{}
		for _, e := range resp.Entries {
			ids[e.GetInfo().GetId()] = true
			if e.NodeId != "node-a" {
				t.Errorf("expected nodeId node-a, got %s", e.NodeId)
			}
		}
		if !ids["mod-a"] || !ids["mod-b"] {
			t.Errorf("expected mod-a and mod-b, got %v", ids)
		}
	})

	t.Run("no registry returns error", func(t *testing.T) {
		ds2 := newDS("node-b")
		_, err := ds2.ListAll(context.Background(), &discoveryv1.ListAllRequest{})
		if err == nil {
			t.Fatal("expected error when registry is nil")
		}
		if status.Code(err) != codes.Unavailable {
			t.Errorf("expected Unavailable, got %v", status.Code(err))
		}
	})
}

func TestSetRegistry_SetsRegistryAndUsable(t *testing.T) {
	ds := newDS("node-a")
	if ds.reg != nil {
		t.Fatal("expected nil registry initially")
	}

	reg := registry.New()
	ds.SetRegistry(reg)

	ds.mu.RLock()
	got := ds.reg
	ds.mu.RUnlock()
	if got != reg {
		t.Fatal("expected registry to be set")
	}

	registerTestModule(t, got, "mod-x", "provider", nil)
	resp, err := ds.ListAll(context.Background(), &discoveryv1.ListAllRequest{})
	if err != nil {
		t.Fatalf("ListAll after SetRegistry: %v", err)
	}
	if len(resp.Entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(resp.Entries))
	}
}

func TestFanOutQuery_NoMembers(t *testing.T) {
	ds := newDS("node-a")
	ds.reg = registry.New()

	results := ds.fanOutQuery(context.Background(), func(client discoveryv1.DiscoveryServiceClient) ([]*discoveryv1.ModuleInfoProto, error) {
		t.Fatal("queryFn should not be called with no members")
		return nil, nil
	})
	if results != nil {
		t.Errorf("expected nil results with no members, got %v", results)
	}
}

func TestFanOutListAll_NoMembers(t *testing.T) {
	ds := newDS("node-a")
	ds.reg = registry.New()

	results := ds.fanOutListAll(context.Background())
	if results != nil {
		t.Errorf("expected nil results with no members, got %v", results)
	}
}

func TestFanOutQuery_OnlySelfMember(t *testing.T) {
	ds := newDS("node-a", "node-a")
	ds.reg = registry.New()

	results := ds.fanOutQuery(context.Background(), func(client discoveryv1.DiscoveryServiceClient) ([]*discoveryv1.ModuleInfoProto, error) {
		t.Fatal("queryFn should not be called when only self is a member")
		return nil, nil
	})
	if results != nil {
		t.Errorf("expected nil results when only self is member, got %v", results)
	}
}

func TestFanOutListAll_OnlySelfMember(t *testing.T) {
	ds := newDS("node-a", "node-a")
	ds.reg = registry.New()

	results := ds.fanOutListAll(context.Background())
	if results != nil {
		t.Errorf("expected nil results when only self is member, got %v", results)
	}
}

func TestHealthErrorString(t *testing.T) {
	if got := healthErrorString(nil); got != "" {
		t.Errorf("expected empty string for nil error, got %q", got)
	}
	if got := healthErrorString(errors.New("boom")); got != "boom" {
		t.Errorf("expected 'boom', got %q", got)
	}
}
