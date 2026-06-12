package mock

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// Audit is a mock of contracts.AuditLogger for testing.
// Records all entries in memory for later inspection.
type Audit struct {
	mu      sync.RWMutex
	Entries []contracts.AuditEntry
}

// NewAudit creates an empty audit mock.
func NewAudit() *Audit {
	return &Audit{}
}

// Log records an audit entry.
func (a *Audit) Log(ctx context.Context, entry contracts.AuditEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Entries = append(a.Entries, entry)
	return nil
}

// Query filters recorded entries by the given filter.
func (a *Audit) Query(ctx context.Context, filter contracts.AuditFilter) ([]contracts.AuditEntry, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var results []contracts.AuditEntry
	for _, e := range a.Entries {
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

// Export returns all entries in the requested format.
func (a *Audit) Export(ctx context.Context, format string) (io.ReadCloser, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	switch strings.ToLower(format) {
	case "json":
		data, err := json.Marshal(a.Entries)
		if err != nil {
			return nil, fmt.Errorf("audit mock: marshal: %w", err)
		}
		return io.NopCloser(strings.NewReader(string(data))), nil
	case "csv":
		var buf strings.Builder
		w := csv.NewWriter(&buf)
		w.Write([]string{"id", "timestamp", "actor", "action", "resource"})
		for _, e := range a.Entries {
			w.Write([]string{e.ID, e.Timestamp.Format("2006-01-02T15:04:05Z"), e.Actor, e.Action, e.Resource})
		}
		w.Flush()
		return io.NopCloser(strings.NewReader(buf.String())), nil
	default:
		return nil, fmt.Errorf("audit mock: unsupported export format: %q", format)
	}
}

// VerifyChainIntegrity checks the hash chain of entries within the given time range.
// Returns a valid result if all entries form an unbroken chain, or lists broken links.
func (a *Audit) VerifyChainIntegrity(ctx context.Context, from, to time.Time) (contracts.ChainVerificationResult, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	result := contracts.ChainVerificationResult{Valid: true}

	// Filter entries in the time range and sort by timestamp.
	var inRange []contracts.AuditEntry
	for _, e := range a.Entries {
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

	// Check PrevEntryHash chain: each entry's PrevEntryHash must match
	// the SHA-256 of the previous entry's canonical fields.
	h := sha256.New()
	for i := 1; i < len(inRange); i++ {
		prev := inRange[i-1]
		curr := inRange[i]

		h.Reset()
		fmt.Fprintf(h, "%s|%s|%s|%s|%s",
			prev.ID, prev.Actor, prev.Action, prev.Resource, prev.Timestamp.Format(time.RFC3339Nano))
		expected := fmt.Sprintf("%x", h.Sum(nil))

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

// VerifyAll returns the same result as VerifyChainIntegrity since the mock
// has no rotated files — all entries are in the in-memory buffer.
func (a *Audit) VerifyAll(ctx context.Context) (contracts.ChainVerificationResult, error) {
	return a.VerifyChainIntegrity(ctx, time.Time{}, time.Time{})
}

// Count returns the number of recorded entries (test helper).
func (a *Audit) Count() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.Entries)
}

var _ contracts.AuditLogger = (*Audit)(nil)
