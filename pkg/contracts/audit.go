package contracts

import (
	"context"
	"io"
	"time"
)

// AuditEntry is a single audit log record.
type AuditEntry struct {
	ID         string
	Timestamp  time.Time
	Actor      string            // user ID, "system", or module ID
	Action     string            // e.g., "http.request", "module.registered", "media.requested"
	Resource   string            // e.g., "/api/movies", "admin-ui", "Inception"
	ResourceID string            // UUID of the affected resource, if applicable
	Details    map[string]string // arbitrary context
	TraceID    string
	NodeID     string
}

// AuditFilter narrows audit queries.
type AuditFilter struct {
	Actor    string
	Action   string
	Resource string
	From     time.Time
	To       time.Time
	TraceID  string
}

// AuditLogger records and queries audit entries.
type AuditLogger interface {
	// Log records an audit entry.
	Log(ctx context.Context, entry AuditEntry) error

	// Query returns entries matching the filter.
	Query(ctx context.Context, filter AuditFilter) ([]AuditEntry, error)

	// Export writes all entries in the given format (json, csv).
	Export(ctx context.Context, format string) (io.Reader, error)
}
