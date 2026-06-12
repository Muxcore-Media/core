package grpcmesh

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/Muxcore-Media/core/pkg/contracts"
	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	policyv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/policy/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type fakeAuthServer struct {
	authv1.UnimplementedAuthServiceServer
	authenticateFn func(context.Context, *authv1.AuthenticateRequest) (*authv1.AuthenticateResponse, error)
	validateFn     func(context.Context, *authv1.ValidateRequest) (*authv1.ValidateResponse, error)
	revokeFn       func(context.Context, *authv1.RevokeRequest) (*authv1.RevokeResponse, error)
	canFn          func(context.Context, *authv1.CanRequest) (*authv1.CanResponse, error)
	extractIdFn    func(context.Context, *authv1.ExtractIdentityRequest) (*authv1.ExtractIdentityResponse, error)
}

func (f *fakeAuthServer) Authenticate(ctx context.Context, req *authv1.AuthenticateRequest) (*authv1.AuthenticateResponse, error) {
	if f.authenticateFn != nil {
		return f.authenticateFn(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (f *fakeAuthServer) Validate(ctx context.Context, req *authv1.ValidateRequest) (*authv1.ValidateResponse, error) {
	if f.validateFn != nil {
		return f.validateFn(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (f *fakeAuthServer) Revoke(ctx context.Context, req *authv1.RevokeRequest) (*authv1.RevokeResponse, error) {
	if f.revokeFn != nil {
		return f.revokeFn(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (f *fakeAuthServer) Can(ctx context.Context, req *authv1.CanRequest) (*authv1.CanResponse, error) {
	if f.canFn != nil {
		return f.canFn(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (f *fakeAuthServer) ExtractIdentity(ctx context.Context, req *authv1.ExtractIdentityRequest) (*authv1.ExtractIdentityResponse, error) {
	if f.extractIdFn != nil {
		return f.extractIdFn(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

type fakePolicyServer struct {
	policyv1.UnimplementedPolicyServiceServer
	allowCallFn    func(context.Context, *policyv1.AllowCallRequest) (*policyv1.AllowCallResponse, error)
	allowPublishFn func(context.Context, *policyv1.AllowPublishRequest) (*policyv1.AllowPublishResponse, error)
}

func (f *fakePolicyServer) AllowCall(ctx context.Context, req *policyv1.AllowCallRequest) (*policyv1.AllowCallResponse, error) {
	if f.allowCallFn != nil {
		return f.allowCallFn(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (f *fakePolicyServer) AllowPublish(ctx context.Context, req *policyv1.AllowPublishRequest) (*policyv1.AllowPublishResponse, error) {
	if f.allowPublishFn != nil {
		return f.allowPublishFn(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func startTestAuthServer(t *testing.T, srv authv1.AuthServiceServer) *grpc.ClientConn {
	t.Helper()
	var lc net.ListenConfig
	lis, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	grpcSrv := grpc.NewServer()
	authv1.RegisterAuthServiceServer(grpcSrv, srv)
	go grpcSrv.Serve(lis)
	t.Cleanup(grpcSrv.GracefulStop)

	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func startTestPolicyServer(t *testing.T, srv policyv1.PolicyServiceServer) *grpc.ClientConn {
	t.Helper()
	var lc net.ListenConfig
	lis, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	grpcSrv := grpc.NewServer()
	policyv1.RegisterPolicyServiceServer(grpcSrv, srv)
	go grpcSrv.Serve(lis)
	t.Cleanup(grpcSrv.GracefulStop)

	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestSidecarAuthProvider_Authenticate_Success(t *testing.T) {
	conn := startTestAuthServer(t, &fakeAuthServer{
		authenticateFn: func(_ context.Context, req *authv1.AuthenticateRequest) (*authv1.AuthenticateResponse, error) {
			return &authv1.AuthenticateResponse{
				Authenticated: true,
				UserId:        "u1",
				Username:      "alice",
				Roles:         []string{"admin"},
				Permissions:   []string{"read", "write"},
				SessionToken:  "tok-123",
			}, nil
		},
	})
	p := NewSidecarAuthProvider(conn)
	sess, err := p.Authenticate(context.Background(), contracts.Credentials{Type: "password", Data: []byte("secret")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sess.UserID != "u1" {
		t.Errorf("UserID = %q, want %q", sess.UserID, "u1")
	}
	if sess.Username != "alice" {
		t.Errorf("Username = %q, want %q", sess.Username, "alice")
	}
	if sess.Token != "tok-123" {
		t.Errorf("Token = %q, want %q", sess.Token, "tok-123")
	}
	if len(sess.Roles) != 1 || sess.Roles[0] != "admin" {
		t.Errorf("Roles = %v, want [admin]", sess.Roles)
	}
}

func TestSidecarAuthProvider_Authenticate_Failed(t *testing.T) {
	conn := startTestAuthServer(t, &fakeAuthServer{
		authenticateFn: func(_ context.Context, _ *authv1.AuthenticateRequest) (*authv1.AuthenticateResponse, error) {
			return &authv1.AuthenticateResponse{
				Authenticated: false,
				Error:         "bad credentials",
			}, nil
		},
	})
	p := NewSidecarAuthProvider(conn)
	_, err := p.Authenticate(context.Background(), contracts.Credentials{Type: "password", Data: []byte("wrong")})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "authentication failed") {
		t.Errorf("error = %q, want containing %q", err.Error(), "authentication failed")
	}
}

func TestSidecarAuthProvider_Authenticate_GRPCError(t *testing.T) {
	conn := startTestAuthServer(t, &fakeAuthServer{
		authenticateFn: func(_ context.Context, _ *authv1.AuthenticateRequest) (*authv1.AuthenticateResponse, error) {
			return nil, status.Error(codes.Internal, "boom")
		},
	})
	p := NewSidecarAuthProvider(conn)
	_, err := p.Authenticate(context.Background(), contracts.Credentials{Type: "password"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "sidecar auth") {
		t.Errorf("error = %q, want containing %q", err.Error(), "sidecar auth")
	}
}

func TestSidecarAuthProvider_Validate_Valid(t *testing.T) {
	conn := startTestAuthServer(t, &fakeAuthServer{
		validateFn: func(_ context.Context, req *authv1.ValidateRequest) (*authv1.ValidateResponse, error) {
			return &authv1.ValidateResponse{
				Valid:       true,
				UserId:      "u2",
				Username:    "bob",
				Roles:       []string{"viewer"},
				Permissions: []string{"read"},
			}, nil
		},
	})
	p := NewSidecarAuthProvider(conn)
	sess, err := p.Validate(context.Background(), "valid-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sess.UserID != "u2" {
		t.Errorf("UserID = %q, want %q", sess.UserID, "u2")
	}
	if sess.Username != "bob" {
		t.Errorf("Username = %q, want %q", sess.Username, "bob")
	}
}

func TestSidecarAuthProvider_Validate_Invalid(t *testing.T) {
	conn := startTestAuthServer(t, &fakeAuthServer{
		validateFn: func(_ context.Context, _ *authv1.ValidateRequest) (*authv1.ValidateResponse, error) {
			return &authv1.ValidateResponse{
				Valid: false,
				Error: "token expired",
			}, nil
		},
	})
	p := NewSidecarAuthProvider(conn)
	_, err := p.Validate(context.Background(), "expired-token")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid token") {
		t.Errorf("error = %q, want containing %q", err.Error(), "invalid token")
	}
}

func TestSidecarAuthProvider_Validate_GRPCError(t *testing.T) {
	conn := startTestAuthServer(t, &fakeAuthServer{
		validateFn: func(_ context.Context, _ *authv1.ValidateRequest) (*authv1.ValidateResponse, error) {
			return nil, status.Error(codes.Internal, "validate error")
		},
	})
	p := NewSidecarAuthProvider(conn)
	_, err := p.Validate(context.Background(), "tok")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "sidecar auth validate") {
		t.Errorf("error = %q, want containing %q", err.Error(), "sidecar auth validate")
	}
}

func TestSidecarAuthProvider_Revoke_Success(t *testing.T) {
	called := false
	conn := startTestAuthServer(t, &fakeAuthServer{
		revokeFn: func(_ context.Context, req *authv1.RevokeRequest) (*authv1.RevokeResponse, error) {
			called = true
			if req.Token != "revoke-me" {
				t.Errorf("Token = %q, want %q", req.Token, "revoke-me")
			}
			return &authv1.RevokeResponse{}, nil
		},
	})
	p := NewSidecarAuthProvider(conn)
	err := p.Revoke(context.Background(), "revoke-me")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("Revoke was not called")
	}
}

func TestSidecarAuthProvider_Revoke_GRPCError(t *testing.T) {
	conn := startTestAuthServer(t, &fakeAuthServer{
		revokeFn: func(_ context.Context, _ *authv1.RevokeRequest) (*authv1.RevokeResponse, error) {
			return nil, status.Error(codes.Unavailable, "service down")
		},
	})
	p := NewSidecarAuthProvider(conn)
	err := p.Revoke(context.Background(), "tok")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestSidecarAuthorizer_Can_Allowed(t *testing.T) {
	conn := startTestAuthServer(t, &fakeAuthServer{
		canFn: func(_ context.Context, req *authv1.CanRequest) (*authv1.CanResponse, error) {
			return &authv1.CanResponse{Allowed: true}, nil
		},
	})
	a := NewSidecarAuthorizer(conn)
	ok, err := a.Can(context.Background(), contracts.Session{UserID: "u1"}, "read", "docs")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected allowed=true")
	}
}

func TestSidecarAuthorizer_Can_Denied(t *testing.T) {
	conn := startTestAuthServer(t, &fakeAuthServer{
		canFn: func(_ context.Context, _ *authv1.CanRequest) (*authv1.CanResponse, error) {
			return &authv1.CanResponse{Allowed: false, Reason: "forbidden"}, nil
		},
	})
	a := NewSidecarAuthorizer(conn)
	ok, err := a.Can(context.Background(), contracts.Session{UserID: "u1"}, "delete", "users")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected allowed=false")
	}
}

func TestSidecarAuthorizer_Can_GRPCError(t *testing.T) {
	conn := startTestAuthServer(t, &fakeAuthServer{
		canFn: func(_ context.Context, _ *authv1.CanRequest) (*authv1.CanResponse, error) {
			return nil, status.Error(codes.Internal, "authz error")
		},
	})
	a := NewSidecarAuthorizer(conn)
	_, err := a.Can(context.Background(), contracts.Session{UserID: "u1"}, "read", "docs")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "sidecar auth can") {
		t.Errorf("error = %q, want containing %q", err.Error(), "sidecar auth can")
	}
}

func TestSidecarIdentityProvider_ExtractIdentity_Found(t *testing.T) {
	conn := startTestAuthServer(t, &fakeAuthServer{
		extractIdFn: func(_ context.Context, _ *authv1.ExtractIdentityRequest) (*authv1.ExtractIdentityResponse, error) {
			return &authv1.ExtractIdentityResponse{
				Found: true,
				Id:    "id-1",
				Kind:  "user",
				Roles: []string{"admin"},
			}, nil
		},
	})
	ip := NewSidecarIdentityProvider(conn)
	ident, err := ip.ExtractIdentity(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ident == nil {
		t.Fatal("expected identity, got nil")
	}
	if ident.ID != "id-1" {
		t.Errorf("ID = %q, want %q", ident.ID, "id-1")
	}
	if ident.Kind != "user" {
		t.Errorf("Kind = %q, want %q", ident.Kind, "user")
	}
}

func TestSidecarIdentityProvider_ExtractIdentity_NotFound(t *testing.T) {
	conn := startTestAuthServer(t, &fakeAuthServer{
		extractIdFn: func(_ context.Context, _ *authv1.ExtractIdentityRequest) (*authv1.ExtractIdentityResponse, error) {
			return &authv1.ExtractIdentityResponse{Found: false}, nil
		},
	})
	ip := NewSidecarIdentityProvider(conn)
	ident, err := ip.ExtractIdentity(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ident != nil {
		t.Errorf("expected nil identity, got %+v", ident)
	}
}

func TestSidecarIdentityProvider_ExtractIdentity_GRPCError(t *testing.T) {
	conn := startTestAuthServer(t, &fakeAuthServer{
		extractIdFn: func(_ context.Context, _ *authv1.ExtractIdentityRequest) (*authv1.ExtractIdentityResponse, error) {
			return nil, status.Error(codes.Internal, "identity error")
		},
	})
	ip := NewSidecarIdentityProvider(conn)
	_, err := ip.ExtractIdentity(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "sidecar auth identity") {
		t.Errorf("error = %q, want containing %q", err.Error(), "sidecar auth identity")
	}
}

func TestSidecarCallPolicy_AllowCall_Allowed(t *testing.T) {
	conn := startTestPolicyServer(t, &fakePolicyServer{
		allowCallFn: func(_ context.Context, req *policyv1.AllowCallRequest) (*policyv1.AllowCallResponse, error) {
			return &policyv1.AllowCallResponse{Allowed: true}, nil
		},
	})
	cp := NewSidecarCallPolicy(conn)
	ok, err := cp.AllowCall(context.Background(), "mod-a", "mod-b", "Read")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected allowed=true")
	}
}

func TestSidecarCallPolicy_AllowCall_Denied(t *testing.T) {
	conn := startTestPolicyServer(t, &fakePolicyServer{
		allowCallFn: func(_ context.Context, _ *policyv1.AllowCallRequest) (*policyv1.AllowCallResponse, error) {
			return &policyv1.AllowCallResponse{Allowed: false, Reason: "not in allowlist"}, nil
		},
	})
	cp := NewSidecarCallPolicy(conn)
	ok, err := cp.AllowCall(context.Background(), "mod-a", "mod-b", "Write")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected allowed=false")
	}
}

func TestSidecarCallPolicy_AllowCall_GRPCError(t *testing.T) {
	conn := startTestPolicyServer(t, &fakePolicyServer{
		allowCallFn: func(_ context.Context, _ *policyv1.AllowCallRequest) (*policyv1.AllowCallResponse, error) {
			return nil, status.Error(codes.Internal, "policy error")
		},
	})
	cp := NewSidecarCallPolicy(conn)
	_, err := cp.AllowCall(context.Background(), "mod-a", "mod-b", "Read")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "sidecar call policy") {
		t.Errorf("error = %q, want containing %q", err.Error(), "sidecar call policy")
	}
}

func TestSidecarPublishPolicy_CanPublish_Allowed(t *testing.T) {
	conn := startTestPolicyServer(t, &fakePolicyServer{
		allowPublishFn: func(_ context.Context, req *policyv1.AllowPublishRequest) (*policyv1.AllowPublishResponse, error) {
			return &policyv1.AllowPublishResponse{Allowed: true}, nil
		},
	})
	pp := NewSidecarPublishPolicy(conn)
	ok, err := pp.CanPublish(context.Background(), "mod-a", "event.created")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected allowed=true")
	}
}

func TestSidecarPublishPolicy_CanPublish_GRPCError(t *testing.T) {
	conn := startTestPolicyServer(t, &fakePolicyServer{
		allowPublishFn: func(_ context.Context, _ *policyv1.AllowPublishRequest) (*policyv1.AllowPublishResponse, error) {
			return nil, status.Error(codes.Internal, "publish policy error")
		},
	})
	pp := NewSidecarPublishPolicy(conn)
	_, err := pp.CanPublish(context.Background(), "mod-a", "event.created")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "sidecar publish policy") {
		t.Errorf("error = %q, want containing %q", err.Error(), "sidecar publish policy")
	}
}
