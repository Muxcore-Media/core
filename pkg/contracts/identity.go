package contracts

import "context"

// Identity represents the caller extracted from an authentication context.
// Fields are intentionally broad — the identity provider populates whatever
// it can determine, and downstream consumers interpret what they understand.
//
// SECURITY: Claims and Extra carry provider-specific data that may include
// sensitive information from auth tokens (JWT payloads, API key metadata).
// These fields MUST be treated as potentially sensitive:
//   - NEVER serialize a full Identity to logs without redaction.
//   - NEVER include Identity in event payloads unless the event is explicitly
//     an auth/identity event with access controls.
//   - Use SafeInfo() when passing identity data to subsystems that do not
//     need provider-specific claims.
//
// The SafeInfo method strips Claims and Extra, retaining only the typed fields
// that Authorizer needs for permission checks.
type Identity struct {
	// ID is the unique identifier for this caller (user ID, service account ID).
	ID string

	// Kind describes the type of identity (e.g., "user", "service", "api-key").
	Kind string

	// Roles are the authorization roles assigned to this identity.
	Roles []string

	// Claims carries provider-specific claims extracted from the auth token.
	// For JWT this is the decoded payload. For API keys this may be scopes.
	//
	// SECURITY: May contain sensitive data. Redact before logging.
	Claims map[string]any

	// Extra carries provider-specific extensions that consumers can interpret.
	//
	// SECURITY: May contain sensitive data. Redact before logging.
	Extra map[string]any
}

// SafeInfo returns a copy of the Identity with Claims and Extra stripped.
// Use this when passing identity to subsystems that do not need provider-specific
// data (logging, metrics, events, workflows).
func (id Identity) SafeInfo() Identity {
	return Identity{
		ID:    id.ID,
		Kind:  id.Kind,
		Roles: append([]string(nil), id.Roles...),
		// Claims and Extra deliberately excluded
	}
}

// IdentityProvider extracts caller identity from context. Modules call this
// to determine who is making a request before passing that identity to the
// Authorizer for permission checks.
//
// This contract is provider-agnostic: a module that calls ExtractIdentity
// works identically whether the backend is JWT validation, API key lookup,
// mTLS certificate extraction, or a future protocol.
//
// SECURITY: When no IdentityProvider is registered, the system MUST apply a
// deny-by-default access policy. Anonymous access (no identity) should only
// be permitted for explicitly public endpoints. The fabric should refuse to
// start in production mode without an IdentityProvider — see EnforcedIdentityProvider.
//
// Discovered via FindByCapability("identity"). If no module implements this
// contract, the fabric treats all requests as unauthenticated (nil identity).
// Modules MUST check for nil identity and apply their own access policy.
// The recommended safe default is to deny access when identity is nil unless
// the endpoint is explicitly marked as public.
type IdentityProvider interface {
	// ExtractIdentity extracts the caller identity from the given context.
	// Returns (nil, nil) if no identity is present (unauthenticated request).
	// Returns an error only if identity extraction itself fails (e.g., invalid
	// token format), not if the request is simply unauthenticated.
	ExtractIdentity(ctx context.Context) (*Identity, error)
}
