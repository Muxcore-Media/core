package registry

import (
	"errors"
	"sync"
	"testing"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

func capMod(id string, caps ...string) *mockModule {
	return &mockModule{info: contracts.ModuleInfo{ID: id, Name: id, Capabilities: caps}}
}

func ids(entries []*Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Info.ID
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// NFR-SEC-002: listing order is registration order, never map order.
func TestListByCapability_RegistrationOrder(t *testing.T) {
	for run := 0; run < 20; run++ {
		r := New()
		for _, id := range []string{"zeta", "alpha", "mid", "beta"} {
			if err := r.Register(capMod(id, "call.policy"), nil); err != nil {
				t.Fatal(err)
			}
		}
		want := []string{"zeta", "alpha", "mid", "beta"}
		if got := ids(r.ListByCapability("call.policy")); !equal(got, want) {
			t.Fatalf("run %d: ListByCapability = %v, want %v", run, got, want)
		}
		if got := ids(r.List()); !equal(got, want) {
			t.Fatalf("run %d: List = %v, want %v", run, got, want)
		}
		if p, ok := r.Provider("call.policy"); !ok || p.Info.ID != "zeta" {
			t.Fatalf("Provider = %v, %v; want zeta", p, ok)
		}
	}
}

func TestSortEntries_TieBreaksByID(t *testing.T) {
	entries := []*Entry{
		{Info: contracts.ModuleInfo{ID: "b"}, seq: 1},
		{Info: contracts.ModuleInfo{ID: "a"}, seq: 1},
		{Info: contracts.ModuleInfo{ID: "c"}, seq: 0},
	}
	sortEntries(entries)
	if got := ids(entries); !equal(got, []string{"c", "a", "b"}) {
		t.Fatalf("got %v", got)
	}
}

func TestProvider_MovesToNextOnUnregister(t *testing.T) {
	r := New()
	_ = r.Register(capMod("first", "auth"), nil)
	_ = r.Register(capMod("second", "auth"), nil)
	if err := r.Unregister("first"); err != nil {
		t.Fatal(err)
	}
	if p, ok := r.Provider("auth"); !ok || p.Info.ID != "second" {
		t.Fatalf("Provider after unregister = %v, %v", p, ok)
	}
	_ = r.Unregister("second")
	if _, ok := r.Provider("auth"); ok {
		t.Fatal("no provider expected")
	}
}

func TestRegister_DuplicateWrapsSentinel(t *testing.T) {
	r := New()
	_ = r.Register(capMod("m"), nil)
	err := r.Register(capMod("m"), nil)
	if !errors.Is(err, ErrAlreadyRegistered) {
		t.Fatalf("err = %v, want ErrAlreadyRegistered", err)
	}
}

func TestRegisterWith_ReplaceKeepsOrderAndSwapsCaps(t *testing.T) {
	r := New()
	_ = r.Register(capMod("owner", "auth", "old.cap"), nil)
	_ = r.Register(capMod("other", "auth"), nil)
	_ = r.SetState("owner", contracts.ModuleStateStarting)

	replacement := capMod("owner", "auth", "new.cap")
	replaced, err := r.RegisterWith(replacement, []string{"dep"}, RegisterOptions{Replace: true})
	if err != nil || !replaced {
		t.Fatalf("RegisterWith replace = %v, %v", replaced, err)
	}
	e, _ := r.Get("owner")
	if e.Module != replacement || e.State != contracts.ModuleStateRegistered || len(e.Deps) != 1 {
		t.Fatalf("entry not replaced: %+v", e)
	}
	if r.SupportsCapability("owner", "old.cap") || len(r.ListByCapability("old.cap")) != 0 {
		t.Fatal("old capability still indexed")
	}
	if !r.SupportsCapability("owner", "new.cap") {
		t.Fatal("new capability not indexed")
	}
	if p, _ := r.Provider("auth"); p.Info.ID != "owner" {
		t.Fatalf("replace lost provider-of-record position: %s", p.Info.ID)
	}
	if r.Count() != 2 {
		t.Fatalf("count %d", r.Count())
	}

	// Replace of an unknown ID is a plain registration.
	replaced, err = r.RegisterWith(capMod("fresh"), nil, RegisterOptions{Replace: true})
	if err != nil || replaced {
		t.Fatalf("fresh = %v, %v", replaced, err)
	}
}

func TestRegisterWith_Exclusive(t *testing.T) {
	r := New()
	opts := RegisterOptions{Exclusive: []string{"auth", "identity"}}
	if _, err := r.RegisterWith(capMod("auth-local", "auth", "identity"), nil, opts); err != nil {
		t.Fatal(err)
	}
	_, err := r.RegisterWith(capMod("impostor", "identity"), nil, opts)
	if !errors.Is(err, ErrExclusiveConflict) {
		t.Fatalf("err = %v, want ErrExclusiveConflict", err)
	}
	if _, gerr := r.Get("impostor"); gerr == nil {
		t.Fatal("rejected module must not be registered")
	}
	// Non-exclusive capabilities and the same ID (replace) are fine.
	if _, err := r.RegisterWith(capMod("helper", "metrics"), nil, opts); err != nil {
		t.Fatal(err)
	}
	opts.Replace = true
	if _, err := r.RegisterWith(capMod("auth-local", "auth", "identity"), nil, opts); err != nil {
		t.Fatalf("same-ID replace with exclusive caps: %v", err)
	}
	// Without Exclusive, a second provider is accepted (dev) and listed second.
	if _, err := r.RegisterWith(capMod("second", "auth"), nil, RegisterOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := ids(r.ListByCapability("auth")); !equal(got, []string{"auth-local", "second"}) {
		t.Fatalf("auth providers = %v", got)
	}
}

// Concurrent registrations cannot both claim an exclusive capability.
func TestRegisterWith_ExclusiveConcurrent(t *testing.T) {
	r := New()
	opts := RegisterOptions{Exclusive: []string{"call.policy"}}
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := string(rune('a' + i))
			if _, err := r.RegisterWith(capMod(id, "call.policy"), nil, opts); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if ok != 1 || len(r.ListByCapability("call.policy")) != 1 {
		t.Fatalf("%d registrations succeeded, want exactly 1", ok)
	}
}

func TestStartupOrder_Deterministic(t *testing.T) {
	r := New()
	for _, id := range []string{"c", "a", "b"} {
		_ = r.Register(capMod(id), nil)
	}
	order, err := r.StartupOrder()
	if err != nil {
		t.Fatal(err)
	}
	if !equal(order, []string{"c", "a", "b"}) {
		t.Fatalf("order %v", order)
	}
}
