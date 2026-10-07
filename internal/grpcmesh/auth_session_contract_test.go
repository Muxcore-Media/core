package grpcmesh

import (
	"context"
	"net"
	"testing"
	"time"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// auth-local and auth-oidc use this embedding to remain source-compatible when
// the service gains methods they have not implemented yet.
type legacySessionAuthProvider struct {
	authv1.UnimplementedAuthServiceServer
}

var _ authv1.AuthServiceServer = (*legacySessionAuthProvider)(nil)

func (legacySessionAuthProvider) Validate(_ context.Context, req *authv1.ValidateRequest) (*authv1.ValidateResponse, error) {
	return &authv1.ValidateResponse{
		Valid:    req.Token == "existing-session",
		UserId:   "existing-user",
		Username: "alice",
	}, nil
}

func (legacySessionAuthProvider) Revoke(_ context.Context, req *authv1.RevokeRequest) (*authv1.RevokeResponse, error) {
	if req.Token != "existing-session" {
		return nil, status.Error(codes.NotFound, "session not found")
	}
	return &authv1.RevokeResponse{}, nil
}

func TestAuthSessionContractBackwardCompatibility(t *testing.T) {
	for _, oldDescriptor := range []bool{false, true} {
		name := "rebuilt_provider_with_unimplemented_methods"
		if oldDescriptor {
			name = "provider_running_previous_service_descriptor"
		}
		t.Run(name, func(t *testing.T) {
			listener := bufconn.Listen(1 << 20)
			server := grpc.NewServer()
			provider := &legacySessionAuthProvider{}
			if oldDescriptor {
				desc := authv1.AuthService_ServiceDesc
				desc.Methods = nil
				for _, method := range authv1.AuthService_ServiceDesc.Methods {
					if method.MethodName != "ListSessions" && method.MethodName != "RevokeSession" {
						desc.Methods = append(desc.Methods, method)
					}
				}
				server.RegisterService(&desc, provider)
			} else {
				authv1.RegisterAuthServiceServer(server, provider)
			}
			served := make(chan error, 1)
			go func() { served <- server.Serve(listener) }()
			t.Cleanup(func() {
				server.Stop()
				if err := <-served; err != nil {
					t.Errorf("serve auth provider: %v", err)
				}
			})
			conn, err := grpc.NewClient("passthrough:///auth-provider",
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
					return listener.DialContext(ctx)
				}),
			)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Errorf("close auth client: %v", err)
				}
			})
			client := authv1.NewAuthServiceClient(conn)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			validated, err := client.Validate(ctx, &authv1.ValidateRequest{Token: "existing-session"})
			if err != nil || !validated.GetValid() || validated.GetUserId() != "existing-user" {
				t.Fatalf("existing Validate contract: response=%v error=%v", validated, err)
			}
			if _, err := client.Revoke(ctx, &authv1.RevokeRequest{Token: "existing-session"}); err != nil {
				t.Fatalf("existing Revoke contract: %v", err)
			}
			if _, err := client.ListSessions(ctx, &authv1.ListSessionsRequest{UserId: "existing-user", PageSize: 100}); status.Code(err) != codes.Unimplemented {
				t.Fatalf("unsupported ListSessions: got %v, want Unimplemented", err)
			}
			if _, err := client.RevokeSession(ctx, &authv1.RevokeSessionRequest{UserId: "existing-user", SessionId: "opaque-id"}); status.Code(err) != codes.Unimplemented {
				t.Fatalf("unsupported RevokeSession: got %v, want Unimplemented", err)
			}
		})
	}
}
