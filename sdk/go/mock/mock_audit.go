package mock

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

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

// Count returns the number of recorded entries (test helper).
func (a *Audit) Count() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.Entries)
}

var _ contracts.AuditLogger = (*Audit)(nil)
