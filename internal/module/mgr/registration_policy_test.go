package mgr

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/pkg/contracts"
	modulev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/module/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// testRegCA is a throwaway core CA. Its leaf certificates are verified with
// x509 (as the TLS handshake would), so the peer contexts below carry real
// verified chains for peerid.VerifiedModuleID.
type testRegCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

func newTestRegCA(t *testing.T) *testRegCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "muxcore test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &testRegCA{cert: cert, key: key, pool: pool}
}

// moduleCtx returns a context whose gRPC peer presented a CA-signed client
// certificate for cn, verified against the CA.
func (ca *testRegCA) moduleCtx(t *testing.T, cn string) context.Context {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	chains, err := leaf.Verify(x509.VerifyOptions{
		Roots:     ca.pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	if err != nil {
		t.Fatalf("verify leaf: %v", err)
	}
	return peer.NewContext(context.Background(), &peer.Peer{
		Addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 4000},
		AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{leaf},
			VerifiedChains:   chains,
		}},
	})
}

// unverifiedCtx is a TLS peer that presented a certificate with no verified
// chain (e.g. self-signed, accepted by RequestClientCert): not an identity.
func unverifiedCtx(cn string) context.Context {
	return peer.NewContext(context.Background(), &peer.Peer{
		Addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 4001},
		AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{{Subject: pkix.Name{CommonName: cn}}},
		}},
	})
}

func regReq(id string, caps ...string) *modulev1.RegisterRequest {
	return &modulev1.RegisterRequest{
		ModuleId: id,
		ModuleInfo: &modulev1.ModuleInfo{
			Id: id, Name: id, Version: "1.0.0", Capabilities: caps, HttpAddr: "127.0.0.1:1",
		},
	}
}

type regFixture struct {
	m   *Manager
	reg *registry.Registry
	srv *registrationServer
	ca  *testRegCA
}

func newRegFixture(t *testing.T, pol RegistrationPolicy) *regFixture {
	t.Helper()
	m, reg, _ := testLifecycleMgr(t)
	m.SetRegistrationPolicy(pol)
	return &regFixture{m: m, reg: reg, srv: &registrationServer{mgr: m}, ca: newTestRegCA(t)}
}

// register returns the gRPC code (codes.OK when accepted) and fails the test
// on an unexpected Accepted=false response.
func (f *regFixture) register(t *testing.T, ctx context.Context, req *modulev1.RegisterRequest) codes.Code {
	t.Helper()
	resp, err := f.srv.Register(ctx, req)
	if err != nil {
		return status.Code(err)
	}
	if !resp.Accepted {
		return codes.AlreadyExists // registry-level rejection reported in-band
	}
	return codes.OK
}

func (f *regFixture) registered(id string) bool {
	_, err := f.reg.Get(id)
	return err == nil
}

var (
	devPolicy       = RegistrationPolicy{}
	householdPolicy = RegistrationPolicy{Household: true}
	stage2Policy    = RegistrationPolicy{Household: true, RequireModuleCerts: true}
)

func TestRegistrationPolicy_DefaultIsDev(t *testing.T) {
	m := NewManager("addr", nil, nil)
	if m.RegistrationPolicy().Household {
		t.Fatal("default policy should be dev")
	}
}

// ADR-0018 row 1: certificate CN != registering module ID → reject in both
// profiles, for security and other capabilities.
func TestRegister_CNMismatchRejected(t *testing.T) {
	for _, pol := range []RegistrationPolicy{devPolicy, householdPolicy} {
		for _, caps := range [][]string{{"auth"}, {"media.movies"}, nil} {
			f := newRegFixture(t, pol)
			ctx := f.ca.moduleCtx(t, "media-movies")
			if code := f.register(t, ctx, regReq("auth-local", caps...)); code != codes.PermissionDenied {
				t.Fatalf("household=%v caps=%v: code %v, want PermissionDenied", pol.Household, caps, code)
			}
			if f.registered("auth-local") {
				t.Fatal("rejected module was registered")
			}
			f.m.mu.Lock()
			_, tracked := f.m.proxies["auth-local"]
			f.m.mu.Unlock()
			if tracked {
				t.Fatal("rejected registration must not track a proxy")
			}
		}
	}
}

// ADR-0018 row 2: no certificate for a security capability → dev warn
// (accepted), household reject PermissionDenied.
func TestRegister_SecurityCapabilityWithoutCert(t *testing.T) {
	for _, capability := range SecurityCapabilities {
		for _, ctx := range []context.Context{context.Background(), unverifiedCtx("auth-local")} {
			dev := newRegFixture(t, devPolicy)
			if code := dev.register(t, ctx, regReq("auth-local", capability)); code != codes.OK {
				t.Fatalf("dev %s: code %v, want OK", capability, code)
			}
			hh := newRegFixture(t, householdPolicy)
			if code := hh.register(t, ctx, regReq("auth-local", capability)); code != codes.PermissionDenied {
				t.Fatalf("household %s: code %v, want PermissionDenied", capability, code)
			}
			if hh.registered("auth-local") {
				t.Fatal("household registered an uncertified security provider")
			}
		}
	}
}

func TestRegister_SecurityCapabilityWithCert(t *testing.T) {
	for _, pol := range []RegistrationPolicy{devPolicy, householdPolicy, stage2Policy} {
		f := newRegFixture(t, pol)
		ctx := f.ca.moduleCtx(t, "auth-local")
		if code := f.register(t, ctx, regReq("auth-local", "auth", "identity", "authorizer")); code != codes.OK {
			t.Fatalf("policy %+v: code %v", pol, code)
		}
	}
}

// ADR-0018 row 3: no certificate for other capabilities → dev allow,
// household stage 1 allow (warning), stage 2 (MUXCORE_REQUIRE_MODULE_CERTS)
// reject.
func TestRegister_OtherCapabilitiesWithoutCert(t *testing.T) {
	cases := []struct {
		name string
		pol  RegistrationPolicy
		want codes.Code
	}{
		{"dev", devPolicy, codes.OK},
		{"dev ignores require-certs", RegistrationPolicy{RequireModuleCerts: true}, codes.OK},
		{"household stage 1", householdPolicy, codes.OK},
		{"household stage 2", stage2Policy, codes.PermissionDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRegFixture(t, tc.pol)
			if code := f.register(t, context.Background(), regReq("media-movies", "media.movies")); code != tc.want {
				t.Fatalf("code %v, want %v", code, tc.want)
			}
			if code := f.register(t, context.Background(), regReq("no-caps")); code != tc.want {
				t.Fatalf("no capabilities: code %v, want %v", code, tc.want)
			}
			// With a certificate it is always accepted.
			if code := f.register(t, f.ca.moduleCtx(t, "certified"), regReq("certified", "media.movies")); code != codes.OK {
				t.Fatalf("certified: code %v", code)
			}
		})
	}
}

// ADR-0018 row 4: a second provider (different ID) of an exclusive security
// capability → dev warn and keep the first; household FailedPrecondition.
func TestRegister_SecondSecurityProvider(t *testing.T) {
	for _, capability := range SecurityCapabilities {
		t.Run(capability+"/dev", func(t *testing.T) {
			f := newRegFixture(t, devPolicy)
			if code := f.register(t, f.ca.moduleCtx(t, "first"), regReq("first", capability)); code != codes.OK {
				t.Fatal(code)
			}
			if code := f.register(t, context.Background(), regReq("second", capability)); code != codes.OK {
				t.Fatalf("dev second provider: code %v, want OK (warn)", code)
			}
			if p, ok := f.reg.Provider(capability); !ok || p.Info.ID != "first" {
				t.Fatalf("provider of record = %v, want first", p)
			}
		})
		t.Run(capability+"/household", func(t *testing.T) {
			f := newRegFixture(t, householdPolicy)
			if code := f.register(t, f.ca.moduleCtx(t, "first"), regReq("first", capability)); code != codes.OK {
				t.Fatal(code)
			}
			if code := f.register(t, f.ca.moduleCtx(t, "second"), regReq("second", capability, "other.cap")); code != codes.FailedPrecondition {
				t.Fatalf("household second provider: code %v, want FailedPrecondition", code)
			}
			if f.registered("second") {
				t.Fatal("conflicting provider was registered")
			}
			if p, _ := f.reg.Provider(capability); p.Info.ID != "first" {
				t.Fatalf("provider of record = %s", p.Info.ID)
			}
		})
	}
}

// An in-process security provider also owns its capability in household.
func TestRegister_HouseholdConflictWithInProcessProvider(t *testing.T) {
	f := newRegFixture(t, householdPolicy)
	inproc := &stubLifecycleModule{info: contracts.ModuleInfo{ID: "builtin-policy", Name: "b", Capabilities: []string{"call.policy"}}}
	if err := f.reg.Register(inproc, nil); err != nil {
		t.Fatal(err)
	}
	if code := f.register(t, f.ca.moduleCtx(t, "sidecar-policy"), regReq("sidecar-policy", "call.policy")); code != codes.FailedPrecondition {
		t.Fatalf("code %v", code)
	}
	// The verified owner of the in-process ID still cannot replace it.
	if code := f.register(t, f.ca.moduleCtx(t, "builtin-policy"), regReq("builtin-policy", "call.policy")); code != codes.AlreadyExists {
		t.Fatalf("replace of in-process module: code %v, want registry rejection", code)
	}
	if e, _ := f.reg.Get("builtin-policy"); e.Module != inproc {
		t.Fatal("in-process module was replaced")
	}
}

// ADR-0018 row 5: the same verified ID re-registers → atomic replace in both
// profiles; it keeps its provider-of-record position.
func TestRegister_SameVerifiedIDReplaces(t *testing.T) {
	for _, pol := range []RegistrationPolicy{devPolicy, householdPolicy} {
		f := newRegFixture(t, pol)
		ctx := f.ca.moduleCtx(t, "auth-local")
		if code := f.register(t, ctx, regReq("auth-local", "auth", "identity")); code != codes.OK {
			t.Fatal(code)
		}
		if !pol.Household {
			// dev: a second provider registered after the owner.
			if code := f.register(t, context.Background(), regReq("dev-auth", "auth")); code != codes.OK {
				t.Fatal(code)
			}
		}
		first, _ := f.reg.Get("auth-local")
		f.m.mu.Lock()
		firstProxy := f.m.proxies["auth-local"]
		f.m.mu.Unlock()

		req := regReq("auth-local", "auth", "identity")
		req.ModuleInfo.Version = "2.0.0"
		req.ModuleInfo.HttpAddr = "127.0.0.1:2"
		if code := f.register(t, ctx, req); code != codes.OK {
			t.Fatalf("household=%v: re-register code %v, want OK", pol.Household, code)
		}
		e, _ := f.reg.Get("auth-local")
		if e == first || e.Info.Version != "2.0.0" || e.Info.HTTPAddr != "127.0.0.1:2" {
			t.Fatalf("entry not replaced: %+v", e.Info)
		}
		f.m.mu.Lock()
		newProxy := f.m.proxies["auth-local"]
		f.m.mu.Unlock()
		if newProxy == firstProxy || newProxy != e.Module {
			t.Fatal("tracked proxy not swapped to the replacement")
		}
		if p, _ := f.reg.Provider("auth"); p.Info.ID != "auth-local" {
			t.Fatalf("household=%v: provider of record after replace = %s", pol.Household, p.Info.ID)
		}
	}
}

// Without a certificate a duplicate ID is refused (no replace) when the
// existing entry was registered with a verified certificate (both profiles),
// and always in household, so an unauthenticated peer cannot displace a
// certificate-registered module.
func TestRegister_UnverifiedDuplicateRefused(t *testing.T) {
	cases := []struct {
		name         string
		pol          RegistrationPolicy
		origVerified bool
	}{
		{"dev/verified original", devPolicy, true},
		{"household/verified original", householdPolicy, true},
		{"household/unverified original", householdPolicy, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRegFixture(t, tc.pol)
			origCtx := context.Background()
			if tc.origVerified {
				origCtx = f.ca.moduleCtx(t, "media-movies")
			}
			if code := f.register(t, origCtx, regReq("media-movies", "media.movies")); code != codes.OK {
				t.Fatal(code)
			}
			orig, _ := f.reg.Get("media-movies")
			if code := f.register(t, context.Background(), regReq("media-movies", "media.movies")); code != codes.AlreadyExists {
				t.Fatalf("unverified duplicate code %v, want registry rejection", code)
			}
			if e, _ := f.reg.Get("media-movies"); e != orig {
				t.Fatal("unverified duplicate replaced the entry")
			}
			f.m.mu.Lock()
			tracked := f.m.proxies["media-movies"]
			f.m.mu.Unlock()
			if tracked != orig.Module {
				t.Fatal("unverified duplicate displaced the tracked proxy")
			}
		})
	}
}

// dev profile: a module that restarts without unregistering (plaintext, no
// certificate) re-registers its own ID and replaces the stale entry instead
// of looping on "already registered". It keeps the provider-of-record
// position for its security capabilities; a different ID still cannot take
// them over or reuse the ID.
func TestRegister_DevUnverifiedSameIDReplaces(t *testing.T) {
	f := newRegFixture(t, devPolicy)
	if code := f.register(t, context.Background(), regReq("auth-local", "auth", "identity")); code != codes.OK {
		t.Fatal(code)
	}
	if code := f.register(t, context.Background(), regReq("dev-auth", "auth")); code != codes.OK {
		t.Fatal(code)
	}
	first, _ := f.reg.Get("auth-local")

	req := regReq("auth-local", "auth", "identity")
	req.ModuleInfo.Version = "2.0.0"
	req.ModuleInfo.HttpAddr = "127.0.0.1:2"
	if code := f.register(t, context.Background(), req); code != codes.OK {
		t.Fatalf("dev unverified re-register: code %v, want OK", code)
	}
	e, _ := f.reg.Get("auth-local")
	if e == first || e.Info.Version != "2.0.0" || e.Info.HTTPAddr != "127.0.0.1:2" {
		t.Fatalf("entry not replaced: %+v", e.Info)
	}
	f.m.mu.Lock()
	tracked := f.m.proxies["auth-local"]
	f.m.mu.Unlock()
	if tracked != e.Module {
		t.Fatal("tracked proxy not swapped to the replacement")
	}
	for _, c := range []string{"auth", "identity"} {
		if p, _ := f.reg.Provider(c); p.Info.ID != "auth-local" {
			t.Fatalf("provider of record for %s after replace = %s, want auth-local", c, p.Info.ID)
		}
	}

	// The other provider re-registering replaces only itself and does not
	// take over the capability.
	if code := f.register(t, context.Background(), regReq("dev-auth", "auth")); code != codes.OK {
		t.Fatalf("dev-auth re-register: %v", code)
	}
	if p, _ := f.reg.Provider("auth"); p.Info.ID != "auth-local" {
		t.Fatalf("provider of record = %s, want auth-local", p.Info.ID)
	}
	if n := len(f.reg.ListByCapability("auth")); n != 2 {
		t.Fatalf("auth providers = %d, want 2", n)
	}
}

// A verified certificate for a different ID still cannot register (and so
// cannot replace) an existing ID, in either profile.
func TestRegister_DifferentIDCannotReplace(t *testing.T) {
	for _, pol := range []RegistrationPolicy{devPolicy, householdPolicy} {
		f := newRegFixture(t, pol)
		if code := f.register(t, f.ca.moduleCtx(t, "auth-local"), regReq("auth-local", "auth")); code != codes.OK {
			t.Fatal(code)
		}
		orig, _ := f.reg.Get("auth-local")
		if code := f.register(t, f.ca.moduleCtx(t, "impostor"), regReq("auth-local", "auth")); code != codes.PermissionDenied {
			t.Fatalf("household=%v: code %v, want PermissionDenied", pol.Household, code)
		}
		if e, _ := f.reg.Get("auth-local"); e != orig {
			t.Fatal("entry replaced by a different ID")
		}
	}
}

// ADR-0018 row 6: Unregister is open in dev and requires CN == ID in
// household.
func TestUnregister_Policy(t *testing.T) {
	t.Run("dev open", func(t *testing.T) {
		f := newRegFixture(t, devPolicy)
		_ = f.register(t, f.ca.moduleCtx(t, "auth-local"), regReq("auth-local", "auth"))
		resp, err := f.srv.Unregister(context.Background(), &modulev1.UnregisterRequest{ModuleId: "auth-local"})
		if err != nil || !resp.Acknowledged {
			t.Fatalf("dev unregister: %v %v", resp, err)
		}
	})
	t.Run("household", func(t *testing.T) {
		f := newRegFixture(t, householdPolicy)
		if code := f.register(t, f.ca.moduleCtx(t, "auth-local"), regReq("auth-local", "auth")); code != codes.OK {
			t.Fatal(code)
		}
		for name, ctx := range map[string]context.Context{
			"no cert":         context.Background(),
			"unverified cert": unverifiedCtx("auth-local"),
			"other module":    f.ca.moduleCtx(t, "media-movies"),
		} {
			_, err := f.srv.Unregister(ctx, &modulev1.UnregisterRequest{ModuleId: "auth-local"})
			if status.Code(err) != codes.PermissionDenied {
				t.Fatalf("%s: err %v, want PermissionDenied", name, err)
			}
			if !f.registered("auth-local") {
				t.Fatalf("%s: module was unregistered", name)
			}
		}
		resp, err := f.srv.Unregister(f.ca.moduleCtx(t, "auth-local"), &modulev1.UnregisterRequest{ModuleId: "auth-local"})
		if err != nil || !resp.Acknowledged {
			t.Fatalf("owner unregister: %v %v", resp, err)
		}
		if f.registered("auth-local") {
			t.Fatal("module still registered")
		}
	})
}
