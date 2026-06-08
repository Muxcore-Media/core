package eventpolicy

import (
	"context"
	"testing"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// stubRegistry for eventpolicy tests.
type stubRegistry struct {
	modules map[string]contracts.ModuleEntry
}

func (r *stubRegistry) Resolve(id string) (contracts.ModuleEntry, error) {
	e, ok := r.modules[id]
	if !ok {
		return contracts.ModuleEntry{}, &notFoundError{id}
	}
	return e, nil
}
func (r *stubRegistry) FindByRole(role string) []contracts.ModuleEntry       { return nil }
func (r *stubRegistry) FindByCapability(cap string) []contracts.ModuleEntry  { return nil }
func (r *stubRegistry) SupportsCapability(id, cap string) bool               { return false }
func (r *stubRegistry) ListAll() []contracts.ModuleEntry                     { return nil }
func (r *stubRegistry) StartupOrder() ([]string, error)                      { return nil, nil }
func (r *stubRegistry) DependencyGraph(id string) ([]string, error)          { return nil, nil }

type notFoundError struct{ id string }

func (e *notFoundError) Error() string { return "not found: " + e.id }

func newReg(id string, caps []string) *stubRegistry {
	return &stubRegistry{
		modules: map[string]contracts.ModuleEntry{
			id: {Info: contracts.ModuleInfo{ID: id, Capabilities: caps}},
		},
	}
}

func emptyReg() *stubRegistry {
	return &stubRegistry{modules: map[string]contracts.ModuleEntry{}}
}

func TestCanPublish_EmptyCaller_AlwaysAllowed(t *testing.T) {
	p := NewBuiltinPolicy(emptyReg())
	ok, err := p.CanPublish(context.Background(), "", "any.event")
	if err != nil || !ok {
		t.Errorf("empty callerID should be allowed, got ok=%v err=%v", ok, err)
	}
}

func TestCanPublish_CoreLifecycleEvents_AlwaysAllowed(t *testing.T) {
	p := NewBuiltinPolicy(emptyReg())
	coreEvents := []string{
		contracts.EventModuleRegistered,
		contracts.EventModuleUnregistered,
		contracts.EventModuleDegraded,
	}
	for _, eventType := range coreEvents {
		ok, err := p.CanPublish(context.Background(), "any-module", eventType)
		if err != nil || !ok {
			t.Errorf("core event %q should always be allowed, got ok=%v err=%v", eventType, ok, err)
		}
	}
}

func TestCanPublish_ClusterEvents_AlwaysAllowed(t *testing.T) {
	p := NewBuiltinPolicy(emptyReg())
	clusterEvents := []string{"cluster.joined", "cluster.leader_changed", "cluster.node_left"}
	for _, eventType := range clusterEvents {
		ok, err := p.CanPublish(context.Background(), "any-module", eventType)
		if err != nil || !ok {
			t.Errorf("cluster event %q should always be allowed, got ok=%v err=%v", eventType, ok, err)
		}
	}
}

func TestCanPublish_UnknownCaller_SoftAllow(t *testing.T) {
	p := NewBuiltinPolicy(emptyReg())
	ok, err := p.CanPublish(context.Background(), "unknown-module", "media.added")
	if err != nil || !ok {
		t.Errorf("non-strict: unknown caller should be allowed, got ok=%v err=%v", ok, err)
	}
}

func TestCanPublish_UnknownCaller_StrictDeny(t *testing.T) {
	t.Setenv("MUXCORE_STRICT_PUBLISH_POLICY", "true")
	p := NewBuiltinPolicy(emptyReg())
	ok, err := p.CanPublish(context.Background(), "unknown-module", "media.added")
	if err != nil || ok {
		t.Errorf("strict: unknown caller should be denied, got ok=%v err=%v", ok, err)
	}
}

func TestCanPublish_MatchingCapability_Allowed(t *testing.T) {
	reg := newReg("downloader-q", []string{"downloader"})
	p := NewBuiltinPolicy(reg)
	ok, err := p.CanPublish(context.Background(), "downloader-q", "download.completed")
	if err != nil || !ok {
		t.Errorf("matching capability should allow publish, got ok=%v err=%v", ok, err)
	}
}

func TestCanPublish_MismatchedCapability_SoftAllow(t *testing.T) {
	reg := newReg("metrics-mod", []string{"metrics"})
	p := NewBuiltinPolicy(reg)
	ok, err := p.CanPublish(context.Background(), "metrics-mod", "media.added")
	if err != nil || !ok {
		t.Errorf("non-strict: mismatch should be allowed (soft), got ok=%v err=%v", ok, err)
	}
}

func TestCanPublish_MismatchedCapability_StrictDeny(t *testing.T) {
	t.Setenv("MUXCORE_STRICT_PUBLISH_POLICY", "true")
	reg := newReg("metrics-mod", []string{"metrics"})
	p := NewBuiltinPolicy(reg)
	ok, err := p.CanPublish(context.Background(), "metrics-mod", "media.added")
	if err != nil || ok {
		t.Errorf("strict: mismatch should be denied, got ok=%v err=%v", ok, err)
	}
}

func TestCanPublish_WildcardCapability_AlwaysAllowed(t *testing.T) {
	t.Setenv("MUXCORE_STRICT_PUBLISH_POLICY", "true")
	reg := newReg("admin-mod", []string{"*"})
	p := NewBuiltinPolicy(reg)
	ok, err := p.CanPublish(context.Background(), "admin-mod", "absolutely.anything")
	if err != nil || !ok {
		t.Errorf("* capability should allow any publish, got ok=%v err=%v", ok, err)
	}
}

func TestCapabilityMatchesEvent_PrefixLogic(t *testing.T) {
	cases := []struct {
		caps      []string
		eventType string
		expect    bool
	}{
		{[]string{"downloader"}, "download.completed", true},  // cap prefix of event prefix
		{[]string{"download"}, "download.started", true},      // cap == event prefix
		{[]string{"media"}, "media.movie.added", true},        // cap == first segment
		{[]string{"metrics"}, "media.added", false},           // no match
		{[]string{"*"}, "anything.at.all", true},              // wildcard
		{[]string{"mesh.admin"}, "restricted.event", true},    // mesh.admin bypass
		{[]string{}, "some.event", false},                     // empty caps
	}
	for _, tc := range cases {
		got := capabilityMatchesEvent(tc.caps, tc.eventType)
		if got != tc.expect {
			t.Errorf("capabilityMatchesEvent(%v, %q) = %v, want %v", tc.caps, tc.eventType, got, tc.expect)
		}
	}
}

func TestIsCoreEvent(t *testing.T) {
	if !isCoreEvent(contracts.EventModuleRegistered) {
		t.Error("module.registered should be a core event")
	}
	if !isCoreEvent("cluster.joined") {
		t.Error("cluster.* should be core events")
	}
	if isCoreEvent("media.added") {
		t.Error("media.added should not be a core event")
	}
}
