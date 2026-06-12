package deadletter

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
	defaultFilePerm = 0600
	defaultDirPerm  = 0700
	maxFileEntries  = 10000
)

type storedEntry struct {
	contracts.DeadLetterEntry
}

// Store implements contracts.DeadLetterProvider with in-memory storage
// and optional file persistence.
type Store struct {
	mu      sync.RWMutex
	entries map[string]*storedEntry // indexed by event ID
	dir     string
}

// New creates an in-memory dead letter store.
func New() *Store {
	return &Store{
		entries: make(map[string]*storedEntry),
	}
}

// NewWithDir creates a file-backed dead letter store. Existing entries
// are loaded from the directory on creation.
func NewWithDir(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, defaultDirPerm); err != nil {
		return nil, fmt.Errorf("deadletter: create dir: %w", err)
	}
	s := &Store{
		entries: make(map[string]*storedEntry),
		dir:     dir,
	}
	if err := s.load(); err != nil {
		slog.Warn("deadletter: load persisted entries", "error", err)
	}
	return s, nil
}

// Store implements contracts.DeadLetterProvider.
func (s *Store) Store(_ context.Context, event contracts.Event, handlerName string, err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Enforce max entry cap to prevent unbounded map growth (DoS via
	// repeated handler failures). Discards the oldest entry when full.
	if len(s.entries) >= maxFileEntries {
		for id := range s.entries {
			delete(s.entries, id)
			break
		}
	}

	id := event.ID
	if id == "" {
		id = fmt.Sprintf("dl-%d", time.Now().UnixNano())
	}

	errStr := "unknown error"
	if err != nil {
		errStr = err.Error()
	}
	entry := &storedEntry{
		DeadLetterEntry: contracts.DeadLetterEntry{
			Event:       event,
			HandlerName: handlerName,
			Error:       errStr,
			FailedAt:    time.Now(),
		},
	}
	s.entries[id] = entry
	s.persist(entry)
	return nil
}

// Replay implements contracts.DeadLetterProvider.
func (s *Store) Replay(_ context.Context, handlerName string, since time.Time) ([]contracts.DeadLetterEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []contracts.DeadLetterEntry
	for _, e := range s.entries {
		if handlerName != "" && e.HandlerName != handlerName {
			continue
		}
		if !since.IsZero() && e.FailedAt.Before(since) {
			continue
		}
		result = append(result, e.DeadLetterEntry)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].FailedAt.Before(result[j].FailedAt)
	})
	return result, nil
}

// Discard implements contracts.DeadLetterProvider.
func (s *Store) Discard(_ context.Context, eventID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.entries[eventID]; !ok {
		return fmt.Errorf("deadletter: event %q not found", eventID)
	}
	delete(s.entries, eventID)
	s.removeFile(eventID)
	return nil
}

// Count returns the number of stored dead letter entries.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

// --- file persistence ---

func (s *Store) safeFilename(id string) string {
	r := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_",
		" ", "_", "..", "_", "\x00", "",
	)
	return r.Replace(id) + ".json"
}

func (s *Store) entryPath(id string) string {
	return filepath.Join(s.dir, s.safeFilename(id))
}

func (s *Store) persist(entry *storedEntry) {
	if s.dir == "" {
		return
	}
	id := entry.Event.ID
	if id == "" {
		return
	}
	data, err := json.Marshal(entry)
	if err != nil {
		slog.Warn("deadletter: marshal entry", "event_id", id, "error", err)
		return
	}
	if err := os.WriteFile(s.entryPath(id), data, defaultFilePerm); err != nil {
		slog.Warn("deadletter: write entry", "event_id", id, "error", err)
	}
}

func (s *Store) removeFile(id string) {
	if s.dir == "" {
		return
	}
	if err := os.Remove(s.entryPath(id)); err != nil && !os.IsNotExist(err) {
		slog.Warn("deadletter: remove entry", "event_id", id, "error", err)
	}
}

func (s *Store) load() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	for _, de := range entries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, de.Name()))
		if err != nil {
			slog.Warn("deadletter: read file", "file", de.Name(), "error", err)
			continue
		}
		var entry storedEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			slog.Warn("deadletter: unmarshal entry", "file", de.Name(), "error", err)
			continue
		}
		id := entry.Event.ID
		if id == "" {
			id = strings.TrimSuffix(de.Name(), ".json")
		}
		s.entries[id] = &entry
	}
	return nil
}

// deadletterModule wraps a Store to satisfy contracts.Module for registry registration.
type deadletterModule struct {
	store  *Store
	nodeID string
}

func (m *deadletterModule) Info() contracts.ModuleInfo {
	return contracts.ModuleInfo{
		ID:           "core.deadletter",
		Name:         "core-deadletter",
		Version:      "1.0.0",
		Description:  "Core file-backed dead letter store for failed event inspection and replay",
		Capabilities: []string{contracts.CapabilityDeadLetter},
	}
}

func (m *deadletterModule) Init(_ context.Context) error   { return nil }
func (m *deadletterModule) Start(_ context.Context) error  { return nil }
func (m *deadletterModule) Stop(_ context.Context) error   { return nil }
func (m *deadletterModule) Health(_ context.Context) error { return nil }

// RegisterSelf creates a virtual module entry so this store is discoverable
// via FindByCapability("deadletter").
func RegisterSelf(reg *registry.Registry, store *Store, nodeID string) {
	m := &deadletterModule{store: store, nodeID: nodeID}
	if err := reg.Register(m, nil); err != nil && !strings.Contains(err.Error(), "already registered") {
		slog.Warn("deadletter: register self", "error", err)
	}
}
