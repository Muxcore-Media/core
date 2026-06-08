// Package audit provides a default file-based audit logger for MuxCore.
// When configured with a non-empty LogPath, writes JSON Lines (one JSON
// object per line) to the specified file. When LogPath is empty, all
// operations are no-ops — safe to wire into the Fabric with zero config.
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// FileLogger writes audit entries as JSONL to a local file.
// Thread-safe. No-op when LogPath is empty.
type FileLogger struct {
	mu      sync.Mutex
	path    string
	file    *os.File
	entries []contracts.AuditEntry // in-memory buffer for Query/Export
}

// NewFileLogger creates an audit logger. If logPath is empty, all operations
// are no-ops (safe to wire with zero config).
func NewFileLogger(logPath string) (*FileLogger, error) {
	fl := &FileLogger{path: logPath}
	if logPath == "" {
		return fl, nil
	}
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("audit: open %s: %w", logPath, err)
	}
	fl.file = f
	return fl, nil
}

// Log writes an audit entry as a single JSON line. No-op when LogPath is empty.
func (fl *FileLogger) Log(ctx context.Context, entry contracts.AuditEntry) error {
	if fl.path == "" {
		return nil
	}

	fl.mu.Lock()
	defer fl.mu.Unlock()

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("audit: marshal entry: %w", err)
	}
	data = append(data, '\n')

	if _, err := fl.file.Write(data); err != nil {
		return fmt.Errorf("audit: write entry: %w", err)
	}

	fl.entries = append(fl.entries, entry)
	return nil
}

// Query returns in-memory entries matching the filter. No-op when LogPath is empty.
func (fl *FileLogger) Query(ctx context.Context, filter contracts.AuditFilter) ([]contracts.AuditEntry, error) {
	if fl.path == "" {
		return nil, nil
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
			sb.WriteString(fmt.Sprintf("%s,%s,%s,%s,%s,%s,%s,%s\n",
				e.ID, e.Timestamp.Format("2006-01-02T15:04:05Z"),
				e.Actor, e.Action, e.Resource, e.ResourceID,
				e.TraceID, e.NodeID))
		}
		return io.NopCloser(strings.NewReader(sb.String())), nil
	default:
		return nil, fmt.Errorf("audit: unsupported export format: %q", format)
	}
}

// Close flushes and closes the underlying file.
func (fl *FileLogger) Close() error {
	fl.mu.Lock()
	defer fl.mu.Unlock()
	if fl.file != nil {
		return fl.file.Close()
	}
	return nil
}
