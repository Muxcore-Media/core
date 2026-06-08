// Package audit provides a default file-based audit logger for MuxCore.
// When configured with a non-empty LogPath, writes JSON Lines (one JSON
// object per line) to the specified file. When LogPath is empty, all
// operations are no-ops — safe to wire into the Fabric with zero config.
package audit

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// maxInMemoryEntries is the maximum number of entries retained in the
// in-memory buffer for Query/Export. Older entries are kept only on disk.
// This prevents unbounded memory growth on long-running deployments.
const maxInMemoryEntries = 50_000

// defaultMaxAuditSizeMB is the default audit log rotation threshold.
// When the file exceeds this size, it is renamed to .1 and a new file opened.
const defaultMaxAuditSizeMB = 100

// defaultMaxRotatedFiles is how many rotated files to keep (.1, .2, ...).
const defaultMaxRotatedFiles = 5

// FileLogger writes audit entries as JSONL to a local file.
// Thread-safe. No-op when LogPath is empty.
// Maintains a SHA-256 hash chain across entries for tamper detection.
// The in-memory buffer retains at most maxInMemoryEntries recent entries;
// older entries must be read directly from the JSONL file.
//
// Log rotation: when the file exceeds MaxSizeMB, it is rotated (renamed
// to path.1, path.2, ..., up to MaxRotatedFiles). Rotation is done on
// each Log() call — no background goroutine needed.
type FileLogger struct {
	mu              sync.Mutex
	path            string
	file            *os.File
	entries         []contracts.AuditEntry // in-memory ring buffer for Query/Export
	lastHash        string                 // SHA-256 hash of the previous entry (hex)
	signingKey      []byte                 // optional HMAC-SHA256 signing key
	MaxSizeMB       int                    // rotate when file exceeds this size (0 = defaultMaxAuditSizeMB)
	MaxRotatedFiles int                    // number of rotated files to keep (0 = defaultMaxRotatedFiles)
}

// NewFileLogger creates an audit logger. If logPath is empty, all operations
// are no-ops (safe to wire with zero config).
func NewFileLogger(logPath string) (*FileLogger, error) {
	fl := &FileLogger{
		path:            logPath,
		MaxSizeMB:       defaultMaxAuditSizeMB,
		MaxRotatedFiles: defaultMaxRotatedFiles,
	}
	if logPath == "" {
		return fl, nil
	}
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600) //nolint:gosec // path comes from operator configuration
	if err != nil {
		return nil, fmt.Errorf("audit: open %s: %w", logPath, err)
	}
	fl.file = f
	return fl, nil
}

// SetSigningKey configures an optional HMAC-SHA256 signing key for audit entries.
// When set, every Log() call produces an HMAC signature in the entry's Signature field.
// Pass nil to disable signing.
func (fl *FileLogger) SetSigningKey(key []byte) {
	fl.mu.Lock()
	defer fl.mu.Unlock()
	fl.signingKey = key
}

// Log writes an audit entry as a single JSON line. No-op when LogPath is empty.
// Populates PrevEntryHash (SHA-256 chain) and Signature (HMAC-SHA256 if signing key set).
// Rotates the log file when it exceeds MaxSizeMB.
func (fl *FileLogger) Log(ctx context.Context, entry contracts.AuditEntry) error {
	if fl.path == "" {
		return nil
	}

	fl.mu.Lock()
	defer fl.mu.Unlock()

	// Rotate if the file exceeds the size threshold.
	if err := fl.maybeRotateLocked(); err != nil {
		slog.Error("audit: log rotation failed", "error", err)
		// Continue writing to the current file rather than losing entries.
	}

	// Link the hash chain: PrevEntryHash = hash of the previous entry.
	entry.PrevEntryHash = fl.lastHash

	// Marshal the complete entry for storage.
	diskData, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("audit: marshal entry: %w", err)
	}

	// Compute the hash of this entry (without PrevEntryHash) for the chain.
	entryCopy := entry
	entryCopy.PrevEntryHash = ""
	canonData, err := json.Marshal(entryCopy)
	if err != nil {
		return fmt.Errorf("audit: marshal canonical entry: %w", err)
	}
	h := sha256.Sum256(canonData)
	fl.lastHash = hex.EncodeToString(h[:])

	// Sign if a signing key is configured.
	if fl.signingKey != nil {
		mac := hmac.New(sha256.New, fl.signingKey)
		mac.Write(canonData)
		entry.Signature = hex.EncodeToString(mac.Sum(nil))
		// Re-marshal with the signature included for the on-disk record.
		diskData, err = json.Marshal(entry)
		if err != nil {
			return fmt.Errorf("audit: marshal signed entry: %w", err)
		}
	}

	diskData = append(diskData, '\n')

	if _, err := fl.file.Write(diskData); err != nil {
		return fmt.Errorf("audit: write entry: %w", err)
	}
	if err := fl.file.Sync(); err != nil {
		return fmt.Errorf("audit: sync entry: %w", err)
	}

	fl.entries = append(fl.entries, entry)

	// Cap the in-memory buffer to prevent unbounded memory growth.
	// When the cap is exceeded, drop the oldest entries (ring-buffer semantics).
	if len(fl.entries) > maxInMemoryEntries {
		excess := len(fl.entries) - maxInMemoryEntries
		fl.entries = fl.entries[excess:]
	}

	return nil
}

// Query returns in-memory entries matching the filter. No-op when LogPath is empty.
func (fl *FileLogger) Query(ctx context.Context, filter contracts.AuditFilter) ([]contracts.AuditEntry, error) {
	if fl.path == "" {
		return []contracts.AuditEntry{}, nil
	}

	fl.mu.Lock()
	defer fl.mu.Unlock()

	var results []contracts.AuditEntry
	for _, e := range fl.entries {
		if filter.Actor != "" && e.Actor != filter.Actor {
			continue
		}
		if filter.Action != "" && e.Action != filter.Action {
			continue
		}
		if filter.Resource != "" && e.Resource != filter.Resource {
			continue
		}
		if filter.TraceID != "" && e.TraceID != filter.TraceID {
			continue
		}
		if !filter.From.IsZero() && e.Timestamp.Before(filter.From) {
			continue
		}
		if !filter.To.IsZero() && e.Timestamp.After(filter.To) {
			continue
		}
		results = append(results, e)
	}
	return results, nil
}

// Export writes all entries in the requested format as a stream.
// Supports "json" (JSON array) and "csv". No-op when LogPath is empty.
func (fl *FileLogger) Export(ctx context.Context, format string) (io.ReadCloser, error) {
	if fl.path == "" {
		return io.NopCloser(strings.NewReader("")), nil
	}

	fl.mu.Lock()
	defer fl.mu.Unlock()

	switch strings.ToLower(format) {
	case "json":
		data, err := json.Marshal(fl.entries)
		if err != nil {
			return nil, fmt.Errorf("audit: marshal export: %w", err)
		}
		return io.NopCloser(strings.NewReader(string(data))), nil
	case "csv":
		var sb strings.Builder
		sb.WriteString("id,timestamp,actor,action,resource,resource_id,trace_id,node_id\n")
		for _, e := range fl.entries {
			fmt.Fprintf(&sb, "%s,%s,%s,%s,%s,%s,%s,%s\n",
				e.ID, e.Timestamp.Format("2006-01-02T15:04:05Z"),
				e.Actor, e.Action, e.Resource, e.ResourceID,
				e.TraceID, e.NodeID)
		}
		return io.NopCloser(strings.NewReader(sb.String())), nil
	default:
		return nil, fmt.Errorf("audit: unsupported export format: %q", format)
	}
}

// VerifyChainIntegrity performs a standalone integrity check on the audit log
// hash chain within the given time range. Returns a verification result
// indicating whether the chain is intact and, if not, where the breaks occurred.
func (fl *FileLogger) VerifyChainIntegrity(ctx context.Context, from, to time.Time) (contracts.ChainVerificationResult, error) {
	fl.mu.Lock()
	defer fl.mu.Unlock()

	result := contracts.ChainVerificationResult{Valid: true}

	if fl.path == "" {
		return result, nil
	}

	// Filter entries in the time range (already sorted by insertion order = time order).
	var inRange []contracts.AuditEntry
	for _, e := range fl.entries {
		if !from.IsZero() && e.Timestamp.Before(from) {
			continue
		}
		if !to.IsZero() && e.Timestamp.After(to) {
			continue
		}
		inRange = append(inRange, e)
	}

	result.TotalEntries = len(inRange)
	if len(inRange) < 2 {
		return result, nil
	}

	// Verify the hash chain: each entry's PrevEntryHash must match
	// the SHA-256 of the previous entry's JSON representation.
	h := sha256.New()
	for i := 1; i < len(inRange); i++ {
		prev := inRange[i-1]
		curr := inRange[i]

		// Re-compute the previous entry's hash the same way Log() does:
		// JSON-marshal it (without the PrevEntryHash from the chain link)
		// and SHA-256 the result.
		prevCopy := prev
		prevCopy.PrevEntryHash = "" // strip chain link for re-hashing
		data, err := json.Marshal(prevCopy)
		if err != nil {
			return result, fmt.Errorf("audit: marshal entry %s for verification: %w", prev.ID, err)
		}
		h.Reset()
		h.Write(data)
		expected := hex.EncodeToString(h.Sum(nil))

		if curr.PrevEntryHash != "" && curr.PrevEntryHash != expected {
			result.Valid = false
			result.BrokenLinks = append(result.BrokenLinks, curr.ID)
			if result.FirstBrokenAt.IsZero() {
				result.FirstBrokenAt = curr.Timestamp
			}
		}
	}

	return result, nil
}

// Close flushes and closes the underlying file.
// maybeRotateLocked checks the current log file size and rotates if needed.
// Rotation: path.1, path.2, ... path.N (N = MaxRotatedFiles).
// Oldest file (.N) is deleted. Caller must hold fl.mu.
func (fl *FileLogger) maybeRotateLocked() error {
	if fl.file == nil {
		return nil
	}
	maxBytes := int64(fl.MaxSizeMB) * 1024 * 1024
	if maxBytes <= 0 {
		maxBytes = int64(defaultMaxAuditSizeMB) * 1024 * 1024
	}

	info, err := fl.file.Stat()
	if err != nil || info.Size() < maxBytes {
		return nil // no rotation needed
	}

	// Close the current file before renaming.
	fl.file.Close()
	fl.file = nil

	maxRotated := fl.MaxRotatedFiles
	if maxRotated <= 0 {
		maxRotated = defaultMaxRotatedFiles
	}

	// Shift existing rotated files: .N-1 → .N, ..., .1 → .2
	for i := maxRotated; i > 1; i-- {
		older := fmt.Sprintf("%s.%d", fl.path, i)
		newer := fmt.Sprintf("%s.%d", fl.path, i-1)
		if _, err := os.Stat(newer); err == nil {
			os.Rename(newer, older)
		}
	}
	// Rename current file to .1
	os.Rename(fl.path, fl.path+".1")

	// Open a new file.
	f, err := os.OpenFile(fl.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("audit: open new log after rotation: %w", err)
	}
	fl.file = f
	slog.Info("audit: log rotated", "path", fl.path, "max_mb", fl.MaxSizeMB)
	return nil
}

func (fl *FileLogger) Close() error {
	fl.mu.Lock()
	defer fl.mu.Unlock()
	if fl.file != nil {
		return fl.file.Close()
	}
	return nil
}
