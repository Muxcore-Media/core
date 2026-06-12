package grpcmesh

import (
	"context"
	"fmt"

	policyv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/policy/v1"
	"google.golang.org/grpc"
)

// SidecarCallPolicy wraps a gRPC connection to a sidecar module's PolicyService
// and implements contracts.CallPolicyProvider by forwarding queries over gRPC.
//
// Created when core discovers a sidecar module registered with the "call.policy"
// capability and a non-empty HTTPAddr (used as the gRPC dial target).
type SidecarCallPolicy struct {
	client policyv1.PolicyServiceClient
}

// NewSidecarCallPolicy creates a CallPolicyProvider backed by a sidecar module's
// gRPC PolicyService. conn is the gRPC connection to the sidecar module.
func NewSidecarCallPolicy(conn *grpc.ClientConn) *SidecarCallPolicy {
	return &SidecarCallPolicy{
		client: policyv1.NewPolicyServiceClient(conn),
	}
}

// AllowCall forwards the policy check to the sidecar module via gRPC.
// Returns (false, nil) if the module denies the call, or the gRPC error.
func (s *SidecarCallPolicy) AllowCall(ctx context.Context, callerModuleID, targetModuleID, method string) (bool, error) {
	resp, err := s.client.AllowCall(ctx, &policyv1.AllowCallRequest{
		CallerModuleId: callerModuleID,
		TargetModuleId: targetModuleID,
		Method:         method,
	})
	if err != nil {
		return false, fmt.Errorf("sidecar call policy: %w", err)
	}
	if !resp.GetAllowed() {
		return false, nil
	}
	return true, nil
}

// SidecarPublishPolicy wraps a gRPC connection to a sidecar module's PolicyService
// and implements contracts.PublishPolicyProvider by forwarding queries over gRPC.
type SidecarPublishPolicy struct {
	client policyv1.PolicyServiceClient
}

// NewSidecarPublishPolicy creates a PublishPolicyProvider backed by a sidecar module.
func NewSidecarPublishPolicy(conn *grpc.ClientConn) *SidecarPublishPolicy {
	return &SidecarPublishPolicy{
		client: policyv1.NewPolicyServiceClient(conn),
	}
}

// CanPublish forwards the publish policy check to the sidecar module via gRPC.
func (s *SidecarPublishPolicy) CanPublish(ctx context.Context, callerID, eventType string) (bool, error) {
	resp, err := s.client.AllowPublish(ctx, &policyv1.AllowPublishRequest{
		CallerModuleId: callerID,
		EventType:      eventType,
	})
	if err != nil {
		return false, fmt.Errorf("sidecar publish policy: %w", err)
	}
	return resp.GetAllowed(), nil
}
