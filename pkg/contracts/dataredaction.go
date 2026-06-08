package contracts

import "context"

// DataRedactionProvider redacts sensitive data from structured payloads
// before they are logged, audited, or stored. This contract exists so
// modules never write PII (emails, IP addresses, tokens, personal data)
// to plaintext logs or audit trails.
//
// Modules call Redact on any map[string]any before passing it to
// StructuredLogger, AuditLogger, or any persistent storage. The provider
// applies configured rules to replace sensitive values with redacted
// placeholders.
//
// The rules parameter is an opaque list of rule identifiers that the
// provider interprets. Common patterns: field-name matching ("email",
// "password"), path expressions ("user.address.street"), regex patterns
// ("/\\d{3}-\\d{2}-\\d{4}/"), or category tags ("pii", "phi").
// The provider documents which rule syntax it supports.
//
// This contract is provider-agnostic: a module that calls Redact works
// identically whether the backend uses regex, field-name allowlists,
// an external DLP service, or a future redaction engine.
//
// If no DataRedactionProvider is registered, Redact returns the input
// unchanged — modules degrade without nil checks but SHOULD document
// that redaction is inactive.
type DataRedactionProvider interface {
	// Redact returns a new map with sensitive values replaced according
	// to the given rules. The input map is not modified. Rules are
	// provider-specific identifiers; the provider silently ignores
	// rules it doesn't recognize.
	Redact(ctx context.Context, data map[string]any, rules []string) (map[string]any, error)
}
