package grpcmesh

import (
	"context"
	"fmt"

	secretsv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/secrets/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SidecarSecrets wraps a gRPC connection to a sidecar module's SecretsService
// and implements contracts.SecretsProvider by forwarding queries over gRPC.
type SidecarSecrets struct {
	client secretsv1.SecretsServiceClient
}

// NewSidecarSecrets creates a SecretsProvider backed by a sidecar module's
// gRPC SecretsService.
func NewSidecarSecrets(conn *grpc.ClientConn) *SidecarSecrets {
	return &SidecarSecrets{
		client: secretsv1.NewSecretsServiceClient(conn),
	}
}

func (s *SidecarSecrets) Get(ctx context.Context, key string) (string, error) {
	resp, err := s.client.Get(ctx, &secretsv1.GetRequest{Key: key})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return "", fmt.Errorf("secret %q not found", key)
		}
		return "", fmt.Errorf("sidecar secrets: %w", err)
	}
	return resp.GetValue(), nil
}

func (s *SidecarSecrets) Set(ctx context.Context, key, value string) error {
	_, err := s.client.Set(ctx, &secretsv1.SetRequest{Key: key, Value: value})
	if err != nil {
		return fmt.Errorf("sidecar secrets: %w", err)
	}
	return nil
}

func (s *SidecarSecrets) Delete(ctx context.Context, key string) error {
	_, err := s.client.Delete(ctx, &secretsv1.DeleteRequest{Key: key})
	if err != nil {
		return fmt.Errorf("sidecar secrets: %w", err)
	}
	return nil
}

func (s *SidecarSecrets) List(ctx context.Context) ([]string, error) {
	resp, err := s.client.List(ctx, &secretsv1.ListRequest{})
	if err != nil {
		return nil, fmt.Errorf("sidecar secrets: %w", err)
	}
	return resp.GetKeys(), nil
}
