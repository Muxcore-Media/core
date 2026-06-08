package contracts

import "context"

// CredentialType enumerates well-known authentication schemes.
// Implementations MUST validate the type against this set to prevent
// credential-type confusion attacks (e.g., an attacker sending a
// password in a "token"-typed credential).
type CredentialType string

const (
	CredentialTypePassword CredentialType = "password"
	CredentialTypeToken    CredentialType = "token"
	CredentialTypeCert     CredentialType = "cert"
	CredentialTypeAPIKey   CredentialType = "api-key"
)

// Credentials carries authentication material to the AuthProvider.
// Type identifies the credential scheme and Data holds the opaque
// credential payload.
//
// SECURITY: Implementations MUST NOT log the Data field. Callers
// MUST NOT include credentials in structured log fields, audit
// entries, or event payloads. Use the SecretsProvider contract to
// store and retrieve credentials instead of passing them through
// application code.
//
// Backward-compat: Credentials.Type remains string (not CredentialType)
// so existing providers that use custom type strings continue to work.
// New implementations SHOULD validate against the CredentialType constants.
type Credentials struct {
	Type string
	Data []byte
}

// AuthProvider authenticates callers and validates existing sessions.
//
// SECURITY: Implementations MUST NOT log tokens. The token parameter
// on Validate and the Token field on Session are sensitive material.
// If you need to log authentication events, use AuditLogger with
// redacted token references (e.g., a SHA-256 prefix of the token).
type AuthProvider interface {
	// Authenticate verifies credentials and returns a session if valid.
	// The returned Session.Token is the bearer token for subsequent
	// Validate calls.
	Authenticate(ctx context.Context, credentials Credentials) (Session, error)

	// Validate checks whether a token is still valid and returns the
	// associated session. Returns an error if the token is expired,
	// revoked, or malformed.
	//
	// SECURITY: The token parameter MUST NOT be logged. Use a
	// cryptographically safe prefix (first 8 chars of SHA-256) for
	// audit trail references.
	Validate(ctx context.Context, token string) (Session, error)

	// Revoke invalidates a token so it can no longer be used for
	// Validate calls. Already-established sessions are not affected
	// until their next Validate.
	Revoke(ctx context.Context, token string) error
}

// Session represents an authenticated session.
//
// SECURITY WARNING: The Token field carries the raw bearer token.
// Session structs flow through the authorization path (Authorizer.Can),
// and can be inadvertently serialized in logs, audit entries, or event
// payloads. Consumers that pass Session to loggers or serializers MUST
// zero the Token field first. Use the Safe() method when passing
// session identity to non-security subsystems.
type Session struct {
	UserID      string
	Username    string
	Roles       []string
	Permissions []string
	Token       string // SECURITY: zero before logging or serializing
}

// Safe returns a copy of the Session with the Token field zeroed.
// Use this when passing session information to subsystems that do
// not need the raw token (logging, metrics, events, workflows).
func (s Session) Safe() Session {
	return Session{
		UserID:      s.UserID,
		Username:    s.Username,
		Roles:       append([]string(nil), s.Roles...),
		Permissions: append([]string(nil), s.Permissions...),
		Token:       "", // deliberately zeroed
	}
}

// Permission represents a named permission in the authorization system.
// The string format is "<domain>.<verb>" or "<domain>.<resource>.<verb>",
// e.g. "storage.read", "media.movies.delete", "admin.*".
//
// Wildcard support:
//   - "*" matches all permissions (superuser)
//   - "domain.*" matches all permissions within a domain
//   - "domain.resource.*" matches all permissions on a specific resource
type Permission string

// Action represents an operation a caller wants to perform.
// Standard verbs: "create", "read", "update", "delete", "list", "execute".
type Action string

const (
	ActionCreate  Action = "create"
	ActionRead    Action = "read"
	ActionUpdate  Action = "update"
	ActionDelete  Action = "delete"
	ActionList    Action = "list"
	ActionExecute Action = "execute"
)

// ResourceDescriptor carries the resource identifier and optional
// attributes for attribute-based access decisions.
type ResourceDescriptor struct {
	// Type is the resource kind, e.g. "media", "storage", "module".
	Type string
	// ID is the resource identifier, e.g. a UUID or key path.
	// Empty means the check is at the resource-type level.
	ID string
	// Attributes carries additional resource properties that
	// policy engines can use for ABAC decisions (owner, labels, tier).
	// MUST NOT contain credentials or PII.
	Attributes map[string]string
}

// Authorizer checks whether a session is permitted to perform an
// action on a resource.
//
// For resource-level checks with ABAC support, implement the optional
// ResourceAuthorizer interface alongside Authorizer. Callers that need
// resource-level authorization type-assert to ResourceAuthorizer and fall
// back to Authorizer.Can if it's not supported.
type Authorizer interface {
	// Can performs a permission check using a flat (action, resource) pair.
	//
	// SECURITY: The session parameter contains a raw token. Authorizer
	// implementations MUST NOT log or store the full Session struct.
	// Use session.Safe() if identity information is needed for audit.
	Can(ctx context.Context, session Session, action string, resource string) (bool, error)
}

// ResourceAuthorizer is an optional extension to Authorizer that enables
// resource-level permission checks with ABAC support. Implement this
// alongside Authorizer when your policy engine supports hierarchical
// resource permissions.
//
// Callers that need resource-level authorization type-assert to this
// interface and fall back to Authorizer.Can() if not supported.
type ResourceAuthorizer interface {
	Authorizer

	// CanWithResource performs a permission check with resource context.
	// The resource descriptor enables hierarchical checks, ABAC policies,
	// and resource-ownership gating. When ResourceDescriptor.ID is empty,
	// the check is scoped to the resource type.
	CanWithResource(ctx context.Context, session Session, action Action, resource ResourceDescriptor) (bool, error)
}
