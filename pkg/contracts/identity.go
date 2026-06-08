package contracts

import "context"

// Identity represents the caller extracted from an authentication context.
// Fields are intentionally broad — the identity provider populates whatever
// it can determine, and downstream consumers interpret what they understand.
type Identity struct {
	// ID is the unique identifier for this caller (user ID, service account ID).
	ID string

	// Kind describes the type of identity (e.g., "user", "service", "api-key").
	Kind string

	// Roles are the authorization roles assigned to this identity.
	Roles []string

	// Claims carries provider-specific claims extracted from the auth token.
	// For JWT this is the decoded payload. For API keys this may be scopes.
	Claims map[string]any

	// Extra carries provider-specific extensions that consumers can interpret.
	Extra map[string]any
}

// IdentityProvider extracts caller identity from context. Modules call this
// to determine who is making a request before passing that identity to the
// Authorizer for permission checks.
//
// This contract is provider-agnostic: a module that calls ExtractIdentity
// works identically whether the backend is JWT validation, API key lookup,
// mTLS certificate extraction, or a future protocol.
//
// If no IdentityProvider is registered, callers are treated as anonymous
// (no identity) — modules should apply their own default access policy.
type IdentityProvider interface {
	// ExtractIdentity extracts the caller identity from the given context.
	// Returns (nil, nil) if no identity is present (unauthenticated request).
	// Returns an error only if identity extraction itself fails (e.g., invalid
	// token format), not if the request is simply unauthenticated.
	ExtractIdentity(ctx context.Context) (*Identity, error)
}
