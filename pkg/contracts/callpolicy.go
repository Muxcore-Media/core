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
// Discovered via FindByCapability(CapabilityCallPolicy). Only one policy
// provider is consulted — if multiple register, the first found wins.
type CallPolicyProvider interface {
	// AllowCall returns (true, nil) if caller is permitted to invoke method on target.
	// Returns (false, nil) if access is denied, or (false, error) if the policy engine
	// encountered an error (transient failure, configuration issue, etc.).
	AllowCall(ctx context.Context, callerModuleID, targetModuleID, method string) (bool, error)
}
