package enroll

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// LedgerFile is the ledger's file name inside the core CA directory.
const LedgerFile = "enrolled.json"

const ledgerVersion = 1

// Entry is one enrolled module.
type Entry struct {
	EnrolledAt time.Time `json:"enrolled_at"`
	ModuleID   string    `json:"-"`
}

type ledgerDoc struct {
	Modules map[string]*Entry `json:"modules"`
	Version int               `json:"version"`
}

// Ledger is the persisted single-use record of enrolled module IDs
// (<ca dir>/enrolled.json, mode 0600). Every operation re-reads the file so
// that `muxcored enroll reset` takes effect on a running core, and writes are
// atomic (temp file + fsync + rename). An advisory file lock serialises
// read-modify-write between core and the CLI where the platform supports it.
type Ledger struct {
	now  func() time.Time
	path string
	mu   sync.Mutex
}

// NewLedger returns the ledger in caDir. The file is created on first write.
func NewLedger(caDir string) *Ledger {
	return &Ledger{path: filepath.Join(caDir, LedgerFile), now: time.Now}
}

// Path returns the ledger file path.
func (l *Ledger) Path() string { return l.path }

// Consume records id as enrolled. It fails with ErrAlreadyEnrolled when id
// is already recorded.
func (l *Ledger) Consume(id string) error {
	return l.update(func(doc *ledgerDoc) (bool, error) {
		if _, ok := doc.Modules[id]; ok {
			return false, fmt.Errorf("%w (module %q)", ErrAlreadyEnrolled, id)
		}
		doc.Modules[id] = &Entry{EnrolledAt: l.now().UTC()}
		return true, nil
	})
}

// Reset removes id from the ledger so its token can be used again. It
// reports whether an entry was removed.
func (l *Ledger) Reset(id string) (bool, error) {
	removed := false
	err := l.update(func(doc *ledgerDoc) (bool, error) {
		if _, ok := doc.Modules[id]; !ok {
			return false, nil
		}
		delete(doc.Modules, id)
		removed = true
		return true, nil
	})
	return removed, err
}

// Has reports whether id is recorded.
func (l *Ledger) Has(id string) (bool, error) {
	entries, err := l.List()
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.ModuleID == id {
			return true, nil
		}
	}
	return false, nil
}

// List returns the enrolled modules sorted by ID.
func (l *Ledger) List() ([]Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	unlock, err := lockFile(l.path + ".lock")
	if err != nil {
		return nil, err
	}
	defer unlock()
	doc, err := l.read()
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(doc.Modules))
	for id, e := range doc.Modules {
		out = append(out, Entry{ModuleID: id, EnrolledAt: e.EnrolledAt})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModuleID < out[j].ModuleID })
	return out, nil
}

func (l *Ledger) update(fn func(*ledgerDoc) (bool, error)) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return fmt.Errorf("enroll ledger: create dir: %w", err)
	}
	unlock, err := lockFile(l.path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	doc, err := l.read()
	if err != nil {
		return err
	}
	changed, err := fn(doc)
	if err != nil || !changed {
		return err
	}
	return l.write(doc)
}

func (l *Ledger) read() (*ledgerDoc, error) {
	doc := &ledgerDoc{Version: ledgerVersion, Modules: map[string]*Entry{}}
	data, err := os.ReadFile(l.path)
	if errors.Is(err, os.ErrNotExist) {
		return doc, nil
	}
	if err != nil {
		return nil, fmt.Errorf("enroll ledger: read %s: %w", l.path, err)
	}
	if err := json.Unmarshal(data, doc); err != nil {
		return nil, fmt.Errorf("enroll ledger: parse %s: %w", l.path, err)
	}
	if doc.Version != ledgerVersion {
		return nil, fmt.Errorf("enroll ledger: %s has unsupported version %d", l.path, doc.Version)
	}
	if doc.Modules == nil {
		doc.Modules = map[string]*Entry{}
	}
	return doc, nil
}

func (l *Ledger) write(doc *ledgerDoc) error {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("enroll ledger: encode: %w", err)
	}
	data = append(data, '\n')
	dir := filepath.Dir(l.path)
	tmp, err := os.CreateTemp(dir, LedgerFile+".tmp-*") // created 0600
	if err != nil {
		return fmt.Errorf("enroll ledger: create temp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("enroll ledger: chmod: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("enroll ledger: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("enroll ledger: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("enroll ledger: close: %w", err)
	}
	if err := os.Rename(tmpName, l.path); err != nil {
		cleanup()
		return fmt.Errorf("enroll ledger: install: %w", err)
	}
	if d, err := os.Open(dir); err == nil { //nolint:gosec // CA directory
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
