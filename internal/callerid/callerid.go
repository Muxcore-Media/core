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
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, key{}, moduleID)
}

// Get extracts the caller module ID from context. Returns "" if not set.
func Get(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if id, ok := ctx.Value(key{}).(string); ok {
		return id
	}
	return ""
}

// Principal kinds recorded alongside the caller ID by the gRPC auth
// interceptor (ADR-0017).
const (
	// KindModule is a mesh module (service principal): a verified client
	// certificate CN, or x-caller-id in the dev/insecure profile.
	KindModule = "module"
	// KindUser is an end user resolved from a bearer token by the identity
	// provider.
	KindUser = "user"
	// KindPublic marks calls to open methods ("_public").
	KindPublic = "public"
)

type kindKey struct{}

// SetPrincipal sets the caller ID and records the principal kind.
func SetPrincipal(ctx context.Context, id, kind string) context.Context {
	ctx = Set(ctx, id)
	return context.WithValue(ctx, kindKey{}, kind)
}

// Kind returns the principal kind recorded by SetPrincipal, or "" when the
// caller was set without a kind (e.g. test harnesses using Set).
func Kind(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if k, ok := ctx.Value(kindKey{}).(string); ok {
		return k
	}
	return ""
}
