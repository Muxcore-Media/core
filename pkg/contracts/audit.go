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
	// PrevEntryHash is the SHA-256 hash of the chronologically previous audit entry.
	// This forms a tamper-evident hash chain. Zero value for the first entry in a log.
	PrevEntryHash string
	// Signature is an optional HMAC-SHA256 over the entry fields using a root key.
	// Enables verification that the entry was produced by an authorized audit logger.
	// Empty if the audit logger does not support cryptographic signing.
	Signature string
}

// AuditFilter narrows audit queries.
type AuditFilter struct {
	Actor    string
	Action   string
	Resource string
	From     time.Time
	To       time.Time
	TraceID  string
	// VerifyChain, when true, instructs Query to validate the hash chain
	// of returned entries and exclude or flag entries with broken links.
	VerifyChain bool
}

// AuditLogger records and queries audit entries.
type AuditLogger interface {
	// Log records an audit entry.
	Log(ctx context.Context, entry AuditEntry) error

	// Query returns entries matching the filter.
	Query(ctx context.Context, filter AuditFilter) ([]AuditEntry, error)

	// Export writes all entries in the given format (json, csv).
	Export(ctx context.Context, format string) (io.ReadCloser, error)
}
