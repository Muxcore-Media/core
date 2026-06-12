package grpcmesh

import (
	"context"
	"fmt"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"

	"github.com/Muxcore-Media/core/pkg/contracts"
	"google.golang.org/grpc"
)

// SidecarAuthProvider wraps a gRPC connection to a sidecar module's AuthService
// and implements contracts.AuthProvider by delegating over gRPC.
type SidecarAuthProvider struct {
	client authv1.AuthServiceClient
}

func NewSidecarAuthProvider(conn *grpc.ClientConn) *SidecarAuthProvider {
	return &SidecarAuthProvider{client: authv1.NewAuthServiceClient(conn)}
}

func (s *SidecarAuthProvider) Authenticate(ctx context.Context, creds contracts.Credentials) (contracts.Session, error) {
	resp, err := s.client.Authenticate(ctx, &authv1.AuthenticateRequest{
		CredentialType: creds.Type,
		CredentialData: creds.Data,
	})
	if err != nil {
		return contracts.Session{}, fmt.Errorf("sidecar auth: %w", err)
	}
	if !resp.Authenticated {
		return contracts.Session{}, fmt.Errorf("authentication failed: %s", resp.Error)
	}
	return contracts.Session{
		UserID:      resp.UserId,
		Username:    resp.Username,
		Roles:       resp.Roles,
		Permissions: resp.Permissions,
		Token:       resp.SessionToken,
	}, nil
}

func (s *SidecarAuthProvider) Validate(ctx context.Context, token string) (contracts.Session, error) {
	resp, err := s.client.Validate(ctx, &authv1.ValidateRequest{Token: token})
	if err != nil {
		return contracts.Session{}, fmt.Errorf("sidecar auth validate: %w", err)
	}
	if !resp.Valid {
		return contracts.Session{}, fmt.Errorf("invalid token: %s", resp.Error)
	}
	return contracts.Session{
		UserID:      resp.UserId,
		Username:    resp.Username,
		Roles:       resp.Roles,
		Permissions: resp.Permissions,
	}, nil
}

func (s *SidecarAuthProvider) Revoke(ctx context.Context, token string) error {
	_, err := s.client.Revoke(ctx, &authv1.RevokeRequest{Token: token})
	return err
}

// SidecarAuthorizer wraps a gRPC connection to a sidecar module's AuthService
// and implements contracts.Authorizer by delegating over gRPC.
type SidecarAuthorizer struct {
	client authv1.AuthServiceClient
}

func NewSidecarAuthorizer(conn *grpc.ClientConn) *SidecarAuthorizer {
	return &SidecarAuthorizer{client: authv1.NewAuthServiceClient(conn)}
}

func (s *SidecarAuthorizer) Can(ctx context.Context, session contracts.Session, action, resource string) (bool, error) {
	resp, err := s.client.Can(ctx, &authv1.CanRequest{
		UserId:   session.UserID,
		Action:   action,
		Resource: resource,
	})
	if err != nil {
		return false, fmt.Errorf("sidecar auth can: %w", err)
	}
	return resp.Allowed, nil
}

// SidecarIdentityProvider wraps a gRPC connection to a sidecar module's AuthService
// and implements contracts.IdentityProvider by delegating over gRPC.
type SidecarIdentityProvider struct {
	client authv1.AuthServiceClient
}

func NewSidecarIdentityProvider(conn *grpc.ClientConn) *SidecarIdentityProvider {
	return &SidecarIdentityProvider{client: authv1.NewAuthServiceClient(conn)}
}

func (s *SidecarIdentityProvider) ExtractIdentity(ctx context.Context) (*contracts.Identity, error) {
	resp, err := s.client.ExtractIdentity(ctx, &authv1.ExtractIdentityRequest{})
	if err != nil {
		return nil, fmt.Errorf("sidecar auth identity: %w", err)
	}
	if !resp.Found {
		return nil, nil
	}
	return &contracts.Identity{
		ID:    resp.Id,
		Kind:  resp.Kind,
		Roles: resp.Roles,
	}, nil
}
