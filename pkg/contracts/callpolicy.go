package contracts

import (
	"context"
	"errors"
)

// ErrCallDenied is returned when a call policy provider denies access.
// This is distinct from ErrCallNoPolicy which means no provider is registered.
var ErrCallDenied = errors.New("call policy: access denied")

// ErrCallNoPolicy is returned when no CallPolicyProvider is registered.
// In production, the mesh MUST reject cross-module calls when no provider
// is present rather than defaulting to open mode. This error signals that
// the deployment is missing a required security component.
var ErrCallNoPolicy = errors.New("call policy: no provider registered — calls are denied by default")

// CallPolicyProvider determines whether one module is allowed to call another.
// Implemented by modules that define inter-module access control. The mesh
// client consults this before dispatching calls.
//
// SECURITY: When no CallPolicyProvider is registered, the mesh MUST deny all
// cross-module calls and return ErrCallNoPolicy. This is a change from the
// previous behavior where the default was open mode. Deployments that want
// open mode must register an explicit permissive policy provider.
//
// Discovered via FindByCapability("call.policy"). Only one policy provider
// is consulted — if multiple register, the first found wins.
type CallPolicyProvider interface {
	// AllowCall returns (true, nil) if caller is permitted to invoke method on target.
	// Returns (false, nil) if access is denied, or (false, error) if the policy engine
	// encountered an error (transient failure, configuration issue, etc.).
	AllowCall(ctx context.Context, callerModuleID, targetModuleID, method string) (bool, error)
}

// Context keys for caller identity propagation through the mesh.
//
// SECURITY: WithCallerID stores the caller module ID as an unauthenticated
// context value. Any code in the same process can set any module ID.
// The mesh infrastructure (gRPC server) MUST overwrite the caller ID
// from the authenticated gRPC metadata before consulting the policy
// provider — never trust the context value alone from untrusted sources.
//
// For gRPC-based mesh calls, use the gRPC metadata header
// "x-muxcore-caller-id" which the mesh server extracts from the
// authenticated connection. The context function below is retained
// for in-process (local) dispatch where the caller is trusted.
type callerIDKey struct{}

// WithCallerID returns a context carrying the given module ID as the caller.
// Modules use this before calling Mesh.Call() so the call policy provider
// can identify the caller.
//
// SECURITY: Only use this for in-process (local) dispatch where the caller
// is part of the same trusted process. For gRPC mesh calls, the server
// extracts the caller ID from the authenticated connection metadata.
//
// Deprecated: The gRPC mesh server now reads caller identity from the
// authenticated connection. This function remains for local dispatch only
// and will be removed when all modules have migrated to the sidecar model.
func WithCallerID(ctx context.Context, moduleID string) context.Context {
	return context.WithValue(ctx, callerIDKey{}, moduleID)
}

// CallerIDFromContext extracts the caller module ID from context, or ""
// if none was set.
func CallerIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(callerIDKey{}).(string); ok {
		return id
	}
	return ""
}
