package deadletter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/pkg/contracts"
)

func dlEvent(id string) contracts.Event {
	return contracts.Event{
		ID:   id,
		Type: "test.event",
	}
}

func TestStore_StoreAndReplay(t *testing.T) {
	s := New()
	ctx := context.Background()

	err := s.Store(ctx, dlEvent("evt-1"), "handler-a", errors.New("timeout"))
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	entries, err := s.Replay(ctx, "handler-a", time.Time{})
	if err != nil {
		t.Fatalf("Replay failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].HandlerName != "handler-a" {
		t.Fatalf("expected handler-a, got %s", entries[0].HandlerName)
	}
	if entries[0].Error != "timeout" {
		t.Fatalf("expected error 'timeout', got %q", entries[0].Error)
	}
}

func TestStore_Discard(t *testing.T) {
	s := New()
	ctx := context.Background()

	s.Store(ctx, dlEvent("evt-discard"), "handler-b", errors.New("bad data"))

	if err := s.Discard(ctx, "evt-discard"); err != nil {
		t.Fatalf("Discard failed: %v", err)
	}

	entries, _ := s.Replay(ctx, "handler-b", time.Time{})
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries after discard, got %d", len(entries))
	}
}

func TestStore_DiscardNotFound(t *testing.T) {
	s := New()
	err := s.Discard(context.Background(), "does-not-exist")
	if err == nil {
		t.Fatal("expected error for discarding non-existent entry")
	}
}

func TestStore_ReplayByHandler(t *testing.T) {
	s := New()
	ctx := context.Background()

	s.Store(ctx, dlEvent("evt-1"), "handler-a", errors.New("err1"))
	s.Store(ctx, dlEvent("evt-2"), "handler-b", errors.New("err2"))
	s.Store(ctx, dlEvent("evt-3"), "handler-a", errors.New("err3"))

	entries, _ := s.Replay(ctx, "handler-a", time.Time{})
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries for handler-a, got %d", len(entries))
	}
}

func TestStore_ReplaySince(t *testing.T) {
	s := New()
	ctx := context.Background()

	s.Store(ctx, dlEvent("old"), "handler-a", errors.New("old"))
	time.Sleep(10 * time.Millisecond)

	cutoff := time.Now()

	s.Store(ctx, dlEvent("new"), "handler-a", errors.New("new"))

	entries, _ := s.Replay(ctx, "handler-a", cutoff)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry since cutoff, got %d", len(entries))
	}
	if entries[0].Event.ID != "new" {
		t.Fatalf("expected 'new' entry, got %s", entries[0].Event.ID)
	}
}

func TestStore_EmptyHandlerReturnsAll(t *testing.T) {
	s := New()
	ctx := context.Background()

	s.Store(ctx, dlEvent("evt-1"), "handler-a", errors.New("e1"))
	s.Store(ctx, dlEvent("evt-2"), "handler-b", errors.New("e2"))

	entries, _ := s.Replay(ctx, "", time.Time{})
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries with empty handler filter, got %d", len(entries))
	}
}

func TestStore_IdsSortedByTime(t *testing.T) {
	s := New()
	ctx := context.Background()

	s.Store(ctx, dlEvent("second"), "h", errors.New("e2"))
	time.Sleep(5 * time.Millisecond)
	s.Store(ctx, dlEvent("first"), "h", errors.New("e1"))

	entries, _ := s.Replay(ctx, "h", time.Time{})
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Event.ID != "second" || entries[1].Event.ID != "first" {
		t.Fatal("expected entries sorted by FailedAt ascending")
	}
}

func TestFileStore_Persistence(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	s, err := NewWithDir(dir)
	if err != nil {
		t.Fatalf("NewWithDir failed: %v", err)
	}
	s.Store(ctx, dlEvent("persist-me"), "handler-x", errors.New("disk-err"))
	if s.Count() != 1 {
		t.Fatalf("expected 1 entry, got %d", s.Count())
	}

	s2, err := NewWithDir(dir)
	if err != nil {
		t.Fatalf("second NewWithDir failed: %v", err)
	}
	if s2.Count() != 1 {
		t.Fatalf("expected 1 persisted entry, got %d", s2.Count())
	}

	entries, _ := s2.Replay(ctx, "handler-x", time.Time{})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after reload, got %d", len(entries))
	}
	if entries[0].Error != "disk-err" {
		t.Fatalf("expected error 'disk-err', got %q", entries[0].Error)
	}
}

func TestFileStore_DiscardPersists(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	s, _ := NewWithDir(dir)
	s.Store(ctx, dlEvent("gone"), "h", errors.New("bye"))
	s.Discard(ctx, "gone")

	s2, _ := NewWithDir(dir)
	if s2.Count() != 0 {
		t.Fatal("expected discarded entry to not be reloaded")
	}
}

func TestStore_ImplementsInterface(t *testing.T) {
	var _ contracts.DeadLetterProvider = (*Store)(nil)
}

func TestNew_InMemoryStore(t *testing.T) {
	s := New()
	if s == nil {
		t.Fatal("New returned nil")
	}
	if s.dir != "" {
		t.Fatal("in-memory store should have empty dir")
	}
	if s.Count() != 0 {
		t.Fatalf("expected 0 entries, got %d", s.Count())
	}
}

func TestNewWithDir_CreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub", "deadletter")
	s, err := NewWithDir(dir)
	if err != nil {
		t.Fatalf("NewWithDir failed: %v", err)
	}
	if s.dir != dir {
		t.Fatalf("expected dir %q, got %q", dir, s.dir)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("expected directory")
	}
}

func TestStore_GeneratesIDForEmptyID(t *testing.T) {
	s := New()
	ctx := context.Background()

	evt := contracts.Event{Type: "no-id-event"}
	err := s.Store(ctx, evt, "handler-gen", errors.New("fail"))
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}
	if s.Count() != 1 {
		t.Fatalf("expected 1 entry, got %d", s.Count())
	}
}

func TestStore_CapsAtMaxEntries(t *testing.T) {
	s := New()
	ctx := context.Background()

	for i := 0; i < maxFileEntries+5; i++ {
		id := fmt.Sprintf("evt-%d", i)
		s.Store(ctx, dlEvent(id), "handler-cap", errors.New("err"))
	}

	if s.Count() > maxFileEntries {
		t.Fatalf("expected count <= %d, got %d", maxFileEntries, s.Count())
	}
}

func TestCount_ReturnsCorrectCount(t *testing.T) {
	s := New()
	ctx := context.Background()

	if s.Count() != 0 {
		t.Fatalf("expected 0, got %d", s.Count())
	}

	s.Store(ctx, dlEvent("a"), "h", errors.New("e"))
	s.Store(ctx, dlEvent("b"), "h", errors.New("e"))
	s.Store(ctx, dlEvent("c"), "h", errors.New("e"))

	if s.Count() != 3 {
		t.Fatalf("expected 3, got %d", s.Count())
	}

	s.Discard(ctx, "b")
	if s.Count() != 2 {
		t.Fatalf("expected 2 after discard, got %d", s.Count())
	}
}

func TestSafeFilename(t *testing.T) {
	s := New()
	tests := []struct {
		input string
		check func(string) bool
	}{
		{"normal", func(r string) bool { return r == "normal.json" }},
		{"has/slash", func(r string) bool { return !strings.Contains(r, "/") }},
		{"has:colon", func(r string) bool { return !strings.Contains(r, ":") }},
		{"has space", func(r string) bool { return !strings.Contains(r, " ") }},
		{"has..dot", func(r string) bool { return !strings.Contains(r, "..") }},
		{"has\x00null", func(r string) bool { return !strings.Contains(r, "\x00") }},
	}
	for _, tc := range tests {
		got := s.safeFilename(tc.input)
		if !strings.HasSuffix(got, ".json") {
			t.Errorf("safeFilename(%q) = %q, missing .json suffix", tc.input, got)
		}
		if !tc.check(got) {
			t.Errorf("safeFilename(%q) = %q, failed check", tc.input, got)
		}
	}
}

func TestRegisterSelf_RegistersInRegistry(t *testing.T) {
	reg := registry.New()
	s := New()
	RegisterSelf(reg, s, "node-1")

	mods := reg.FindByCapability(contracts.CapabilityDeadLetter)
	if len(mods) != 1 {
		t.Fatalf("expected 1 module with deadletter capability, got %d", len(mods))
	}
	if mods[0].Info.ID != "core.deadletter" {
		t.Fatalf("expected module ID 'core.deadletter', got %q", mods[0].Info.ID)
	}
}

func TestRegisterSelf_DuplicateIsNoop(t *testing.T) {
	reg := registry.New()
	s := New()
	RegisterSelf(reg, s, "node-1")
	RegisterSelf(reg, s, "node-1")

	mods := reg.FindByCapability(contracts.CapabilityDeadLetter)
	if len(mods) != 1 {
		t.Fatalf("expected 1 module after duplicate register, got %d", len(mods))
	}
}

func TestStore_NilErrorBecomesUnknown(t *testing.T) {
	s := New()
	ctx := context.Background()

	s.Store(ctx, dlEvent("nil-err"), "handler-nil", nil)

	entries, _ := s.Replay(ctx, "handler-nil", time.Time{})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Error != "unknown error" {
		t.Fatalf("expected 'unknown error', got %q", entries[0].Error)
	}
}

func TestReplay_EmptyResultReturnsNilSlice(t *testing.T) {
	s := New()
	ctx := context.Background()

	entries, err := s.Replay(ctx, "nonexistent", time.Time{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty result, got %d", len(entries))
	}
}
