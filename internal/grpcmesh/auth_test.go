package grpcmesh

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/Muxcore-Media/core/pkg/contracts"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// --- Stubs ---

type stubAuthorizer struct{ allow bool }

func (a *stubAuthorizer) Can(_ context.Context, _ contracts.Session, _, _ string) (bool, error) {
	return a.allow, nil
}

type stubIdentityProvider struct {
	identity *contracts.Identity
	err      error
}

func (p *stubIdentityProvider) ExtractIdentity(ctx context.Context) (*contracts.Identity, error) {
	return p.identity, p.err
}

func fakeUnaryInfo(method string) *grpc.UnaryServerInfo {
	return &grpc.UnaryServerInfo{FullMethod: method}
}

func fakeHandler(resp interface{}, err error) grpc.UnaryHandler {
	return func(ctx context.Context, req interface{}) (interface{}, error) {
		return resp, err
	}
}

// --- Tests ---

func TestAuthInterceptor_ModuleRegistration_AlwaysOpen(t *testing.T) {
	a := NewAuthInterceptor()
	interceptor := a.UnaryInterceptor()

	_, err := interceptor(
		context.Background(), nil,
		fakeUnaryInfo("/muxcore.module.v1.ModuleRegistration/Register"),
		fakeHandler("ok", nil),
	)
	if err != nil {
		t.Errorf("module registration should be open: %v", err)
	}
}

func TestAuthInterceptor_DeniesAnonymousByDefault(t *testing.T) {
	a := NewAuthInterceptor()
	interceptor := a.UnaryInterceptor()

	_, err := interceptor(
		context.Background(), nil,
		fakeUnaryInfo("/health/Check"),
		fakeHandler("ok", nil),
	)
	if err == nil {
		t.Fatal("expected denial for anonymous call")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %s", st.Code())
	}
}

func TestAuthInterceptor_DeniesModuleWithoutAuthorizer(t *testing.T) {
	a := NewAuthInterceptor()
	a.SetInsecureModuleIdentity(false) // independent of MUXCORE_INSECURE_DISABLE_TLS in the test env
	interceptor := a.UnaryInterceptor()

	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("x-caller-id", "module-xyz"))

	_, err := interceptor(ctx, nil, fakeUnaryInfo("/mesh/Call"), fakeHandler(nil, nil))
	if err == nil {
		t.Fatal("expected denial for module call without authorizer configured")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %s", st.Code())
	}
}

func TestAuthInterceptor_Enforcement_Allowed(t *testing.T) {
	a := NewAuthInterceptor()
	a.SetAuthorizer(&stubAuthorizer{allow: true})
	a.SetIdentityProvider(&stubIdentityProvider{
		identity: &contracts.Identity{ID: "user-1", Roles: []string{"admin"}},
	})

	interceptor := a.UnaryInterceptor()
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs())

	_, err := interceptor(ctx, nil, fakeUnaryInfo("/mesh/Call"), fakeHandler(nil, nil))
	if err != nil {
		t.Errorf("expected allowed, got error: %v", err)
	}
}

func TestAuthInterceptor_Enforcement_Denied(t *testing.T) {
	a := NewAuthInterceptor()
	a.SetAuthorizer(&stubAuthorizer{allow: false})
	a.SetIdentityProvider(&stubIdentityProvider{
		identity: &contracts.Identity{ID: "user-2"},
	})

	interceptor := a.UnaryInterceptor()
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs())

	_, err := interceptor(ctx, nil, fakeUnaryInfo("/mesh/Call"), fakeHandler(nil, nil))
	if err == nil {
		t.Fatal("expected error when access denied")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %s", st.Code())
	}
}

func TestAuthInterceptor_Enforcement_IdentityExtractionFails(t *testing.T) {
	a := NewAuthInterceptor()
	a.SetAuthorizer(&stubAuthorizer{allow: true})
	a.SetIdentityProvider(&stubIdentityProvider{
		err: errors.New("token expired"),
	})

	interceptor := a.UnaryInterceptor()
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs())

	_, err := interceptor(ctx, nil, fakeUnaryInfo("/mesh/Call"), fakeHandler(nil, nil))
	if err == nil {
		t.Fatal("expected error when identity extraction fails")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.Unauthenticated {
		t.Errorf("expected Unauthenticated, got %s", st.Code())
	}
}

func TestAuthInterceptor_Enforcement_NilIdentity_Unauthenticated(t *testing.T) {
	a := NewAuthInterceptor()
	a.SetAuthorizer(&stubAuthorizer{allow: true})
	a.SetIdentityProvider(&stubIdentityProvider{identity: nil})

	interceptor := a.UnaryInterceptor()
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs())

	_, err := interceptor(ctx, nil, fakeUnaryInfo("/mesh/Call"), fakeHandler(nil, nil))
	if err == nil {
		t.Fatal("expected Unauthenticated for nil identity")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.Unauthenticated {
		t.Errorf("expected Unauthenticated, got %s", st.Code())
	}
}

func TestAuthInterceptor_RateLimit_AfterThreshold(t *testing.T) {
	a := NewAuthInterceptor()
	defer a.StopCleanup()

	// Always fail auth so every call increments the failure counter.
	a.SetAuthorizer(&stubAuthorizer{allow: false})
	a.SetIdentityProvider(&stubIdentityProvider{
		identity: &contracts.Identity{ID: "user-1", Roles: []string{"admin"}},
	})
	interceptor := a.UnaryInterceptor()
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs())

	// The backoff check happens at the START of extractAndVerify, before
	// the failure is recorded. So after N failures, call N+1 returns the
	// rate limit error (the Nth failure set blockUntil; call N+1 checks it).
	for i := 0; i < gRPCAuthBackoffThreshold; i++ {
		_, err := interceptor(ctx, nil, fakeUnaryInfo("/mesh/Call"), fakeHandler(nil, nil))
		if err == nil {
			t.Fatalf("attempt %d: expected error", i)
		}
		if st, _ := status.FromError(err); st.Code() != codes.PermissionDenied {
			t.Fatalf("attempt %d: expected PermissionDenied, got %s", i, st.Code())
		}
	}

	// Call N+1 should trigger ResourceExhausted (rate limited).
	_, err := interceptor(ctx, nil, fakeUnaryInfo("/mesh/Call"), fakeHandler(nil, nil))
	if err == nil {
		t.Fatal("expected rate limit error after threshold")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.ResourceExhausted {
		t.Errorf("expected ResourceExhausted, got %s", st.Code())
	}
	if st.Message() != "too many authentication failures" {
		t.Errorf("unexpected message: %s", st.Message())
	}
}

func TestAuthInterceptor_RateLimit_PerIdentity(t *testing.T) {
	a := NewAuthInterceptor()
	defer a.StopCleanup()
	a.SetInsecureModuleIdentity(false)

	a.SetAuthorizer(&stubAuthorizer{allow: false})
	a.SetIdentityProvider(&stubIdentityProvider{
		identity: &contracts.Identity{ID: "user-1", Roles: []string{"admin"}},
	})
	interceptor := a.UnaryInterceptor()
	peerCtx := func(addr string, kv ...string) context.Context {
		ctx := peer.NewContext(context.Background(), &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP(addr), Port: 4000}})
		return metadata.NewIncomingContext(ctx, metadata.Pairs(kv...))
	}
	ctx := peerCtx("10.0.0.1")

	// The backoff check happens at the START of extractAndVerify, before
	// the failure is recorded. So we need N+1 calls to trigger rate limiting:
	// N calls record failures, call N+1 sees the backoff and returns ResourceExhausted.
	for i := 0; i < gRPCAuthBackoffThreshold; i++ {
		interceptor(ctx, nil, fakeUnaryInfo("/mesh/Call"), fakeHandler(nil, nil))
	}

	// The peer should now be rate limited (call N+1).
	_, err := interceptor(ctx, nil, fakeUnaryInfo("/mesh/Call"), fakeHandler(nil, nil))
	if err == nil {
		t.Fatal("expected rate limit for exhausted identity")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.ResourceExhausted {
		t.Errorf("expected ResourceExhausted, got %s", st.Code())
	}

	// Rotating the unverified x-caller-id does not escape the backoff
	// (ADR-0017: client-supplied caller IDs are not identities).
	_, err = interceptor(peerCtx("10.0.0.1", "x-caller-id", "other-identity"), nil, fakeUnaryInfo("/mesh/Call"), fakeHandler(nil, nil))
	if st, _ := status.FromError(err); st.Code() != codes.ResourceExhausted {
		t.Errorf("rotated x-caller-id: expected ResourceExhausted, got %v", err)
	}

	// A different peer is not rate limited.
	_, err = interceptor(peerCtx("10.0.0.2"), nil, fakeUnaryInfo("/mesh/Call"), fakeHandler(nil, nil))
	if err == nil {
		t.Fatal("expected PermissionDenied for different peer (still denied, but not rate limited)")
	}
	st, _ = status.FromError(err)
	if st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied for different peer, got %s", st.Code())
	}
}

func TestAuthInterceptor_SetAuthorizer_Nil_StillDenies(t *testing.T) {
	a := NewAuthInterceptor()
	a.SetAuthorizer(&stubAuthorizer{allow: true})
	a.SetIdentityProvider(&stubIdentityProvider{identity: &contracts.Identity{ID: "u"}})
	// Disable by setting nil authorizer — interceptor falls back to deny-by-default.
	a.SetAuthorizer(nil)

	interceptor := a.UnaryInterceptor()
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs())

	_, err := interceptor(ctx, nil, fakeUnaryInfo("/mesh/Call"), fakeHandler(nil, nil))
	if err == nil {
		t.Fatal("expected denial when authorizer is nil (deny-by-default)")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %s", st.Code())
	}
}
