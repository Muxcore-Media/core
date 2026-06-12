//nolint:govet // struct field alignment
package events

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Muxcore-Media/core/internal/trace"
	"github.com/Muxcore-Media/core/pkg/contracts"
	"golang.org/x/sys/unix"
)

const (
	// walSegmentSize is the maximum size of a WAL segment file before rotation.
	walSegmentSize = 32 * 1024 * 1024 // 32 MB

	// walDiskFreeMinBytes is the minimum free disk space before WAL writes are
	// suspended. When available space drops below this threshold, writes are
	// dropped and an error-level log is emitted.
	walDiskFreeMinBytes = 100 * 1024 * 1024 // 100 MB

	// walMaxSegments is the maximum number of WAL segment files to retain.
	// At 32MB per segment, 100 segments = 3.2GB maximum WAL disk usage.
	// When exceeded, the oldest segments are pruned regardless of subscriber
	// sequence progress to prevent unbounded disk growth.
	walMaxSegments = 100
)

// WALSegment is a single WAL journal file on disk.
type WALSegment struct {
	Path      string
	FirstSeq  uint64
	LastSeq   uint64
	Size      int64
	CreatedAt time.Time
}

// WALWriter provides a write-ahead log for the event bus.
// Events are appended as JSONL with a monotonically increasing sequence
// number. Segments are rotated when they exceed walSegmentSize.
// Old segments are pruned when all active subscribers have passed their
// sequence numbers.
type WALWriter struct {
	mu          sync.Mutex
	dir         string
	file        *os.File
	writer      *bufio.Writer
	currentSize int64
	seq         uint64
	segments    []WALSegment
	// minSubscriberSeq is the lowest sequence number among all subscribers
	// that use catch-up (SubscribeFrom). Segments wholly below this can
	// be pruned.
	minSubscriberSeq uint64
}

// walEntry is the on-disk format for a WAL event.
type walEntry struct {
	Seq       uint64          `json:"seq"`
	Timestamp time.Time       `json:"timestamp"`
	Event     contracts.Event `json:"event"`
}

// NewWALWriter opens or creates a WAL in the given directory.
// If the directory doesn't exist, it is created.
func NewWALWriter(dir string) (*WALWriter, error) {
	// 0700: WAL contains event payloads which may be sensitive — owner-only.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("wal: create directory %s: %w", dir, err)
	}

	w := &WALWriter{
		dir: dir,
	}

	// Discover existing segments and find the highest sequence number.
	if err := w.discoverSegments(); err != nil {
		return nil, fmt.Errorf("wal: discover segments: %w", err)
	}

	// Open the current (last) segment for appending, or create a new one.
	if err := w.openCurrentSegment(); err != nil {
		return nil, fmt.Errorf("wal: open segment: %w", err)
	}

	slog.Info("WAL ready",
		"dir", dir,
		"segments", len(w.segments),
		"next_seq", w.seq+1,
	)

	return w, nil
}

// Write appends an event to the WAL. Thread-safe.
// Returns the assigned sequence number.
func (w *WALWriter) Write(event contracts.Event) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.seq++
	entry := walEntry{
		Seq:       w.seq,
		Timestamp: time.Now(),
		Event:     event,
	}

	// Check disk space before writing to avoid silent data loss when full.
	if free, err := w.diskFree(); err == nil && free < walDiskFreeMinBytes {
		slog.Error("wal: disk critically low — dropping event to prevent filesystem full",
			"free_bytes", free,
			"min_bytes", walDiskFreeMinBytes,
			"dir", w.dir,
		)
		return 0, fmt.Errorf("wal: disk free space %d bytes below minimum %d", free, walDiskFreeMinBytes)
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return 0, fmt.Errorf("wal: marshal entry %d: %w", w.seq, err)
	}
	data = append(data, '\n')

	n, err := w.writer.Write(data)
	if err != nil {
		return 0, fmt.Errorf("wal: write entry %d: %w", w.seq, err)
	}
	w.currentSize += int64(n)
	if len(w.segments) > 0 {
		w.segments[len(w.segments)-1].LastSeq = w.seq
	}

	// Rotate if segment exceeds the size limit.
	if w.currentSize >= walSegmentSize {
		if err := w.rotateLocked(); err != nil {
			slog.Error("wal: segment rotation failed", "error", err)
			// Continue with current segment — better than losing events.
		}
	}

	return w.seq, nil
}

// Flush ensures all buffered writes are persisted to disk and fsynced.
func (w *WALWriter) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.writer.Flush(); err != nil {
		return err
	}
	return w.file.Sync()
}

// Sync performs an fsync on the underlying file. Call after Flush to ensure
// data is durably on disk. Use FlushSync for a combined flush+sync.
func (w *WALWriter) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Sync()
}

// Close flushes, syncs, and closes the WAL.
func (w *WALWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.writer.Flush(); err != nil {
		return err
	}
	if err := w.file.Sync(); err != nil {
		return err
	}
	return w.file.Close()
}

// LastSeq returns the highest sequence number written.
func (w *WALWriter) LastSeq() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.seq
}

// Segments returns metadata for all WAL segments.
func (w *WALWriter) Segments() []WALSegment {
	w.mu.Lock()
	defer w.mu.Unlock()
	segs := make([]WALSegment, len(w.segments))
	copy(segs, w.segments)
	return segs
}

// ReplayFrom reads events from the WAL starting at the given sequence number
// and invokes the callback for each event. If sinceSeq is 0, replays from
// the beginning. The callback is called synchronously; it should not block.
func (w *WALWriter) ReplayFrom(ctx context.Context, sinceSeq uint64, fn func(contracts.Event) error) error {
	w.mu.Lock()
	// Flush any buffered writes so the latest events are visible on disk.
	if err := w.writer.Flush(); err != nil {
		slog.Warn("wal: flush before replay", "error", err)
	}
	segments := make([]WALSegment, len(w.segments))
	copy(segments, w.segments)
	w.mu.Unlock()

	for _, seg := range segments {
		if seg.LastSeq < sinceSeq {
			continue
		}
		if err := w.replaySegment(ctx, seg.Path, sinceSeq, fn); err != nil {
			return fmt.Errorf("wal: replay segment %s: %w", seg.Path, err)
		}
	}
	return nil
}

// SetMinSubscriberSeq informs the WAL of the lowest subscriber sequence
// number. Segments with LastSeq < minSeq can be pruned.
func (w *WALWriter) SetMinSubscriberSeq(seq uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.minSubscriberSeq = seq
	w.pruneSegmentsLocked()
}

// --- Internal ---

func (w *WALWriter) segmentPath(firstSeq uint64) string {
	return filepath.Join(w.dir, fmt.Sprintf("events-%020d.wal", firstSeq))
}

func (w *WALWriter) discoverSegments() error {
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "events-") || !strings.HasSuffix(e.Name(), ".wal") {
			continue
		}
		// Parse first sequence number from filename.
		seqStr := strings.TrimPrefix(e.Name(), "events-")
		seqStr = strings.TrimSuffix(seqStr, ".wal")
		firstSeq, err := strconv.ParseUint(seqStr, 10, 64)
		if err != nil {
			slog.Warn("wal: skipping unparseable segment filename", "name", e.Name())
			continue
		}

		info, err := e.Info()
		if err != nil {
			continue
		}

		seg := WALSegment{
			Path:      filepath.Join(w.dir, e.Name()),
			FirstSeq:  firstSeq,
			Size:      info.Size(),
			CreatedAt: info.ModTime(),
		}

		// Scan the last entry in this segment to get LastSeq.
		lastSeq, err := w.scanLastSeq(seg.Path)
		if err != nil {
			slog.Warn("wal: cannot scan segment for last seq", "path", seg.Path, "error", err)
			continue
		}
		seg.LastSeq = lastSeq

		w.segments = append(w.segments, seg)
		if lastSeq > w.seq {
			w.seq = lastSeq
		}
	}

	return nil
}

func (w *WALWriter) scanLastSeq(path string) (uint64, error) {
	f, err := os.Open(path) //nolint:gosec // path is constructed internally from WAL dir
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()

	var lastSeq uint64
	var skipped int
	scanner := bufio.NewScanner(f)
	// Increase buffer for large event payloads.
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		var entry walEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			skipped++
			continue // skip corrupted lines
		}
		lastSeq = entry.Seq
	}
	if skipped > 0 {
		slog.Warn("wal: skipped corrupted lines during startup scan",
			"segment", filepath.Base(path),
			"skipped_lines", skipped,
		)
	}
	return lastSeq, scanner.Err()
}

// diskFree returns the number of free bytes available on the filesystem
// containing the WAL directory. Supported on all unix-like platforms;
// returns 0 on unsupported platforms (disk check is best-effort).
func (w *WALWriter) diskFree() (uint64, error) {
	if runtime.GOOS == "windows" {
		return 0, nil
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(w.dir, &stat); err != nil {
		return 0, err
	}
	if stat.Bsize <= 0 {
		return 0, fmt.Errorf("wal: unexpected block size %d", stat.Bsize)
	}
	// Bsize is int32 on some platforms (darwin, freebsd), int64 on others (linux).
	bsize := uint64(stat.Bsize)
	return stat.Bavail * bsize, nil
}

func (w *WALWriter) openCurrentSegment() error {
	path := w.segmentPath(w.seq + 1)

	// If there's an existing file for the next expected segment, append to it.
	if _, err := os.Stat(path); err == nil {
		return w.openSegment(path)
	}

	// Create a new segment. 0600: owner-only, matches WAL directory permissions.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // path is internally constructed from WAL dir
	if err != nil {
		return fmt.Errorf("create segment %s: %w", path, err)
	}
	w.file = f
	w.writer = bufio.NewWriterSize(f, 256*1024) // 256KB buffer
	w.currentSize = 0

	seg := WALSegment{
		Path:      path,
		FirstSeq:  w.seq + 1,
		CreatedAt: time.Now(),
	}
	w.segments = append(w.segments, seg)

	return nil
}

func (w *WALWriter) openSegment(path string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // path is internally constructed from WAL dir
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	w.file = f
	w.writer = bufio.NewWriterSize(f, 256*1024)
	w.currentSize = info.Size()
	return nil
}

func (w *WALWriter) rotateLocked() error {
	if err := w.writer.Flush(); err != nil {
		return err
	}
	if err := w.file.Sync(); err != nil {
		return err
	}
	if err := w.file.Close(); err != nil {
		return err
	}

	// Update the last segment's LastSeq.
	if len(w.segments) > 0 {
		w.segments[len(w.segments)-1].LastSeq = w.seq
	}

	return w.openCurrentSegment()
}

func (w *WALWriter) replaySegment(ctx context.Context, path string, sinceSeq uint64, fn func(contracts.Event) error) error {
	f, err := os.Open(path) //nolint:gosec // path is internally constructed from WAL dir
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		var entry walEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		if entry.Seq < sinceSeq {
			continue
		}
		if err := fn(entry.Event); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (w *WALWriter) pruneSegmentsLocked() {
	// Always enforce the hard cap on segment count to prevent unbounded
	// disk growth, regardless of subscriber progress.
	if len(w.segments) > walMaxSegments {
		excess := len(w.segments) - walMaxSegments
		for i := 0; i < excess && i < len(w.segments); i++ {
			path := w.segments[i].Path
			slog.Warn("wal: pruning segment beyond max limit",
				"path", path,
				"max_segments", walMaxSegments,
			)
			if err := os.Remove(path); err != nil {
				slog.Warn("wal: failed to prune segment", "path", path, "error", err)
			}
		}
		w.segments = w.segments[excess:]
	}

	if w.minSubscriberSeq == 0 {
		return
	}

	var keep []WALSegment
	for _, seg := range w.segments {
		if seg.LastSeq < w.minSubscriberSeq {
			slog.Info("wal: pruning segment",
				"path", seg.Path,
				"first_seq", seg.FirstSeq,
				"last_seq", seg.LastSeq,
				"min_subscriber_seq", w.minSubscriberSeq,
			)
			if err := os.Remove(seg.Path); err != nil {
				slog.Warn("wal: failed to prune segment", "path", seg.Path, "error", err)
				keep = append(keep, seg) // keep if we can't remove
				continue
			}
			continue
		}
		keep = append(keep, seg)
	}
	w.segments = keep
}

// --- MemoryBus WAL integration ---

// EnableWAL attaches a WAL writer to the event bus. All published events
// are persisted to the WAL before being dispatched to subscribers.
// The WAL is closed automatically when bus.Close() is called.
func (b *MemoryBus) EnableWAL(dir string) error {
	w, err := NewWALWriter(dir)
	if err != nil {
		return err
	}

	b.mu.Lock()
	b.wal = w
	// Default minimum subscriber sequence: don't prune anything initially.
	b.wal.SetMinSubscriberSeq(0)
	b.mu.Unlock()

	// Optionally skip replay on startup.
	if os.Getenv("MUXCORE_EVENT_REPLAY") != "false" && os.Getenv("MUXCORE_EVENT_REPLAY") != "0" {
		slog.Info("WAL replay enabled — events will be replayed to late subscribers")
	}

	return nil
}

// CloseWAL flushes and closes the WAL. Safe to call on a bus without WAL.
func (b *MemoryBus) CloseWAL() error {
	b.mu.RLock()
	w := b.wal
	b.mu.RUnlock()
	if w == nil {
		return nil
	}
	return w.Close()
}

// ReplayFrom replays persisted events starting at sinceSeq (inclusive),
// calling fn for each. Returns an error if no WAL is configured.
// Satisfies the grpcmesh.WALReplayer interface.
func (b *MemoryBus) ReplayFrom(ctx context.Context, sinceSeq uint64, fn func(contracts.Event) error) error {
	b.mu.RLock()
	w := b.wal
	b.mu.RUnlock()
	if w == nil {
		return fmt.Errorf("WAL not configured: set MUXCORE_EVENT_JOURNAL_PATH to enable event replay")
	}
	return w.ReplayFrom(ctx, sinceSeq, fn)
}

// SubscribeFrom subscribes to events of the given type and replays any
// events with sequence numbers >= sinceSeq before beginning live dispatch.
// sinceSeq=0 means "replay all available events."
func (b *MemoryBus) SubscribeFrom(ctx context.Context, eventType string, handler contracts.EventHandler, sinceSeq uint64) (func(), error) {
	b.mu.RLock()
	wal := b.wal
	b.mu.RUnlock()

	// Replay historical events if WAL is available.
	if wal != nil && sinceSeq <= wal.LastSeq() {
		replayTimeout := b.WALReplayTimeout
		if replayTimeout <= 0 {
			replayTimeout = 30 * time.Second
		}
		replayCtx, cancel := context.WithTimeout(ctx, replayTimeout)
		defer cancel()

		replayed := uint64(0)
		if err := wal.ReplayFrom(replayCtx, sinceSeq, func(event contracts.Event) error {
			if event.Type == eventType || eventType == "*" {
				handlerCtx, handlerCancel := context.WithTimeout(ctx, defaultHandlerTimeout)
				defer handlerCancel()
				if event.TraceID != "" {
					handlerCtx = trace.WithTraceID(handlerCtx, event.TraceID)
				}
				if err := handler(handlerCtx, event); err != nil {
					slog.Error("wal replay handler error", "event_type", event.Type, "error", err)
				}
				replayed++
			}
			return nil
		}); err != nil {
			slog.Warn("WAL replay interrupted", "event_type", eventType, "error", err)
		}
		if replayed > 0 {
			slog.Info("WAL replay complete", "event_type", eventType, "replayed", replayed)
		}
	}

	// Subscribe for live events.
	cancel, err := b.Subscribe(ctx, eventType, handler)
	return cancel, err
}

// UpdateMinSubscriberSeq updates the WAL's minimum subscriber sequence
// for pruning purposes. Call this periodically or when subscribers advance.
func (b *MemoryBus) UpdateMinSubscriberSeq(seq uint64) {
	b.mu.RLock()
	wal := b.wal
	b.mu.RUnlock()
	if wal != nil {
		wal.SetMinSubscriberSeq(seq)
	}
}
