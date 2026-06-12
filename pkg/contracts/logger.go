package contracts

import "context"

// LogLevel describes the severity of a log message.
type LogLevel string

const (
	LogLevelDebug LogLevel = "debug"
	LogLevelInfo  LogLevel = "info"
	LogLevelWarn  LogLevel = "warn"
	LogLevelError LogLevel = "error"
)

// SensitiveLogFieldNames returns a copy of the curated list of field name
// prefixes that commonly carry credentials, tokens, or PII. Redaction
// providers and logging implementations SHOULD use this list as a default
// deny-set.
//
// This list is not exhaustive — implementations may extend it based
// on their domain. The convention is lowercase field names as they
// appear in structured log fields.
func SensitiveLogFieldNames() []string {
	return []string{
		"password", "passwd", "secret", "token", "api_key", "apikey",
		"credential", "private_key", "ssh_key", "access_key", "auth",
		"authorization", "cookie", "jwt", "session", "signature",
		"credit_card", "ssn", "social_security", "passport",
	}
}

// StructuredLogger provides leveled, structured logging for modules.
// This is the general-purpose logger — distinct from AuditLogger which
// handles security-relevant audit events specifically.
//
// Modules call StructuredLogger instead of importing a specific logging
// library. This makes the logging backend swappable (slog, zap, zerolog,
// or a future standard) without changing module code.
//
// SECURITY: All log methods accept fields as map[string]any. This map
// can inadvertently contain credentials, tokens, PII, or other sensitive
// data. Callers MUST:
//   - Apply DataRedactionProvider.Redact() to the fields map before
//     passing it to any log method if the map contains untrusted data.
//   - Never log full Identity structs — use Identity.SafeInfo().
//   - Never log Session structs — use Session.Safe().
//   - Avoid logging raw request bodies, query parameters, or headers.
//
// Implementations SHOULD apply automatic field-name-based redaction
// for keys matching SensitiveLogFieldNames before writing to any sink.
//
// If no StructuredLogger is registered, log calls are no-ops — modules
// do not need nil checks.
type StructuredLogger interface {
	// Debug logs at debug level with structured fields.
	// SECURITY: fields may contain sensitive data — redact before calling.
	Debug(ctx context.Context, msg string, fields map[string]any)

	// Info logs at info level with structured fields.
	// SECURITY: fields may contain sensitive data — redact before calling.
	Info(ctx context.Context, msg string, fields map[string]any)

	// Warn logs at warning level with structured fields.
	// SECURITY: fields may contain sensitive data — redact before calling.
	Warn(ctx context.Context, msg string, fields map[string]any)

	// Error logs at error level with structured fields.
	// SECURITY: fields may contain sensitive data — redact before calling.
	Error(ctx context.Context, msg string, fields map[string]any)

	// Level returns the configured minimum log level. Messages below this
	// level are silently dropped by the provider.
	Level() LogLevel
}

// RedactingLogger is an optional extension that guarantees all log output
// is redacted. Implementations that embed automatic redaction can declare
// this interface so that callers can skip manual redaction.
//
// Modules MAY type-assert their StructuredLogger to RedactingLogger to
// determine if manual redaction is necessary.
type RedactingLogger interface {
	StructuredLogger

	// IsRedacting reports whether this logger automatically applies
	// redaction to all fields before writing to sinks.
	IsRedacting() bool
}
