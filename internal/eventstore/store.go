//nolint:govet // struct field alignment
package eventstore

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/pkg/contracts"
)

const (
	defaultFilePerm = 0o600
	defaultDirPerm  = 0o700
	subscribePoll   = 500 * time.Millisecond
	seqBits         = 20
)

type streamState struct {
	mu      sync.Mutex
	seq     int64 // current highest sequence, 0 = empty
	dir     string
	entries []*contracts.EventStoreEntry // in-memory cache for all stores
}

// Store implements contracts.EventStore with per-stream file-backed storage.
// Each stream is a directory; events are individual numbered JSON files.
type Store struct {
	baseMu  sync.RWMutex
	streams map[string]*streamState
	baseDir string
}

// New creates an in-memory EventStore with no persistence.
func New() *Store {
	return &Store{
		streams: make(map[string]*streamState),
	}
}

// NewWithDir creates a file-backed EventStore. Existing streams are
// discovered on startup by scanning the directory.
func NewWithDir(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, defaultDirPerm); err != nil {
		return nil, fmt.Errorf("eventstore: create dir: %w", err)
	}
	s := &Store{
		streams: make(map[string]*streamState),
		baseDir: dir,
	}
	if err := s.discover(); err != nil {
		slog.Warn("eventstore: discover streams", "error", err)
	}
	return s, nil
}

func (s *Store) discover() error {
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return err
	}
	for _, de := range entries {
		if !de.IsDir() {
			continue
		}
		streamName := de.Name()
		ss, err := s.loadStream(streamName)
		if err != nil {
			slog.Warn("eventstore: load stream", "stream", streamName, "error", err)
			continue
		}
		s.streams[s.safeStreamKey(streamName)] = ss
	}
	return nil
}

func (s *Store) loadStream(name string) (*streamState, error) {
	dir := filepath.Join(s.baseDir, s.safeStreamKey(name))
	fileEntries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	ss := &streamState{dir: dir}
	for _, de := range fileEntries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, de.Name())) //nolint:gosec // entries are files we wrote
		if err != nil {
			slog.Warn("eventstore: read entry", "file", de.Name(), "error", err)
			continue
		}
		var entry contracts.EventStoreEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			slog.Warn("eventstore: unmarshal entry", "file", de.Name(), "error", err)
			continue
		}
		if entry.Sequence > ss.seq {
			ss.seq = entry.Sequence
		}
		ss.entries = append(ss.entries, &entry)
	}
	// Sort entries by sequence
	sort.Slice(ss.entries, func(i, j int) bool {
		return ss.entries[i].Sequence < ss.entries[j].Sequence
	})
	return ss, nil
}

func (s *Store) getOrCreateStream(name string) *streamState {
	s.baseMu.Lock()
	defer s.baseMu.Unlock()

	key := s.safeStreamKey(name)
	if ss, ok := s.streams[key]; ok {
		return ss
	}
	dir := ""
	if s.baseDir != "" {
		dir = filepath.Join(s.baseDir, key)
	}
	ss := &streamState{dir: dir}
	s.streams[key] = ss
	return ss
}

func (s *Store) safeStreamKey(name string) string {
	r := strings.NewReplacer(
		"/", "_", "\\", "_", "..", "_",
		"\x00", "", ":", "_",
	)
	return r.Replace(name)
}

func seqFilename(seq int64) string {
	return fmt.Sprintf("%020d.json", seq)
}

// Append implements contracts.EventStore.
func (s *Store) Append(_ context.Context, stream string, events []contracts.Event) (int64, error) {
	if len(events) == 0 {
		return 0, fmt.Errorf("eventstore: cannot append empty batch")
	}

	ss := s.getOrCreateStream(stream)
	ss.mu.Lock()
	defer ss.mu.Unlock()

	startSeq := ss.seq + 1
	for i, evt := range events {
		seq := startSeq + int64(i)
		entry := contracts.EventStoreEntry{
			Stream:   stream,
			Sequence: seq,
			Event:    evt,
			StoredAt: time.Now(),
		}
		if err := s.writeEntry(ss, &entry); err != nil {
			// Rollback: remove written files up to this point
			for j := 0; j < i; j++ {
				s.removeFile(ss, startSeq+int64(j))
			}
			return 0, fmt.Errorf("eventstore: append event %d: %w", seq, err)
		}
	}
	ss.seq = startSeq + int64(len(events)) - 1
	return startSeq, nil
}

func (s *Store) writeEntry(ss *streamState, entry *contracts.EventStoreEntry) error {
	ss.entries = append(ss.entries, entry)
	if ss.dir == "" {
		return nil // in-memory only
	}
	if err := os.MkdirAll(ss.dir, defaultDirPerm); err != nil {
		return err
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	path := filepath.Join(ss.dir, seqFilename(entry.Sequence))
	return os.WriteFile(path, data, defaultFilePerm)
}

func (s *Store) removeFile(ss *streamState, seq int64) {
	// Remove from cache
	for i, e := range ss.entries {
		if e.Sequence == seq {
			ss.entries = append(ss.entries[:i], ss.entries[i+1:]...)
			break
		}
	}
	if ss.dir == "" {
		return
	}
	_ = os.Remove(filepath.Join(ss.dir, seqFilename(seq)))
}

// Read implements contracts.EventStore.
func (s *Store) Read(_ context.Context, stream string, fromSequence int64, limit int) ([]contracts.EventStoreEntry, error) {
	ss := s.getOrCreateStream(stream)
	ss.mu.Lock()
	defer ss.mu.Unlock()

	if fromSequence < 1 {
		fromSequence = 1
	}
	if limit <= 0 {
		limit = int(ss.seq - fromSequence + 1)
		if limit < 0 {
			return []contracts.EventStoreEntry{}, nil
		}
	}

	var result []contracts.EventStoreEntry
	for seq := fromSequence; seq <= ss.seq && len(result) < limit; seq++ {
		entry, err := s.readEntry(ss, seq)
		if err != nil {
			continue // skip gaps
		}
		result = append(result, *entry)
	}
	if result == nil {
		return []contracts.EventStoreEntry{}, nil
	}
	return result, nil
}

func (s *Store) readEntry(ss *streamState, seq int64) (*contracts.EventStoreEntry, error) {
	// Check in-memory cache first
	for _, e := range ss.entries {
		if e.Sequence == seq {
			return e, nil
		}
	}
	// Fall back to file read
	if ss.dir != "" {
		data, err := os.ReadFile(filepath.Join(ss.dir, seqFilename(seq)))
		if err != nil {
			return nil, fmt.Errorf("eventstore: read entry seq %d: %w", seq, err)
		}
		var entry contracts.EventStoreEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			return nil, fmt.Errorf("eventstore: unmarshal entry seq %d: %w", seq, err)
		}
		return &entry, nil
	}
	return nil, fmt.Errorf("eventstore: sequence %d not found in stream", seq)
}

// Subscribe implements contracts.EventStore.
func (s *Store) Subscribe(ctx context.Context, stream string, fromSequence int64) (<-chan contracts.EventStoreEntry, error) {
	ss := s.getOrCreateStream(stream)
	ch := make(chan contracts.EventStoreEntry, 64)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("eventstore subscribe panic recovered", "stream", stream, "panic", r)
			}
		}()
		defer close(ch)

		// Catch-up phase: deliver existing events from fromSequence
		if fromSequence <= ss.seq {
			entries, err := s.Read(ctx, stream, fromSequence, 0)
			if err == nil {
				for _, e := range entries {
					select {
					case ch <- e:
					case <-ctx.Done():
						return
					}
				}
			}
		}

		// Live phase: poll for new events
		nextSeq := ss.seq + 1
		ticker := time.NewTicker(subscribePoll)
		defer ticker.Stop()

		for {
			// Re-acquire the latest sequence
			ss.mu.Lock()
			currentSeq := ss.seq
			ss.mu.Unlock()

			for seq := nextSeq; seq <= currentSeq; seq++ {
				entry, err := s.readEntry(ss, seq)
				if err != nil {
					continue
				}
				select {
				case ch <- *entry:
				case <-ctx.Done():
					return
				}
				nextSeq = seq + 1
			}

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	return ch, nil
}

// Streams implements contracts.EventStore.
func (s *Store) Streams(_ context.Context) ([]string, error) {
	s.baseMu.RLock()
	defer s.baseMu.RUnlock()

	names := make([]string, 0, len(s.streams))
	for key := range s.streams {
		names = append(names, key)
	}
	sort.Strings(names)
	return names, nil
}

// eventstoreModule wraps a Store to satisfy contracts.Module for registry registration.
type eventstoreModule struct {
	store  *Store
	nodeID string
}

func (m *eventstoreModule) Info() contracts.ModuleInfo {
	return contracts.ModuleInfo{
		ID:           "core.eventstore",
		Name:         "core-eventstore",
		Version:      "1.0.0",
		Description:  "Core file-backed event store for stream-based event sourcing and replay",
		Capabilities: []string{contracts.CapabilityEventStore},
	}
}

func (m *eventstoreModule) Init(_ context.Context) error   { return nil }
func (m *eventstoreModule) Start(_ context.Context) error  { return nil }
func (m *eventstoreModule) Stop(_ context.Context) error   { return nil }
func (m *eventstoreModule) Health(_ context.Context) error { return nil }

// RegisterSelf creates a virtual module entry so this store is discoverable
// via FindByCapability("event.store").
func RegisterSelf(reg *registry.Registry, store *Store, nodeID string) {
	if reg == nil {
		slog.Warn("eventstore: RegisterSelf called with nil registry")
		return
	}
	m := &eventstoreModule{store: store, nodeID: nodeID}
	if err := reg.Register(m, nil); err != nil && !strings.Contains(err.Error(), "already registered") {
		slog.Warn("eventstore: register self", "error", err)
	}
}
