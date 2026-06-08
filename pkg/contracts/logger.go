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

// StructuredLogger provides leveled, structured logging for modules.
// This is the general-purpose logger — distinct from AuditLogger which
// handles security-relevant audit events specifically.
//
// Modules call StructuredLogger instead of importing a specific logging
// library. This makes the logging backend swappable (slog, zap, zerolog,
// or a future standard) without changing module code.
//
// If no StructuredLogger is registered, log calls are no-ops — modules
// do not need nil checks.
type StructuredLogger interface {
	// Debug logs at debug level with structured fields.
	Debug(ctx context.Context, msg string, fields map[string]any)

	// Info logs at info level with structured fields.
	Info(ctx context.Context, msg string, fields map[string]any)

	// Warn logs at warning level with structured fields.
	Warn(ctx context.Context, msg string, fields map[string]any)

	// Error logs at error level with structured fields.
	Error(ctx context.Context, msg string, fields map[string]any)

	// Level returns the configured minimum log level. Messages below this
	// level are silently dropped by the provider.
	Level() LogLevel
}
