package contracts

import "context"

// CallPolicyProvider determines whether one module is allowed to call another.
// Implemented by modules that define inter-module access control. The mesh
// client consults this before dispatching calls; if no provider is registered,
// all calls are allowed (open mode).
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
type callerIDKey struct{}

// WithCallerID returns a context carrying the given module ID as the caller.
// Modules use this before calling Mesh.Call() so the call policy provider
// can identify the caller.
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
