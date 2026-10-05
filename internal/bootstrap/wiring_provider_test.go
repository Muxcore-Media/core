package bootstrap

import (
	"context"
	"testing"

	"github.com/Muxcore-Media/core/internal/events"
	"github.com/Muxcore-Media/core/internal/grpcmesh"
	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/pkg/contracts"
)

// policyModule is an in-process call and publish policy provider.
type policyModule struct {
	id string
}

func (p *policyModule) Info() contracts.ModuleInfo {
	return contracts.ModuleInfo{ID: p.id, Name: p.id, Capabilities: []string{
		contracts.CapabilityCallPolicy, contracts.CapabilityPublishPolicy,
	}}
}
func (p *policyModule) Init(context.Context) error   { return nil }
func (p *policyModule) Start(context.Context) error  { return nil }
func (p *policyModule) Stop(context.Context) error   { return nil }
func (p *policyModule) Health(context.Context) error { return nil }
func (p *policyModule) AllowCall(context.Context, string, string, string) (bool, error) {
	return true, nil
}
func (p *policyModule) CanPublish(context.Context, string, string) (bool, error) {
	return true, nil
}

// ADR-0018 / NFR-SEC-002: wiring picks the first-registered provider, not a
// random map entry. Repeated to catch map-order nondeterminism.
func TestWirePolicies_FirstRegisteredProviderWins(t *testing.T) {
	for run := 0; run < 25; run++ {
		reg := registry.New()
		first := &policyModule{id: "zz-first"}
		for _, m := range []*policyModule{first, {id: "aa-second"}, {id: "mm-third"}} {
			if err := reg.Register(m, nil); err != nil {
				t.Fatal(err)
			}
		}
		meshClient := grpcmesh.NewClient(grpcmesh.NewServer())
		storageGrpc := grpcmesh.NewStorageServer(nil)
		if err := WireCallPolicy(reg, meshClient, storageGrpc, nil, 0); err != nil {
			t.Fatal(err)
		}
		if got, ok := meshClient.CallPolicy().(*policyModule); !ok || got != first {
			t.Fatalf("run %d: call policy wired to %v, want %s", run, meshClient.CallPolicy(), first.id)
		}
		bus := events.NewMemoryBus()
		if err := WirePublishPolicy(reg, bus, nil, 0); err != nil {
			t.Fatal(err)
		}
		if got, ok := bus.PublishPolicy().(*policyModule); !ok || got != first {
			t.Fatalf("run %d: publish policy wired to %v, want %s", run, bus.PublishPolicy(), first.id)
		}
	}
}
