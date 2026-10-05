package grpcmesh

import (
	"context"
	"crypto/tls"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/internal/callerid"
	"github.com/Muxcore-Media/core/internal/events"
	"github.com/Muxcore-Media/core/pkg/contracts"
	eventsv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/events/v1"
	meshv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/mesh/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// recordingIdP resolves bearer tokens to users and records every call,
// including the hints it was given.
type recordingIdP struct {
	mu      sync.Mutex
	users   map[string]string // token -> user ID
	calls   int
	tokens  []string
	callers []string
}

func (p *recordingIdP) ExtractIdentity(ctx context.Context) (*contracts.Identity, error) {
	token, caller := identityHintsFromContext(ctx)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.tokens = append(p.tokens, token)
	p.callers = append(p.callers, caller)
	if id, ok := p.users[token]; ok && token != "" {
		return &contracts.Identity{ID: id, Kind: "user", Roles: []string{"admin"}}, nil
	}
	return nil, nil
}

func (p *recordingIdP) snapshot() (calls int, callers []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, append([]string(nil), p.callers...)
}

type recordingAuthorizer struct {
	mu    sync.Mutex
	users []string
}

func (a *recordingAuthorizer) Can(_ context.Context, s contracts.Session, _, _ string) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.users = append(a.users, s.UserID)
	return true, nil
}

func (a *recordingAuthorizer) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.users)
}

// recordingPublishPolicy allows everything and records the caller IDs the
// event bus saw.
type recordingPublishPolicy struct {
	mu      sync.Mutex
	callers []string
}

func (p *recordingPublishPolicy) CanPublish(_ context.Context, callerID, _ string) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.callers = append(p.callers, callerID)
	return true, nil
}

func (p *recordingPublishPolicy) last() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.callers) == 0 {
		return ""
	}
	return p.callers[len(p.callers)-1]
}

type principalHarness struct {
	client eventsv1.EventServiceClient
	idp    *recordingIdP
	authz  *recordingAuthorizer
	policy *recordingPublishPolicy
}

// startPrincipalServer runs an EventService behind the real auth interceptor
// with either plaintext (srvTLS nil) or the given server TLS config.
func startPrincipalServer(t *testing.T, insecureIDs bool, srvTLS *tls.Config, clientCreds credentials.TransportCredentials) *principalHarness {
	t.Helper()
	h := &principalHarness{
		idp:    &recordingIdP{users: map[string]string{"tok-alice": "alice"}},
		authz:  &recordingAuthorizer{},
		policy: &recordingPublishPolicy{},
	}
	bus := events.NewMemoryBus()
	bus.SetPublishPolicy(h.policy)

	auth := NewAuthInterceptor()
	t.Cleanup(auth.StopCleanup)
	auth.SetInsecureModuleIdentity(insecureIDs)
	auth.SetAuthorizer(h.authz)
	auth.SetIdentityProvider(h.idp)

	opts := []grpc.ServerOption{
		grpc.UnaryInterceptor(auth.UnaryInterceptor()),
		grpc.StreamInterceptor(auth.StreamInterceptor()),
	}
	if srvTLS != nil {
		opts = append(opts, grpc.Creds(credentials.NewTLS(srvTLS)))
	}
	srv := grpc.NewServer(opts...)
	NewEventServer(bus).RegisterWithGRPC(srv)
	var lc net.ListenConfig
	lis, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	if clientCreds == nil {
		clientCreds = insecure.NewCredentials()
	}
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(clientCreds))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	h.client = eventsv1.NewEventServiceClient(conn)
	return h
}

func publish(ctx context.Context, c eventsv1.EventServiceClient) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := c.Publish(ctx, &eventsv1.PublishRequest{Event: &eventsv1.Event{
		Id: "e1", Type: "media.movie.added", Source: "media-movies", Payload: []byte("{}"),
	}})
	return err
}

func mdCtx(kv ...string) context.Context {
	return metadata.NewOutgoingContext(context.Background(), metadata.Pairs(kv...))
}

// (a) dev/insecure: x-caller-id is the module principal; identity provider
// and user authorizer are not consulted.
func TestPrincipal_InsecureModuleCallerID(t *testing.T) {
	h := startPrincipalServer(t, true, nil, nil)
	if err := publish(mdCtx("x-caller-id", "media-movies"), h.client); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got := h.policy.last(); got != "media-movies" {
		t.Fatalf("publish policy caller = %q, want media-movies", got)
	}
	if calls, _ := h.idp.snapshot(); calls != 0 {
		t.Fatalf("identity provider called %d times for a module principal", calls)
	}
	if n := h.authz.count(); n != 0 {
		t.Fatalf("user authorizer called %d times for a module principal", n)
	}
}

// Without the insecure flag a plaintext x-caller-id is not a principal.
func TestPrincipal_PlaintextCallerIDRejectedWithoutInsecureFlag(t *testing.T) {
	h := startPrincipalServer(t, false, nil, nil)
	err := publish(mdCtx("x-caller-id", "media-movies"), h.client)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("Publish err = %v, want Unauthenticated", err)
	}
	if _, callers := h.idp.snapshot(); len(callers) != 1 || callers[0] != "" {
		t.Fatalf("identity provider caller hints = %q, want one call without x-caller-id", callers)
	}
}

// (b) mTLS: the verified certificate CN wins over a spoofed x-caller-id.
func TestPrincipal_MTLSCertCNOverridesSpoofedCallerID(t *testing.T) {
	ca := testCA(t)
	cert := moduleTLSCert(t, ca, "media-movies")
	h := startPrincipalServer(t, false,
		serverTLSConfig(t, ca, tls.VerifyClientCertIfGiven), clientTLSCreds(t, ca, &cert))

	if err := publish(mdCtx("x-caller-id", "auth-local"), h.client); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got := h.policy.last(); got != "media-movies" {
		t.Fatalf("publish policy caller = %q, want media-movies (cert CN)", got)
	}
	if calls, _ := h.idp.snapshot(); calls != 0 {
		t.Fatalf("identity provider called %d times for a cert-authenticated module", calls)
	}
	if n := h.authz.count(); n != 0 {
		t.Fatalf("user authorizer called %d times for a module principal", n)
	}
}

// (c) TLS without a client certificate: x-caller-id is ignored even when the
// insecure flag is set, and is not forwarded to the identity provider.
func TestPrincipal_TLSWithoutClientCertIgnoresCallerID(t *testing.T) {
	ca := testCA(t)
	h := startPrincipalServer(t, true,
		serverTLSConfig(t, ca, tls.VerifyClientCertIfGiven), clientTLSCreds(t, ca, nil))

	err := publish(mdCtx("x-caller-id", "media-movies"), h.client)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("Publish err = %v, want Unauthenticated", err)
	}
	if got := h.policy.last(); got != "" {
		t.Fatalf("event bus saw caller %q, want no publish", got)
	}
	if _, callers := h.idp.snapshot(); len(callers) != 1 || callers[0] != "" {
		t.Fatalf("identity provider caller hints = %q, want one call without x-caller-id", callers)
	}
}

// (d) a user bearer token still resolves through the identity provider and
// the user authorizer, in every transport mode, and wins over x-caller-id.
func TestPrincipal_UserBearerTokenUnchanged(t *testing.T) {
	ca := testCA(t)
	cert := moduleTLSCert(t, ca, "api-rest")
	cases := []struct {
		name  string
		h     func(t *testing.T) *principalHarness
		extra []string
	}{
		{"plaintext", func(t *testing.T) *principalHarness { return startPrincipalServer(t, false, nil, nil) }, nil},
		{"insecure with x-caller-id", func(t *testing.T) *principalHarness { return startPrincipalServer(t, true, nil, nil) },
			[]string{"x-caller-id", "api-rest"}},
		{"mTLS module forwarding user token", func(t *testing.T) *principalHarness {
			return startPrincipalServer(t, false, serverTLSConfig(t, ca, tls.VerifyClientCertIfGiven), clientTLSCreds(t, ca, &cert))
		}, []string{"x-caller-id", "api-rest"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.h(t)
			kv := append([]string{"authorization", "Bearer tok-alice"}, tc.extra...)
			if err := publish(mdCtx(kv...), h.client); err != nil {
				t.Fatalf("Publish: %v", err)
			}
			if got := h.policy.last(); got != "alice" {
				t.Fatalf("publish policy caller = %q, want alice", got)
			}
			if calls, _ := h.idp.snapshot(); calls != 1 {
				t.Fatalf("identity provider calls = %d, want 1", calls)
			}
			if n := h.authz.count(); n != 1 {
				t.Fatalf("user authorizer calls = %d, want 1", n)
			}
			// Invalid token: unauthenticated, no fallback to the module identity.
			err := publish(mdCtx("authorization", "Bearer nope", "x-caller-id", "api-rest"), h.client)
			if status.Code(err) != codes.Unauthenticated {
				t.Fatalf("invalid token err = %v, want Unauthenticated", err)
			}
		})
	}
}

// Module principals get core method rules: operator surfaces need a user.
func TestPrincipal_ModuleDeniedOperatorMethods(t *testing.T) {
	a := NewAuthInterceptor()
	defer a.StopCleanup()
	a.SetInsecureModuleIdentity(true)
	authz := &recordingAuthorizer{}
	a.SetAuthorizer(authz)
	a.SetIdentityProvider(&recordingIdP{})
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-caller-id", "media-movies"))

	for _, m := range []string{
		"/muxcore.lifecycle.v1.ModuleLifecycleService/StopModule",
		"/muxcore.spool.v1.SpoolService/DeployTag",
		"/muxcore.audit.v1.AuditService/Query",
		"/muxcore.discovery.v1.DiscoveryService/Leave",
	} {
		_, err := a.UnaryInterceptor()(ctx, nil, fakeUnaryInfo(m), fakeHandler("ok", nil))
		if status.Code(err) != codes.PermissionDenied {
			t.Errorf("%s: err = %v, want PermissionDenied", m, err)
		}
	}
	var gotCaller, gotKind string
	_, err := a.UnaryInterceptor()(ctx, nil, fakeUnaryInfo("/muxcore.storage.v1.StorageService/Stat"),
		func(ctx context.Context, _ any) (any, error) {
			gotCaller, gotKind = callerid.Get(ctx), callerid.Kind(ctx)
			return nil, nil
		})
	if err != nil {
		t.Fatalf("storage Stat: %v", err)
	}
	if gotCaller != "media-movies" || gotKind != callerid.KindModule {
		t.Fatalf("principal = %q/%q, want media-movies/module", gotCaller, gotKind)
	}
	if authz.count() != 0 {
		t.Fatal("user authorizer consulted for a module principal")
	}
	// Repeated method denials are not authentication failures: no backoff.
	for range gRPCAuthBackoffThreshold + 2 {
		_, _ = a.UnaryInterceptor()(ctx, nil, fakeUnaryInfo("/muxcore.spool.v1.SpoolService/DeployTag"), fakeHandler("ok", nil))
	}
	if _, err := a.UnaryInterceptor()(ctx, nil, fakeUnaryInfo("/muxcore.events.v1.EventService/Publish"), fakeHandler("ok", nil)); err != nil {
		t.Fatalf("Publish after operator denials: %v", err)
	}
}

type denyCallPolicy struct{ calls int }

func (p *denyCallPolicy) AllowCall(_ context.Context, _, _, _ string) (bool, error) {
	p.calls++
	return false, nil
}

type principalEchoHandler struct{}

func (principalEchoHandler) HandleCall(_ context.Context, _ string, payload []byte) ([]byte, error) {
	return payload, nil
}

// Inbound ModuleMesh calls from module principals go through the call policy.
func TestServerCall_ModulePrincipalUsesCallPolicy(t *testing.T) {
	srv := NewServer()
	srv.RegisterHandler("target", principalEchoHandler{})
	req := &meshv1.CallRequest{TargetModule: "target", Method: "m", Payload: []byte("x")}
	modCtx := callerid.SetPrincipal(context.Background(), "media-movies", callerid.KindModule)

	if _, err := srv.Call(modCtx, req); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("no call policy: err = %v, want PermissionDenied", err)
	}
	c := NewClient(srv)
	pol := &denyCallPolicy{}
	c.SetCallPolicy(pol)
	if _, err := srv.Call(modCtx, req); status.Code(err) != codes.PermissionDenied || pol.calls != 1 {
		t.Fatalf("deny policy: err = %v calls=%d", err, pol.calls)
	}
	c.SetCallPolicy(allowAllCallPolicyForTest{})
	resp, err := srv.Call(modCtx, req)
	if err != nil || string(resp.GetPayload()) != "x" {
		t.Fatalf("allow policy: resp=%v err=%v", resp, err)
	}
	// User principals are authorized by the interceptor (Can) — unchanged.
	userCtx := callerid.SetPrincipal(context.Background(), "alice", callerid.KindUser)
	c.SetCallPolicy(pol)
	if _, err := srv.Call(userCtx, req); err != nil {
		t.Fatalf("user principal: %v", err)
	}
}

type allowAllCallPolicyForTest struct{}

func (allowAllCallPolicyForTest) AllowCall(_ context.Context, _, _, _ string) (bool, error) {
	return true, nil
}
