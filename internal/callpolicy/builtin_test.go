package callpolicy

import (
	"context"
	"testing"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// stubRegistry implements contracts.Registry for testing.
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

func makeEntry(id string, caps []string, roles []string) contracts.ModuleEntry {
	return contracts.ModuleEntry{
		Info: contracts.ModuleInfo{ID: id, Name: id, Capabilities: caps, Roles: roles},
	}
}

func newReg(entries ...contracts.ModuleEntry) *stubRegistry {
	r := &stubRegistry{modules: make(map[string]contracts.ModuleEntry)}
	for _, e := range entries {
		r.modules[e.Info.ID] = e
	}
	return r
}

func TestAllowCall_EmptyCaller_AlwaysAllowed(t *testing.T) {
	p := NewBuiltinPolicy(newReg())
	ok, err := p.AllowCall(context.Background(), "", "any-target", "method")
	if err != nil || !ok {
		t.Errorf("empty callerID should always be allowed, got ok=%v err=%v", ok, err)
	}
}

func TestAllowCall_UnknownCaller_SoftAllow(t *testing.T) {
	p := NewBuiltinPolicy(newReg()) // no modules registered
	ok, err := p.AllowCall(context.Background(), "unknown", "also-unknown", "m")
	if err != nil || !ok {
		t.Errorf("non-strict: unknown caller should be allowed, got ok=%v err=%v", ok, err)
	}
}

func TestAllowCall_UnknownCaller_StrictDeny(t *testing.T) {
	t.Setenv("MUXCORE_STRICT_CALL_POLICY", "true")
	p := NewBuiltinPolicy(newReg())
	ok, err := p.AllowCall(context.Background(), "unknown", "also-unknown", "m")
	if err != nil || ok {
		t.Errorf("strict: unknown caller should be denied, got ok=%v err=%v", ok, err)
	}
}

func TestAllowCall_MatchingCapability_Allowed(t *testing.T) {
	reg := newReg(
		makeEntry("caller", []string{"storage"}, nil),
		makeEntry("target", nil, []string{"storage"}),
	)
	p := NewBuiltinPolicy(reg)
	ok, err := p.AllowCall(context.Background(), "caller", "target", "m")
	if err != nil || !ok {
		t.Errorf("matching capability should be allowed, got ok=%v err=%v", ok, err)
	}
}

func TestAllowCall_MismatchedCapability_SoftAllow(t *testing.T) {
	reg := newReg(
		makeEntry("caller", []string{"downloader"}, nil),
		makeEntry("target", nil, []string{"auth"}),
	)
	p := NewBuiltinPolicy(reg)
	ok, err := p.AllowCall(context.Background(), "caller", "target", "m")
	if err != nil || !ok {
		t.Errorf("non-strict: mismatch should be allowed, got ok=%v err=%v", ok, err)
	}
}

func TestAllowCall_MismatchedCapability_StrictDeny(t *testing.T) {
	t.Setenv("MUXCORE_STRICT_CALL_POLICY", "true")
	reg := newReg(
		makeEntry("caller", []string{"downloader"}, nil),
		makeEntry("target", nil, []string{"auth"}),
	)
	p := NewBuiltinPolicy(reg)
	ok, err := p.AllowCall(context.Background(), "caller", "target", "m")
	if err != nil || ok {
		t.Errorf("strict: mismatch should be denied, got ok=%v err=%v", ok, err)
	}
}

func TestAllowCall_WildcardCapability_AlwaysAllowed(t *testing.T) {
	t.Setenv("MUXCORE_STRICT_CALL_POLICY", "true")
	reg := newReg(
		makeEntry("caller", []string{"*"}, nil),
		makeEntry("target", nil, []string{"anything"}),
	)
	p := NewBuiltinPolicy(reg)
	ok, err := p.AllowCall(context.Background(), "caller", "target", "m")
	if err != nil || !ok {
		t.Errorf("* capability should always be allowed, got ok=%v err=%v", ok, err)
	}
}

func TestAllowCall_MeshAdminCapability_AlwaysAllowed(t *testing.T) {
	t.Setenv("MUXCORE_STRICT_CALL_POLICY", "true")
	reg := newReg(
		makeEntry("caller", []string{"mesh.admin"}, nil),
		makeEntry("target", nil, []string{"restricted"}),
	)
	p := NewBuiltinPolicy(reg)
	ok, _ := p.AllowCall(context.Background(), "caller", "target", "m")
	if !ok {
		t.Error("mesh.admin should bypass all capability checks")
	}
}

func TestAllowCall_UnknownTarget_Allowed(t *testing.T) {
	reg := newReg(makeEntry("caller", []string{"storage"}, nil))
	p := NewBuiltinPolicy(reg)
	ok, err := p.AllowCall(context.Background(), "caller", "unknown-target", "m")
	if err != nil || !ok {
		t.Errorf("unknown target should be allowed (target may not be registered yet), got ok=%v err=%v", ok, err)
	}
}

func TestHasMatchingCapability_PrefixMatching(t *testing.T) {
	cases := []struct {
		caps   []string
		roles  []string
		expect bool
	}{
		{[]string{"storage"}, []string{"storage-s3"}, true},    // cap prefix of role
		{[]string{"storage-s3"}, []string{"storage"}, true},    // role prefix of cap
		{[]string{"download"}, []string{"downloader"}, true},   // role prefix of cap
		{[]string{"metrics"}, []string{"tracing"}, false},      // no match
		{[]string{}, []string{"storage"}, false},               // empty caps
		{[]string{"storage"}, []string{}, false},               // empty roles
	}
	for _, tc := range cases {
		got := hasMatchingCapability(tc.caps, tc.roles)
		if got != tc.expect {
			t.Errorf("hasMatchingCapability(%v, %v) = %v, want %v", tc.caps, tc.roles, got, tc.expect)
		}
	}
}
