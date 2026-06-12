package idempotency

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/pkg/contracts"
)

const (
	defaultCleanupInterval = 5 * time.Minute
	defaultFilePerm        = 0o600
	defaultDirPerm         = 0o700
)

type entry struct {
	Key      string        `json:"key"`
	Result   []byte        `json:"result,omitempty"`
	StoredAt time.Time     `json:"stored_at"`
	TTL      time.Duration `json:"ttl,omitempty"`
}

func (e *entry) expired() bool {
	if e.TTL <= 0 {
		return false
	}
	return time.Since(e.StoredAt) > e.TTL
}

// Store implements contracts.IdempotencyProvider with in-memory storage
// and optional file persistence.
type Store struct {
	mu           sync.RWMutex
	entries      map[string]*entry
	dir          string // optional: when set, entries are persisted as JSON files
	reaperCancel context.CancelFunc
}

// New creates an in-memory idempotency store.
func New() *Store {
	return &Store{
		entries: make(map[string]*entry),
	}
}

// NewWithDir creates a file-backed idempotency store. Entries are
// persisted as individual JSON files under dir.
func NewWithDir(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, defaultDirPerm); err != nil {
		return nil, fmt.Errorf("idempotency: create dir: %w", err)
	}
	s := &Store{
		entries: make(map[string]*entry),
		dir:     dir,
	}
	if err := s.load(); err != nil {
		slog.Warn("idempotency: load persisted entries", "error", err)
	}
	return s, nil
}

// Start launches a background goroutine that periodically evicts
// expired entries. Safe to call multiple times — subsequent calls
// are no-ops.
func (s *Store) Start(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reaperCancel != nil {
		return
	}
	reaperCtx, cancel := context.WithCancel(ctx)
	s.reaperCancel = cancel
	go s.reaperLoop(reaperCtx)
	slog.Info("idempotency: reaper started", "interval", defaultCleanupInterval)
}

// Stop signals the reaper goroutine to shut down.
func (s *Store) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reaperCancel != nil {
		s.reaperCancel()
		s.reaperCancel = nil
	}
}

func (s *Store) reaperLoop(ctx context.Context) {
	ticker := time.NewTicker(defaultCleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.reap()
		}
	}
}

func (s *Store) reap() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, e := range s.entries {
		if e.expired() {
			delete(s.entries, key)
			s.removeFile(key)
		}
	}
}

// Store implements contracts.IdempotencyProvider.
func (s *Store) Store(_ context.Context, key string, result []byte, ttl time.Duration) (bool, error) {
	if key == "" {
		return false, fmt.Errorf("idempotency: key is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.entries[key]; ok && !existing.expired() {
		return false, nil
	}

	e := &entry{
		Key:      key,
		Result:   result,
		StoredAt: time.Now(),
		TTL:      ttl,
	}
	s.entries[key] = e
	s.persistFile(e)
	return true, nil
}

// Exists implements contracts.IdempotencyProvider.
func (s *Store) Exists(_ context.Context, key string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	e, ok := s.entries[key]
	if !ok || e.expired() {
		return false, nil
	}
	return true, nil
}

// Result implements contracts.IdempotencyProvider.
func (s *Store) Result(_ context.Context, key string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	e, ok := s.entries[key]
	if !ok || e.expired() {
		return nil, nil
	}
	return e.Result, nil
}

// Count returns the number of stored entries (including expired ones
// that haven't been reaped yet).
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

// --- file persistence ---

func (s *Store) safeFilename(key string) string {
	r := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_",
		" ", "_", "..", "_", "\x00", "",
	)
	return r.Replace(key) + ".json"
}

func (s *Store) entryPath(key string) string {
	return filepath.Join(s.dir, s.safeFilename(key))
}

func (s *Store) persistFile(e *entry) {
	if s.dir == "" {
		return
	}
	data, err := json.Marshal(e)
	if err != nil {
		slog.Warn("idempotency: marshal entry", "key", e.Key, "error", err)
		return
	}
	if err := os.WriteFile(s.entryPath(e.Key), data, defaultFilePerm); err != nil {
		slog.Warn("idempotency: write entry", "key", e.Key, "error", err)
	}
}

func (s *Store) removeFile(key string) {
	if s.dir == "" {
		return
	}
	if err := os.Remove(s.entryPath(key)); err != nil && !os.IsNotExist(err) {
		slog.Warn("idempotency: remove entry", "key", key, "error", err)
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
			slog.Warn("idempotency: read file", "file", de.Name(), "error", err)
			continue
		}
		var e entry
		if err := json.Unmarshal(data, &e); err != nil {
			slog.Warn("idempotency: unmarshal entry", "file", de.Name(), "error", err)
			continue
		}
		if !e.expired() {
			s.entries[e.Key] = &e
		} else {
			if err := os.Remove(filepath.Join(s.dir, de.Name())); err != nil {
				slog.Warn("idempotency: remove expired entry", "file", de.Name(), "error", err)
			}
		}
	}
	return nil
}

// idempotencyModule wraps a Store to satisfy contracts.Module for
// registry registration.
type idempotencyModule struct {
	store  *Store
	nodeID string
}

func (m *idempotencyModule) Info() contracts.ModuleInfo {
	return contracts.ModuleInfo{
		ID:           "core.idempotency",
		Name:         "core-idempotency",
		Version:      "1.0.0",
		Description:  "Core in-memory idempotency store for duplicate task prevention",
		Capabilities: []string{contracts.CapabilityIdempotency},
	}
}

func (m *idempotencyModule) Init(_ context.Context) error   { return nil }
func (m *idempotencyModule) Start(_ context.Context) error  { return nil }
func (m *idempotencyModule) Stop(_ context.Context) error   { return nil }
func (m *idempotencyModule) Health(_ context.Context) error { return nil }

// RegisterSelf creates a virtual module entry in the given Registry so
// that this idempotency store is discoverable via
// FindByCapability("idempotency").
func RegisterSelf(reg *registry.Registry, store *Store, nodeID string) {
	m := &idempotencyModule{store: store, nodeID: nodeID}
	if err := reg.Register(m, nil); err != nil && !strings.Contains(err.Error(), "already registered") {
		slog.Warn("idempotency: register self", "error", err)
	}
}
