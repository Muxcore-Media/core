// Package audit provides a default file-based audit logger for MuxCore.
// When configured with a non-empty LogPath, writes JSON Lines (one JSON
// object per line) to the specified file. When LogPath is empty, all
// operations are no-ops — safe to instantiate with zero config.
//
//nolint:govet // struct field alignment
package audit

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
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

// chainStatePath returns the path to the sidecar file that persists the
// hash chain state across restarts: <logPath>.chain
func chainStatePath(logPath string) string {
	return logPath + ".chain"
}

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
	// SyncFlushInterval controls fsync behavior:
	//   0 (default) — sync after every write (safest, slowest)
	//   >0          — sync at most once per interval via background goroutine
	// Setting to e.g. 1s batches writes and syncs periodically, trading
	// up to 1s of data loss on crash for ~100x throughput improvement.
	SyncFlushInterval time.Duration
	syncTicker        *time.Ticker
	syncStop          chan struct{}
	syncDone          sync.WaitGroup
	closeSyncOnce     sync.Once
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
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // path comes from operator configuration
	if err != nil {
		return nil, fmt.Errorf("audit: open %s: %w", logPath, err)
	}
	fl.file = f

	// Restore the last hash from the chain state file so the hash chain
	// survives process restarts. If the file doesn't exist (first run or
	// after manual truncation), lastHash starts empty and the chain resets.
	if data, readErr := os.ReadFile(chainStatePath(logPath)); readErr == nil {
		parts := strings.SplitN(strings.TrimSpace(string(data)), "\n", 2)
		if len(parts) > 0 && parts[0] != "" {
			fl.lastHash = parts[0]
		}
		// Check for truncation: if we have a recorded file size and the
		// actual file is smaller, the log was truncated since last write.
		fi, statErr := f.Stat()
		if statErr == nil && len(parts) > 1 && parts[1] != "" {
			var expectedSize int64
			if _, parseErr := fmt.Sscanf(parts[1], "%d", &expectedSize); parseErr == nil && expectedSize > 0 {
				if fi.Size() < expectedSize {
					slog.Warn("audit: log file truncated since last write",
						"expected_bytes", expectedSize,
						"actual_bytes", fi.Size(),
					)
				}
			}
		}
	}

	if fl.SyncFlushInterval > 0 {
		fl.startSyncLoop()
	}
	return fl, nil
}

// startSyncLoop launches a background goroutine that periodically syncs the
// audit file. Only used when SyncFlushInterval > 0 (batching mode).
func (fl *FileLogger) startSyncLoop() {
	fl.syncTicker = time.NewTicker(fl.SyncFlushInterval)
	fl.syncStop = make(chan struct{})
	fl.syncDone.Add(1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("audit sync loop panic recovered", "panic", r)
			}
			fl.syncDone.Done()
		}()
		for {
			select {
			case <-fl.syncTicker.C:
				fl.mu.Lock()
				if fl.file != nil {
					fl.file.Sync() //nolint:errcheck // best-effort periodic sync
				}
				fl.mu.Unlock()
			case <-fl.syncStop:
				fl.syncTicker.Stop()
				return
			}
		}
	}()
}

func (fl *FileLogger) stopSyncLoop() {
	if fl.syncStop != nil {
		close(fl.syncStop)
		fl.syncDone.Wait()
		fl.syncStop = nil
	}
}

// SetSigningKey configures an optional HMAC-SHA256 signing key for audit entries.
// When set, every Log() call produces an HMAC signature in the entry's Signature field.
// Pass nil to disable signing. The key is copied internally; the caller should
// clear their copy after calling.
func (fl *FileLogger) SetSigningKey(key []byte) {
	fl.mu.Lock()
	defer fl.mu.Unlock()
	if key == nil {
		fl.signingKey = nil
		return
	}
	fl.signingKey = make([]byte, len(key))
	copy(fl.signingKey, key)
}

// ClearSigningKey zeroes the signing key in memory to prevent exposure
// in core dumps or debugger inspection.
func (fl *FileLogger) ClearSigningKey() {
	fl.mu.Lock()
	defer fl.mu.Unlock()
	clearBytes(fl.signingKey)
	fl.signingKey = nil
}

func clearBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
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
		return fmt.Errorf("audit: log rotation failed: %w", err)
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
	// Sync-on-every-write is the default (safest). When SyncFlushInterval > 0,
	// a background goroutine handles periodic fsyncs and we skip it here.
	if fl.SyncFlushInterval <= 0 {
		if err := fl.file.Sync(); err != nil {
			return fmt.Errorf("audit: sync entry: %w", err)
		}
	}

	fl.entries = append(fl.entries, entry)

	// Cap the in-memory buffer to prevent unbounded memory growth.
	// When the cap is exceeded, drop the oldest entries (ring-buffer semantics).
	if len(fl.entries) > maxInMemoryEntries {
		excess := len(fl.entries) - maxInMemoryEntries
		fl.entries = fl.entries[excess:]
	}

	// Persist the last hash to a sidecar file so the hash chain survives
	// process restarts. Also store the file size for truncation detection.
	chainPath := chainStatePath(fl.path)
	chainData := fl.lastHash
	// To detect truncation, also store the file size at last write.
	if fi, statErr := fl.file.Stat(); statErr == nil {
		chainData += "\n" + fmt.Sprintf("%d", fi.Size())
	}
	if writeErr := os.WriteFile(chainPath, []byte(chainData+"\n"), 0o600); writeErr != nil {
		slog.Warn("audit: persist chain state failed", "error", writeErr)
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
// Entries are streamed via io.Pipe to avoid buffering the entire set in memory.
func (fl *FileLogger) Export(ctx context.Context, format string) (io.ReadCloser, error) {
	if fl.path == "" {
		return io.NopCloser(strings.NewReader("")), nil
	}

	fl.mu.Lock()
	entries := make([]contracts.AuditEntry, len(fl.entries))
	copy(entries, fl.entries)
	fl.mu.Unlock()

	pr, pw := io.Pipe()

	switch strings.ToLower(format) {
	case "json":
		go func() {
			defer func() {
				if r := recover(); r != nil {
					pw.CloseWithError(fmt.Errorf("audit export JSON panic: %v", r))
				}
			}()
			enc := json.NewEncoder(pw)
			_, _ = pw.Write([]byte("["))
			for i, e := range entries {
				if i > 0 {
					_, _ = pw.Write([]byte(","))
				}
				if err := enc.Encode(e); err != nil {
					pw.CloseWithError(fmt.Errorf("audit: encode entry: %w", err))
					return
				}
			}
			_, _ = pw.Write([]byte("]"))
			_ = pw.Close()
		}()
		return pr, nil
	case "csv":
		go func() {
			defer func() {
				if r := recover(); r != nil {
					pw.CloseWithError(fmt.Errorf("audit export CSV panic: %v", r))
				}
			}()
			_, _ = pw.Write([]byte("id,timestamp,actor,action,resource,resource_id,trace_id,node_id\n"))
			for _, e := range entries {
				line := fmt.Sprintf("%s,%s,%s,%s,%s,%s,%s,%s\n",
					e.ID, e.Timestamp.Format("2006-01-02T15:04:05Z"),
					e.Actor, e.Action, e.Resource, e.ResourceID,
					e.TraceID, e.NodeID)
				if _, err := pw.Write([]byte(line)); err != nil {
					pw.CloseWithError(err)
					return
				}
			}
			_ = pw.Close()
		}()
		return pr, nil
	default:
		_ = pw.Close()
		return nil, fmt.Errorf("audit: unsupported export format: %q", format)
	}
}

// VerifyChainIntegrity performs a standalone integrity check on the audit log
// hash chain within the given time range. Returns a verification result
// indicating whether the chain is intact and, if not, where the breaks occurred.
func (fl *FileLogger) VerifyChainIntegrity(ctx context.Context, from, to time.Time) (contracts.ChainVerificationResult, error) {
	fl.mu.Lock()

	if fl.path == "" {
		fl.mu.Unlock()
		return contracts.ChainVerificationResult{Valid: true}, nil
	}

	entries := make([]contracts.AuditEntry, len(fl.entries))
	copy(entries, fl.entries)
	fl.mu.Unlock()

	result := fl.verifyChain(entries, from, to)
	return result, nil
}

// VerifyAll performs a full integrity check across all surviving rotated audit
// log files and the current active file. Read order is oldest-first so the
// hash chain is verified chronologically, including cross-file boundaries.
// HMAC signatures are verified if a signing key is configured.
//
// Concurrency: the active file is read from disk at call time — entries
// written concurrently are not included. The in-memory buffer (fl.entries)
// may have entries not yet on disk; this is acceptable because the on-disk
// chain is a complete, independently verifiable snapshot.
func (fl *FileLogger) VerifyAll(ctx context.Context) (contracts.ChainVerificationResult, error) {
	if fl.path == "" {
		return contracts.ChainVerificationResult{Valid: true}, nil
	}

	fl.mu.Lock()
	maxRotated := fl.MaxRotatedFiles
	signingKey := fl.signingKey
	fl.mu.Unlock()

	if maxRotated <= 0 {
		maxRotated = defaultMaxRotatedFiles
	}

	// Build the ordered list of files to read: oldest first.
	// After rotation, path.N is the oldest surviving rotated file,
	// path.1 is the newest, and path is the active file.
	var filePaths []string
	for i := maxRotated; i >= 1; i-- {
		p := fmt.Sprintf("%s.%d", fl.path, i)
		if _, err := os.Stat(p); err == nil {
			filePaths = append(filePaths, p)
		}
	}
	// Active file must exist (or we wouldn't be here since fl.path != "").
	// If it was just rotated away, it'll appear as .1 above and a fresh
	// active file will exist.
	filePaths = append(filePaths, fl.path)

	result := contracts.ChainVerificationResult{Valid: true}
	var prevHash string
	h := sha256.New()

	for _, p := range filePaths {
		f, err := os.Open(p) //nolint:gosec // p is from internal rotation naming
		if err != nil {
			continue // skip files that disappeared between stat and open
		}

		scanner := bufio.NewScanner(f)
		// Maximum line length: 10MB. A single audit entry should never
		// exceed this — if it does, something is wrong.
		scanner.Buffer(make([]byte, 64*1024), 10<<20)

		var lineNo int
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}

			var entry contracts.AuditEntry
			if err := json.Unmarshal(line, &entry); err != nil {
				_ = f.Close()
				return result, fmt.Errorf("audit: verify %s:%d: unmarshal: %w", p, lineNo+1, err)
			}

			// Check PrevEntryHash links to previous entry.
			if entry.PrevEntryHash != "" && entry.PrevEntryHash != prevHash {
				result.Valid = false
				result.BrokenLinks = append(result.BrokenLinks, entry.ID)
				if result.FirstBrokenAt.IsZero() {
					result.FirstBrokenAt = entry.Timestamp
				}
			}

			// Verify HMAC signature if signing key is configured.
			if signingKey != nil && entry.Signature != "" {
				entryCopy := entry
				entryCopy.PrevEntryHash = ""
				entryCopy.Signature = ""
				canonData, err := json.Marshal(entryCopy)
				if err != nil {
					_ = f.Close()
					return result, fmt.Errorf("audit: verify %s:%d: marshal for signature: %w", p, lineNo+1, err)
				}
				mac := hmac.New(sha256.New, signingKey)
				mac.Write(canonData)
				expectedSig := hex.EncodeToString(mac.Sum(nil))
				if entry.Signature != expectedSig {
					result.Valid = false
					result.BrokenLinks = append(result.BrokenLinks, entry.ID)
					if result.FirstBrokenAt.IsZero() {
						result.FirstBrokenAt = entry.Timestamp
					}
				}
			}

			// Compute this entry's hash for the chain.
			entryCopy := entry
			entryCopy.PrevEntryHash = ""
			canonData, err := json.Marshal(entryCopy)
			if err != nil {
				_ = f.Close()
				return result, fmt.Errorf("audit: verify %s:%d: marshal for hash: %w", p, lineNo+1, err)
			}
			h.Reset()
			h.Write(canonData)
			prevHash = hex.EncodeToString(h.Sum(nil))

			result.TotalEntries++
			lineNo++
		}

		if err := scanner.Err(); err != nil {
			_ = f.Close()
			return result, fmt.Errorf("audit: verify %s: scan: %w", p, err)
		}
		_ = f.Close()
	}

	if len(result.BrokenLinks) > 0 {
		sort.Strings(result.BrokenLinks)
	}
	return result, nil
}

// StartBackgroundVerification launches a background goroutine that periodically
// verifies the hash chain integrity of recent entries. Results are logged as
// warnings on failure. The goroutine stops when ctx is cancelled.
// Call once after construction; safe for concurrent use.
func (fl *FileLogger) StartBackgroundVerification(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				fl.mu.Lock()
				entryCount := len(fl.entries)
				if entryCount < 2 {
					fl.mu.Unlock()
					continue
				}
				entries := make([]contracts.AuditEntry, entryCount)
				copy(entries, fl.entries)
				fl.mu.Unlock()

				result := fl.verifyChain(entries, time.Time{}, time.Time{})
				if !result.Valid {
					slog.Warn("audit: background chain verification failed",
						"broken_links", len(result.BrokenLinks),
						"total_entries", result.TotalEntries,
						"first_broken", result.FirstBrokenAt,
					)
				}
			}
		}
	}()
}

// verifyChain is the lock-free verification core extracted from
// VerifyChainIntegrity. Caller must ensure entries are stable.
func (fl *FileLogger) verifyChain(entries []contracts.AuditEntry, from, to time.Time) contracts.ChainVerificationResult {
	result := contracts.ChainVerificationResult{Valid: true}

	var inRange []contracts.AuditEntry
	for _, e := range entries {
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
		return result
	}

	h := sha256.New()
	for i := 1; i < len(inRange); i++ {
		prev := inRange[i-1]
		curr := inRange[i]

		prevCopy := prev
		prevCopy.PrevEntryHash = ""
		data, err := json.Marshal(prevCopy)
		if err != nil {
			result.Valid = false
			result.BrokenLinks = append(result.BrokenLinks, curr.ID)
			continue
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

	return result
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
		return nil //nolint:nilerr // no rotation needed when stat fails or size is below threshold
	}

	// Close the current file before renaming.
	_ = fl.file.Close()
	fl.file = nil

	maxRotated := fl.MaxRotatedFiles
	if maxRotated <= 0 {
		maxRotated = defaultMaxRotatedFiles
	}

	// Shift existing rotated files: .N-1 → .N, ..., .1 → .2
	for i := maxRotated; i > 1; i-- {
		older := fmt.Sprintf("%s.%d", fl.path, i)
		newer := fmt.Sprintf("%s.%d", fl.path, i-1)
		if _, err2 := os.Stat(newer); err2 == nil {
			if err3 := os.Rename(newer, older); err3 != nil {
				slog.Error("audit: rename rotated file", "from", newer, "to", older, "error", err3)
			}
		}
	}
	// Rename current file to .1
	if err2 := os.Rename(fl.path, fl.path+".1"); err2 != nil {
		return fmt.Errorf("audit: rename current log for rotation: %w", err2)
	}

	// Open a new file.
	f, err := os.OpenFile(fl.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("audit: open new log after rotation: %w", err)
	}
	fl.file = f
	slog.Info("audit: log rotated", "path", fl.path, "max_mb", fl.MaxSizeMB)
	return nil
}

func (fl *FileLogger) Close() error {
	// Stop the sync goroutine before acquiring the lock to avoid a deadlock
	// where the goroutine holds the lock (inside a tick) while Close waits
	// for it, and Close holds the lock while the goroutine waits for it.
	if fl.syncStop != nil {
		fl.closeSyncOnce.Do(func() {
			close(fl.syncStop)
		})
		fl.syncDone.Wait()
	}

	fl.mu.Lock()
	defer fl.mu.Unlock()

	if fl.file != nil {
		// Final sync: flush any buffered writes before closing.
		fl.file.Sync() //nolint:errcheck // best-effort
		err := fl.file.Close()
		fl.file = nil
		return err
	}
	return nil
}
