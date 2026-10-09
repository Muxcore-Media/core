package erasure_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	"github.com/Muxcore-Media/core/sdk/go/module/erasure"
	"github.com/Muxcore-Media/core/sdk/go/module/erasure/erasuretest"
	"github.com/Muxcore-Media/core/sdk/go/module/meshtls"
)

const (
	providerID = "auth-local"
	ownerID    = "userdata-local"
)

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	b  bytes.Buffer
	mu sync.Mutex
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// clearMeshEnv removes every variable that changes the transport, so each
// test states its environment explicitly.
func clearMeshEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"MUXCORE_INSECURE_DISABLE_TLS", "MUXCORE_DEV_TLS_SKIP", "MUXCORE_GRPC_INSECURE",
		meshtls.EnvTLSCert, meshtls.EnvTLSKey, meshtls.EnvTLSCA, meshtls.EnvTLSServerName,
		"MUXCORE_PROFILE", "MUXCORE_MESH_DIAL_LOCAL", erasure.EnvSweepInterval} {
		t.Setenv(k, "")
	}
}

type harness struct {
	pki      *erasuretest.PKI
	provider *erasuretest.Provider
	disc     *erasuretest.Discovery
	owner    *erasuretest.Owner
	dialer   *erasure.ProviderDialer
	logs     *syncBuffer
	addr     string
}

// newHarness serves a fake auth-local over real mTLS on loopback, registers
// it with a fake core discovery, and builds an owner with mesh identity
// userdata-local. rows are the owner's rows per user id.
func newHarness(t *testing.T, rows map[string]int) *harness {
	t.Helper()
	clearMeshEnv(t)
	h := &harness{pki: erasuretest.NewPKI(t), provider: erasuretest.NewProvider(), logs: &syncBuffer{}}
	h.provider.Allowed = map[string]bool{ownerID: true}
	sc, sk := h.pki.Issue(t, providerID)
	h.addr = erasuretest.ServeTLS(t, h.provider, sc, sk, h.pki.CAFile)
	h.disc = erasuretest.NewDiscovery(erasuretest.Module(providerID, h.addr))
	cc, ck := h.pki.Issue(t, ownerID)
	h.owner = erasuretest.NewOwner(ownerID, rows)
	h.dialer = &erasure.ProviderDialer{Discovery: h.disc, CertFile: cc, KeyFile: ck, CAFile: h.pki.CAFile}
	return h
}

func (h *harness) reconciler(t *testing.T, mutate ...func(*erasure.Config)) *erasure.Reconciler {
	t.Helper()
	cfg := erasure.Config{
		Owner: h.owner, Dialer: h.dialer,
		Logger:   slog.New(slog.NewTextHandler(h.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Interval: time.Minute, AckBackoff: time.Millisecond, RetryBackoff: 10 * time.Millisecond,
		CallTimeout: 5 * time.Second,
	}
	for _, m := range mutate {
		m(&cfg)
	}
	r, err := erasure.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func sweep(t *testing.T, r *erasure.Reconciler) (erasure.Result, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return r.SweepOnce(ctx)
}

func mustSweep(t *testing.T, r *erasure.Reconciler) erasure.Result {
	t.Helper()
	res, err := sweep(t, r)
	if err != nil {
		t.Fatalf("sweep: %v (result %+v)", err, res)
	}
	return res
}

func wantLatest(t *testing.T, p *erasuretest.Provider, erasureID string, outcome authv1.ErasureOutcome, detail string) erasuretest.Ack {
	t.Helper()
	a, ok := p.Latest(erasureID, ownerID)
	if !ok {
		t.Fatalf("no acknowledgement of %s by %s", erasureID, ownerID)
	}
	if a.Outcome != outcome || a.Detail != detail {
		t.Fatalf("ack of %s = %v/%q, want %v/%q", erasureID, a.Outcome, a.Detail, outcome, detail)
	}
	return a
}

func TestSweepAppliesAndAcknowledges(t *testing.T) {
	h := newHarness(t, map[string]int{"victim": 3, "bystander": 5})
	id := h.provider.AddErasure("victim", "")
	r := h.reconciler(t)
	res := mustSweep(t, r)
	if res.Seen != 1 || res.Applied != 1 || res.Acked != 1 || res.Failed != 0 {
		t.Fatalf("result %+v", res)
	}
	if h.owner.Rows("victim") != 0 || h.owner.Rows("bystander") != 5 {
		t.Fatalf("rows victim=%d bystander=%d", h.owner.Rows("victim"), h.owner.Rows("bystander"))
	}
	a := wantLatest(t, h.provider, id, authv1.ErasureOutcome_ERASURE_OUTCOME_OK, "")
	if a.Counts["rows"] != 3 {
		t.Errorf("counts %v", a.Counts)
	}
	st := r.Status()
	if st.State != erasure.StateOK || st.Provider != providerID || st.Sweeps != 1 {
		t.Errorf("status %+v", st)
	}
	if !r.Erased("victim") || r.Erased("bystander") {
		t.Error("Erased() does not reflect the ledger")
	}
}

func TestSweepPagesThroughWholeLedger(t *testing.T) {
	rows := map[string]int{}
	h := newHarness(t, rows)
	const n = 250
	ids := make([]string, 0, n)
	for i := range n {
		u := fmt.Sprintf("user-%03d", i)
		ids = append(ids, h.provider.AddErasure(u, "household"))
	}
	r := h.reconciler(t, func(c *erasure.Config) { c.PageSize = 7 })
	res := mustSweep(t, r)
	if res.Seen != n || res.Applied != n || res.Acked != n {
		t.Fatalf("result %+v", res)
	}
	for _, id := range ids {
		if h.owner.ApplyCalls(id) != 1 {
			t.Fatalf("%s applied %d times", id, h.owner.ApplyCalls(id))
		}
		wantLatest(t, h.provider, id, authv1.ErasureOutcome_ERASURE_OUTCOME_OK, "")
	}
	if list, _ := h.provider.Calls(); list != (n+6)/7 {
		t.Errorf("list calls %d, want %d", list, (n+6)/7)
	}
}

func TestSecondSweepIsNoOp(t *testing.T) {
	h := newHarness(t, map[string]int{"a": 1, "b": 2})
	h.provider.AddErasure("a", "")
	h.provider.AddErasure("b", "")
	r := h.reconciler(t)
	mustSweep(t, r)
	acks := len(h.provider.Acks())
	res := mustSweep(t, r)
	if res.Applied != 0 || res.Acked != 0 || res.Skipped != 2 {
		t.Fatalf("second sweep %+v", res)
	}
	if h.owner.TotalApplyCalls() != 2 || len(h.provider.Acks()) != acks {
		t.Fatalf("second sweep changed state: apply=%d acks=%d", h.owner.TotalApplyCalls(), len(h.provider.Acks()))
	}
}

// The owner's local record says applied but the provider has no OK
// acknowledgement (lost ack, or a restore of the provider): re-acknowledge,
// never re-apply.
func TestAlreadyAppliedIsReacknowledgedWithoutApply(t *testing.T) {
	// The victim's rows went in the earlier, committed application.
	h := newHarness(t, map[string]int{"bystander": 4})
	id := h.provider.AddErasure("victim", "")
	h.owner.MarkApplied(id)
	r := h.reconciler(t)
	res := mustSweep(t, r)
	if h.owner.ApplyCalls(id) != 0 {
		t.Fatalf("Apply called %d times for an applied tombstone", h.owner.ApplyCalls(id))
	}
	if res.Applied != 0 || res.Acked != 1 {
		t.Fatalf("result %+v", res)
	}
	wantLatest(t, h.provider, id, authv1.ErasureOutcome_ERASURE_OUTCOME_OK, "")
}

func TestLostAcknowledgementIsResentWithoutReapplying(t *testing.T) {
	h := newHarness(t, map[string]int{"victim": 2})
	id := h.provider.AddErasure("victim", "")
	h.provider.FailAcks(100, codes.Unavailable)
	r := h.reconciler(t, func(c *erasure.Config) { c.AckAttempts = 3 })
	res, err := sweep(t, r)
	if err == nil || res.Applied != 1 || res.Acked != 0 || res.Errors != 1 {
		t.Fatalf("first sweep: %+v %v", res, err)
	}
	if _, acks := h.provider.Calls(); acks != 3 {
		t.Errorf("ack attempts %d, want 3", acks)
	}
	h.provider.FailAcks(0, codes.OK)
	res = mustSweep(t, r)
	if h.owner.ApplyCalls(id) != 1 || res.Applied != 0 || res.Acked != 1 {
		t.Fatalf("second sweep: apply=%d %+v", h.owner.ApplyCalls(id), res)
	}
	wantLatest(t, h.provider, id, authv1.ErasureOutcome_ERASURE_OUTCOME_OK, "")
}

func TestTransientAckFailureRetriedWithinSweep(t *testing.T) {
	h := newHarness(t, map[string]int{"victim": 1})
	id := h.provider.AddErasure("victim", "")
	h.provider.FailAcks(2, codes.Unavailable)
	r := h.reconciler(t)
	res := mustSweep(t, r)
	if res.Acked != 1 {
		t.Fatalf("result %+v", res)
	}
	wantLatest(t, h.provider, id, authv1.ErasureOutcome_ERASURE_OUTCOME_OK, "")
	if _, acks := h.provider.Calls(); acks != 3 {
		t.Errorf("ack calls %d, want 3", acks)
	}
}

func TestPermanentAckRejectionNotRetried(t *testing.T) {
	h := newHarness(t, map[string]int{"victim": 1})
	h.provider.AddErasure("victim", "")
	h.provider.FailAcks(1, codes.PermissionDenied)
	r := h.reconciler(t)
	res, err := sweep(t, r)
	if err == nil || res.Errors != 1 {
		t.Fatalf("result %+v %v", res, err)
	}
	if _, acks := h.provider.Calls(); acks != 1 {
		t.Errorf("PermissionDenied retried: %d calls", acks)
	}
}

func TestApplyFailureAcksFailedAndNextSweepCompletes(t *testing.T) {
	h := newHarness(t, map[string]int{"victim": 3, "bystander": 1})
	id := h.provider.AddErasure("victim", "")
	var mu sync.Mutex
	failures := 1
	h.owner.FailApply = func(erasure.Tombstone) error {
		mu.Lock()
		defer mu.Unlock()
		if failures > 0 {
			failures--
			return errors.New("injected mid-transaction failure")
		}
		return nil
	}
	r := h.reconciler(t)
	res, err := sweep(t, r)
	if err == nil || res.Failed != 1 || res.Applied != 0 {
		t.Fatalf("first sweep: %+v %v", res, err)
	}
	wantLatest(t, h.provider, id, authv1.ErasureOutcome_ERASURE_OUTCOME_FAILED, erasure.DetailApplyFailed)
	if h.owner.Rows("victim") != 3 || h.owner.IsApplied(id) {
		t.Fatal("failed apply changed state")
	}
	if r.Status().State != erasure.StateError {
		t.Errorf("status %+v", r.Status())
	}
	res = mustSweep(t, r)
	if res.Applied != 1 {
		t.Fatalf("second sweep %+v", res)
	}
	wantLatest(t, h.provider, id, authv1.ErasureOutcome_ERASURE_OUTCOME_OK, "")
	if h.owner.Rows("victim") != 0 || h.owner.Rows("bystander") != 1 {
		t.Fatal("second sweep did not erase exactly the victim")
	}
}

func TestApplyDetailCodeAndUnsupportedOwner(t *testing.T) {
	h := newHarness(t, map[string]int{"a": 1, "b": 1})
	ida := h.provider.AddErasure("a", "")
	idb := h.provider.AddErasure("b", "")
	h.owner.FailApply = func(ts erasure.Tombstone) error {
		if ts.UserID == "a" {
			return erasure.WithDetail("db_locked", errors.New("locked"))
		}
		return fmt.Errorf("cannot: %w", erasure.ErrUnsupported)
	}
	r := h.reconciler(t)
	if _, err := sweep(t, r); err == nil {
		t.Fatal("expected incomplete sweep")
	}
	wantLatest(t, h.provider, ida, authv1.ErasureOutcome_ERASURE_OUTCOME_FAILED, "db_locked")
	wantLatest(t, h.provider, idb, authv1.ErasureOutcome_ERASURE_OUTCOME_UNSUPPORTED, erasure.DetailUnsupported)
}

func TestPostconditionUnmetAcksFailed(t *testing.T) {
	h := newHarness(t, map[string]int{"victim": 3})
	id := h.provider.AddErasure("victim", "")
	h.owner.Leak = func(erasure.Tombstone) int { return 1 }
	r := h.reconciler(t)
	res, err := sweep(t, r)
	if err == nil || res.Failed != 1 {
		t.Fatalf("result %+v %v", res, err)
	}
	wantLatest(t, h.provider, id, authv1.ErasureOutcome_ERASURE_OUTCOME_FAILED, erasure.DetailPostconditionUnmet)
	// Still never re-applied; the failure stays visible until fixed.
	if _, err := sweep(t, r); err == nil {
		t.Fatal("unmet post-condition must keep failing")
	}
	if h.owner.ApplyCalls(id) != 1 {
		t.Fatalf("re-applied: %d", h.owner.ApplyCalls(id))
	}
}

func TestOwnerAppliedErrorLeavesTombstoneForNextSweep(t *testing.T) {
	h := newHarness(t, map[string]int{"victim": 1})
	h.provider.AddErasure("victim", "")
	h.owner.FailApplied = func(string) error { return errors.New("db closed") }
	r := h.reconciler(t)
	res, err := sweep(t, r)
	if err == nil || res.Errors != 1 || h.owner.TotalApplyCalls() != 0 || len(h.provider.Acks()) != 0 {
		t.Fatalf("result %+v %v", res, err)
	}
}

func TestInvalidTombstonesAreNeverApplied(t *testing.T) {
	h := newHarness(t, map[string]int{"": 9, " u1": 9, "u2": 9})
	now := time.Now().UTC().Format(time.RFC3339)
	h.provider.AddRaw(&authv1.UserErasure{ErasureId: "er-empty-user", UserId: "", DeletedAt: now})
	h.provider.AddRaw(&authv1.UserErasure{ErasureId: "er-padded", UserId: " u1", DeletedAt: now})
	h.provider.AddRaw(&authv1.UserErasure{ErasureId: "er-bad-time", UserId: "u2", DeletedAt: "later"})
	h.provider.AddRaw(&authv1.UserErasure{ErasureId: "bad id\n", UserId: "u2", DeletedAt: now})
	r := h.reconciler(t)
	res, err := sweep(t, r)
	if err == nil || res.Errors != 4 || res.Applied != 0 {
		t.Fatalf("result %+v %v", res, err)
	}
	if h.owner.TotalApplyCalls() != 0 {
		t.Fatal("Apply called for an invalid tombstone")
	}
	for _, id := range []string{"er-empty-user", "er-padded", "er-bad-time"} {
		wantLatest(t, h.provider, id, authv1.ErasureOutcome_ERASURE_OUTCOME_FAILED, erasure.DetailInvalidTombstone)
	}
	if strings.Contains(h.logs.String(), "bad id\n") {
		t.Error("malformed erasure id logged verbatim")
	}
}

func TestProviderDownErasesNothingAndBacksOff(t *testing.T) {
	h := newHarness(t, map[string]int{"victim": 1})
	// A port with nothing listening.
	var lc net.ListenConfig
	lis, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := lis.Addr().String()
	_ = lis.Close()
	h.disc.Set("identity", erasuretest.Module(providerID, dead))
	h.provider.AddErasure("victim", "")

	r := h.reconciler(t, func(c *erasure.Config) {
		c.CallTimeout = 300 * time.Millisecond
		c.RetryBackoff = 20 * time.Millisecond
	})
	if _, err := sweep(t, r); err == nil {
		t.Fatal("sweep against a down provider succeeded")
	}
	if r.Status().State != erasure.StateError {
		t.Errorf("status %+v", r.Status())
	}

	// Discovery itself failing is the same.
	h.disc.Err = status.Error(codes.Unavailable, "core down")
	if _, err := sweep(t, r); err == nil {
		t.Fatal("sweep with discovery down succeeded")
	}
	h.disc.Err = nil

	// Run retries with backoff, sooner than the interval, erasing nothing.
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	before := h.disc.Calls("identity")
	if err := r.Run(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run: %v", err)
	}
	retries := h.disc.Calls("identity") - before
	if retries < 3 || retries > 10 {
		t.Errorf("retries in 1.5s with 20ms doubling backoff = %d", retries)
	}
	if h.owner.TotalApplyCalls() != 0 || h.owner.Rows("victim") != 1 || len(h.provider.Acks()) != 0 {
		t.Fatal("data changed while the provider was down")
	}
	// The provider listing errors (e.g. database down) is also nothing erased.
	h.disc.Set("identity", erasuretest.Module(providerID, h.addr))
	h.provider.SetListError(status.Error(codes.Unavailable, "db down"))
	if _, err := sweep(t, r); err == nil || h.owner.TotalApplyCalls() != 0 {
		t.Fatalf("list error: %v apply=%d", err, h.owner.TotalApplyCalls())
	}
}

// impostor serves a ledger full of tombstones for the bystander over a
// same-CA certificate that is not the identity provider's.
func (h *harness) impostor(t *testing.T, cn string, dnsNames ...string) (*erasuretest.Provider, string) {
	t.Helper()
	p := erasuretest.NewProvider()
	p.AddErasure("bystander", "")
	sc, sk := h.pki.Issue(t, cn, dnsNames...)
	return p, erasuretest.ServeTLS(t, p, sc, sk, h.pki.CAFile)
}

func TestLedgerFromWrongCNIsNeverActedOn(t *testing.T) {
	cases := map[string]func(t *testing.T, h *harness) (*erasuretest.Provider, string){
		// The decisive case: a same-CA module certificate that even carries
		// the provider's name as a SAN. Only the CN check rejects it.
		"same_ca_other_module_with_provider_san": func(t *testing.T, h *harness) (*erasuretest.Provider, string) {
			return h.impostor(t, "request-media", providerID, "localhost")
		},
		"same_ca_other_module": func(t *testing.T, h *harness) (*erasuretest.Provider, string) {
			return h.impostor(t, "request-media")
		},
		"other_ca_claiming_provider": func(t *testing.T, _ *harness) (*erasuretest.Provider, string) {
			other := erasuretest.NewPKI(t)
			p := erasuretest.NewProvider()
			p.AddErasure("bystander", "")
			sc, sk := other.Issue(t, providerID)
			return p, erasuretest.ServeTLS(t, p, sc, sk, other.CAFile)
		},
		"server_name_override_is_ignored": func(t *testing.T, h *harness) (*erasuretest.Provider, string) {
			t.Setenv(meshtls.EnvTLSServerName, "request-media")
			return h.impostor(t, "request-media")
		},
		"plaintext_server_without_dev_flag": func(t *testing.T, _ *harness) (*erasuretest.Provider, string) {
			p := erasuretest.NewProvider()
			p.AddErasure("bystander", "")
			return p, erasuretest.ServePlain(t, p)
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, map[string]int{"bystander": 7})
			imp, addr := mk(t, h)
			h.disc.Set("identity", erasuretest.Module(providerID, addr))
			r := h.reconciler(t, func(c *erasure.Config) { c.CallTimeout = 2 * time.Second })
			_, err := sweep(t, r)
			if err == nil {
				t.Fatal("ledger from an impostor was accepted")
			}
			// With the provider's name as a SAN only the CN pin can refuse it.
			if strings.HasPrefix(name, "same_ca_other_module_with_provider_san") &&
				!strings.Contains(err.Error(), erasure.ErrProviderIdentity.Error()) {
				t.Fatalf("refused for the wrong reason: %v", err)
			}
			if h.owner.TotalApplyCalls() != 0 || h.owner.Rows("bystander") != 7 {
				t.Fatal("impostor ledger was acted on")
			}
			if list, _ := imp.Calls(); list != 0 {
				t.Fatalf("impostor received %d ledger requests (handshake must fail first)", list)
			}
			if r.Erased("bystander") {
				t.Fatal("impostor ledger leaked into Erased()")
			}
		})
	}
}

func TestDialerRefusesAmbiguousOrMisconfiguredProvider(t *testing.T) {
	h := newHarness(t, map[string]int{"bystander": 1})
	h.provider.AddErasure("bystander", "")
	_, evil := h.impostor(t, "evil")
	h.disc.Set("identity", erasuretest.Module(providerID, h.addr), erasuretest.Module("evil", evil))
	r := h.reconciler(t)
	if _, err := sweep(t, r); !errors.Is(err, erasure.ErrAmbiguousProvider) {
		t.Fatalf("two identity providers: %v", err)
	}
	h.disc.Set("identity")
	if _, err := sweep(t, r); !errors.Is(err, erasure.ErrNoProvider) {
		t.Fatalf("no provider: %v", err)
	}
	h.disc.Set("identity", erasuretest.Module(providerID, h.addr))

	// The module's own certificate must be its identity.
	other, otherKey := h.pki.Issue(t, "someone-else")
	bad := *h.dialer
	bad.CertFile, bad.KeyFile = other, otherKey
	rb := h.reconciler(t, func(c *erasure.Config) { c.Dialer = &bad })
	if _, err := sweep(t, rb); !errors.Is(err, erasure.ErrTLSConfig) {
		t.Fatalf("foreign client certificate: %v", err)
	}
	// No CA: system roots are never used.
	noCA := *h.dialer
	noCA.CAFile = ""
	rn := h.reconciler(t, func(c *erasure.Config) { c.Dialer = &noCA })
	if _, err := sweep(t, rn); !errors.Is(err, erasure.ErrTLSConfig) {
		t.Fatalf("missing CA: %v", err)
	}
	noCert := *h.dialer
	noCert.CertFile, noCert.KeyFile = "", ""
	rc := h.reconciler(t, func(c *erasure.Config) { c.Dialer = &noCert })
	if _, err := sweep(t, rc); !errors.Is(err, erasure.ErrTLSConfig) {
		t.Fatalf("missing client certificate: %v", err)
	}
	if h.owner.TotalApplyCalls() != 0 {
		t.Fatal("applied despite a refused dial")
	}
}

func TestNonAllowlistedCallerGetsNothing(t *testing.T) {
	h := newHarness(t, map[string]int{"victim": 1})
	h.provider.AddErasure("victim", "")
	h.provider.Allowed = map[string]bool{"some-other-module": true}
	r := h.reconciler(t)
	_, err := sweep(t, r)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}
	if h.owner.TotalApplyCalls() != 0 {
		t.Fatal("applied without being allowlisted")
	}
}

func TestPlaintextOnlyUnderExplicitDevFlag(t *testing.T) {
	h := newHarness(t, map[string]int{"victim": 2})
	plain := erasuretest.NewProvider()
	plain.Allowed = map[string]bool{ownerID: true}
	id := plain.AddErasure("victim", "")
	h.disc.Set("identity", erasuretest.Module(providerID, erasuretest.ServePlain(t, plain)))
	r := h.reconciler(t, func(c *erasure.Config) { c.CallTimeout = 2 * time.Second })

	// Without the flag the dialer insists on TLS and fails the handshake.
	if _, err := sweep(t, r); err == nil || h.owner.TotalApplyCalls() != 0 {
		t.Fatalf("plaintext without flag: %v apply=%d", err, h.owner.TotalApplyCalls())
	}
	// The flag in the household profile is refused, as meshtls refuses it.
	t.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	t.Setenv("MUXCORE_PROFILE", "household")
	if _, err := sweep(t, r); !errors.Is(err, meshtls.ErrInsecureInHousehold) {
		t.Fatalf("household plaintext: %v", err)
	}
	t.Setenv("MUXCORE_PROFILE", "staging")
	if _, err := sweep(t, r); !errors.Is(err, meshtls.ErrInsecureInHousehold) {
		t.Fatalf("staging plaintext: %v", err)
	}
	if h.owner.TotalApplyCalls() != 0 {
		t.Fatal("applied over refused plaintext")
	}
	// dev + flag: plaintext, caller identified by x-caller-id.
	t.Setenv("MUXCORE_PROFILE", "dev")
	mustSweep(t, r)
	if h.owner.Rows("victim") != 0 {
		t.Fatal("dev plaintext sweep did not apply")
	}
	if a, ok := plain.Latest(id, ownerID); !ok || a.Outcome != authv1.ErasureOutcome_ERASURE_OUTCOME_OK {
		t.Fatalf("dev ack %+v %v", a, ok)
	}
}

// unimplementedProvider is a provider built before ADR-0035 (auth-oidc).
type unimplementedProvider struct {
	authv1.UnimplementedAuthServiceServer
}

func TestUnimplementedProviderIsUnsupportedNotOK(t *testing.T) {
	h := newHarness(t, map[string]int{"victim": 1})
	sc, sk := h.pki.Issue(t, providerID)
	h.disc.Set("identity", erasuretest.Module(providerID, erasuretest.ServeTLS(t, unimplementedProvider{}, sc, sk, h.pki.CAFile)))
	r := h.reconciler(t)
	for range 3 {
		if _, err := sweep(t, r); !errors.Is(err, erasure.ErrProviderUnsupported) {
			t.Fatalf("sweep: %v", err)
		}
	}
	st := r.Status()
	if st.State != erasure.StateUnsupported || st.State == erasure.StateOK {
		t.Fatalf("status %+v, want unsupported", st)
	}
	if h.owner.TotalApplyCalls() != 0 {
		t.Fatal("applied without a ledger")
	}
	if n := strings.Count(h.logs.String(), "no erasure ledger"); n != 1 {
		t.Errorf("unsupported logged %d times, want once", n)
	}
	// Once the provider gains the ledger, the state recovers.
	h.disc.Set("identity", erasuretest.Module(providerID, h.addr))
	mustSweep(t, r)
	if r.Status().State != erasure.StateOK {
		t.Fatalf("status after upgrade %+v", r.Status())
	}
}

// blockingOwner blocks in Apply until the context is cancelled.
type blockingOwner struct {
	*erasuretest.Owner
	entered chan struct{}
}

func (b *blockingOwner) Apply(ctx context.Context, _ erasure.Tombstone) (erasure.Counts, error) {
	close(b.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestCancellationStopsWithoutAckOrLeak(t *testing.T) {
	h := newHarness(t, map[string]int{"victim": 1})
	h.provider.AddErasure("victim", "")
	baseline := runtime.NumGoroutine()

	bo := &blockingOwner{Owner: h.owner, entered: make(chan struct{})}
	r := h.reconciler(t, func(c *erasure.Config) { c.Owner = bo })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	select {
	case <-bo.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("Apply never reached")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop after cancellation")
	}
	if acks := h.provider.Acks(); len(acks) != 0 {
		t.Fatalf("cancelled apply was acknowledged: %+v", acks)
	}
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > baseline && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > baseline {
		buf := make([]byte, 1<<16)
		t.Fatalf("goroutines %d > baseline %d after Run returned:\n%s", n, baseline, buf[:runtime.Stack(buf, true)])
	}
}

func TestRunSweepsAtStartupAndOnConcurrentTriggers(t *testing.T) {
	h := newHarness(t, map[string]int{"first": 1, "second": 1})
	h.owner.ApplyDelay = 5 * time.Millisecond
	first := h.provider.AddErasure("first", "")
	r := h.reconciler(t, func(c *erasure.Config) { c.Interval = time.Hour })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	waitFor(t, func() bool { return h.owner.IsApplied(first) }, "startup sweep")
	if err := r.Run(ctx); !errors.Is(err, erasure.ErrAlreadyRunning) {
		t.Fatalf("second Run: %v", err)
	}

	second := h.provider.AddErasure("second", "")
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Trigger()
		}()
	}
	// Manual sweeps race the triggered ones; all are serialised.
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = r.SweepOnce(ctx)
		}()
	}
	wg.Wait()
	waitFor(t, func() bool { return h.owner.IsApplied(second) }, "triggered sweep")
	if h.owner.MaxConcurrentApply() != 1 {
		t.Fatalf("Apply ran concurrently (%d)", h.owner.MaxConcurrentApply())
	}
	if h.owner.ApplyCalls(first) != 1 || h.owner.ApplyCalls(second) != 1 {
		t.Fatalf("apply counts first=%d second=%d", h.owner.ApplyCalls(first), h.owner.ApplyCalls(second))
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run: %v", err)
	}
}

func TestNoUserIDsInLogs(t *testing.T) {
	const victim = "victim-7f3a9c"
	h := newHarness(t, map[string]int{victim: 1})
	h.provider.AddErasure(victim, "tenant-x")
	h.owner.FailApply = func(ts erasure.Tombstone) error {
		return fmt.Errorf("delete rows for %s in tenant %s: disk full", ts.UserID, ts.TenantID)
	}
	r := h.reconciler(t)
	_, _ = sweep(t, r)
	h.owner.FailApply = nil
	h.owner.Leak = func(erasure.Tombstone) int { return 1 }
	_, _ = sweep(t, r)
	logs := h.logs.String()
	if logs == "" {
		t.Fatal("expected warnings to be logged")
	}
	if strings.Contains(logs, victim) {
		t.Fatalf("user id logged:\n%s", logs)
	}
	if !strings.Contains(logs, "erasure_id=er-") {
		t.Errorf("erasure id not logged:\n%s", logs)
	}
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestFakeProviderStatusReflectsAcknowledgements(t *testing.T) {
	h := newHarness(t, map[string]int{"a": 1, "b": 1})
	h.provider.Required = []string{ownerID, "request-media"}
	ida := h.provider.AddErasure("a", "")
	h.provider.AddErasure("b", "")
	mustSweep(t, h.reconciler(t))
	ctx := context.Background()

	one, err := h.provider.GetUserErasureStatus(ctx, &authv1.GetUserErasureStatusRequest{ErasureId: ida})
	if err != nil || len(one.GetErasures()) != 1 {
		t.Fatalf("status of %s: %v %v", ida, one, err)
	}
	st := one.GetErasures()[0]
	if st.GetComplete() || len(st.GetModules()) != 2 {
		t.Fatalf("status %+v: request-media has not acknowledged, must be incomplete", st)
	}
	for _, m := range st.GetModules() {
		switch m.GetModuleId() {
		case ownerID:
			if m.GetOutcome() != authv1.ErasureOutcome_ERASURE_OUTCOME_OK || m.GetAckedAt() == "" || !m.GetRequired() {
				t.Errorf("owner status %+v", m)
			}
		case "request-media":
			if m.GetOutcome() != authv1.ErasureOutcome_ERASURE_OUTCOME_UNSPECIFIED || m.GetAckedAt() != "" {
				t.Errorf("pending status %+v", m)
			}
		}
	}
	h.provider.Required = []string{ownerID}
	pending, err := h.provider.GetUserErasureStatus(ctx, &authv1.GetUserErasureStatusRequest{PendingOnly: true})
	if err != nil || len(pending.GetErasures()) != 0 {
		t.Fatalf("pending after all required acked: %v %v", pending, err)
	}
	if _, err := h.provider.GetUserErasureStatus(ctx, &authv1.GetUserErasureStatusRequest{ErasureId: "er-unknown"}); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown erasure: %v", err)
	}
}
