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
	Details    map[string]string // arbitrary context — MUST NOT contain credentials or PII
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

// ChainVerificationResult reports the outcome of a hash-chain integrity check.
type ChainVerificationResult struct {
	// Valid is true if the entire chain is intact with no broken links.
	Valid bool
	// TotalEntries is the number of entries in the verified range.
	TotalEntries int
	// BrokenLinks is the list of entry IDs where the PrevEntryHash does
	// not match the computed hash of the previous entry.
	BrokenLinks []string
	// FirstBrokenAt is the timestamp of the first detected break, or
	// the zero time if no breaks were found.
	FirstBrokenAt time.Time
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
	//
	// SECURITY: Details MUST NOT contain credentials, tokens, or PII.
	// The caller is responsible for redacting sensitive data before
	// calling Log. Use DataRedactionProvider if the details map is
	// built from untrusted input.
	Log(ctx context.Context, entry AuditEntry) error

	// Query returns entries matching the filter.
	Query(ctx context.Context, filter AuditFilter) ([]AuditEntry, error)

	// Export writes all entries in the given format (json, csv).
	Export(ctx context.Context, format string) (io.ReadCloser, error)

	// VerifyChainIntegrity performs a standalone integrity check on the
	// audit log hash chain in the given time range. Unlike Query with
	// VerifyChain=true, this method does not return entry data — it only
	// returns the verification result. This enables independent integrity
	// audits without exposing audit content to the verifier.
	//
	// If the provider does not support hash chains, this method returns
	// a result with Valid=true and TotalEntries=0 (no chain to verify).
	VerifyChainIntegrity(ctx context.Context, from, to time.Time) (ChainVerificationResult, error)
}
