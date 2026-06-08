package grpcmesh

import (
	"context"
	"errors"
	"testing"

	"github.com/Muxcore-Media/core/pkg/contracts"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
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

func TestAuthInterceptor_NoEnforcement_AllowsAll(t *testing.T) {
	a := NewAuthInterceptor()
	interceptor := a.UnaryInterceptor()

	_, err := interceptor(
		context.Background(), nil,
		fakeUnaryInfo("/health/Check"),
		fakeHandler("ok", nil),
	)
	if err != nil {
		t.Errorf("no enforcement: expected nil error, got %v", err)
	}
}

func TestAuthInterceptor_CallerIDPropagated(t *testing.T) {
	a := NewAuthInterceptor()
	interceptor := a.UnaryInterceptor()

	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("x-caller-id", "module-xyz"))

	var capturedCallerID string
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		capturedCallerID = contracts.CallerIDFromContext(ctx)
		return nil, nil
	}
	_, err := interceptor(ctx, nil, fakeUnaryInfo("/mesh/Call"), handler)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedCallerID != "module-xyz" {
		t.Errorf("expected callerID 'module-xyz', got %q", capturedCallerID)
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

func TestAuthInterceptor_SetAuthorizer_Nil_DisablesEnforcement(t *testing.T) {
	a := NewAuthInterceptor()
	a.SetAuthorizer(&stubAuthorizer{allow: true})
	a.SetIdentityProvider(&stubIdentityProvider{identity: &contracts.Identity{ID: "u"}})
	// Disable by setting nil authorizer.
	a.SetAuthorizer(nil)

	interceptor := a.UnaryInterceptor()
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs())

	_, err := interceptor(ctx, nil, fakeUnaryInfo("/mesh/Call"), fakeHandler(nil, nil))
	if err != nil {
		t.Errorf("nil authorizer should disable enforcement, got %v", err)
	}
}
