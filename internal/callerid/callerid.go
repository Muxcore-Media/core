// Package callerid propagates module caller identity through context.
// Used by the gRPC mesh server to set the authenticated caller, and by
// internal subsystems (event bus, storage server) to extract it for
// policy enforcement. This is NOT a module-facing API — module authors
// receive callerID as a direct parameter from policy interfaces.
package callerid

import "context"

type key struct{}

// Set returns a context carrying the given module ID as the caller.
// Called by the gRPC auth interceptor after verifying the connection identity.
func Set(ctx context.Context, moduleID string) context.Context {
	return context.WithValue(ctx, key{}, moduleID)
}

// Get extracts the caller module ID from context. Returns "" if not set.
func Get(ctx context.Context) string {
	if id, ok := ctx.Value(key{}).(string); ok {
		return id
	}
	return ""
}
