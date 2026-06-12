package eventstore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/pkg/contracts"
)

func event(stream, id string) contracts.Event {
	return contracts.Event{
		ID:      id,
		Type:    stream + ".event",
		Source:  "test",
		Payload: []byte(`{"data":"value"}`),
	}
}

func TestAppend_NewStream(t *testing.T) {
	s := New()
	ctx := context.Background()

	seq, err := s.Append(ctx, "orders", []contracts.Event{event("orders", "1")})
	if err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	if seq != 1 {
		t.Fatalf("expected first sequence 1, got %d", seq)
	}
}

func TestAppend_MultipleEvents(t *testing.T) {
	s := New()
	ctx := context.Background()

	seq, err := s.Append(ctx, "orders", []contracts.Event{
		event("orders", "1"),
		event("orders", "2"),
		event("orders", "3"),
	})
	if err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	if seq != 1 {
		t.Fatalf("expected starting sequence 1, got %d", seq)
	}

	entries, _ := s.Read(ctx, "orders", 1, 0)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	for i, e := range entries {
		if e.Sequence != int64(i+1) {
			t.Fatalf("entry %d: expected sequence %d, got %d", i, i+1, e.Sequence)
		}
	}
}

func TestAppend_EmptyBatch(t *testing.T) {
	s := New()
	_, err := s.Append(context.Background(), "test", nil)
	if err == nil {
		t.Fatal("expected error for empty batch")
	}
}

func TestRead_Range(t *testing.T) {
	s := New()
	ctx := context.Background()

	for i := 1; i <= 10; i++ {
		id := fmt.Sprintf("evt-%d", i)
		s.Append(ctx, "range-stream", []contracts.Event{event("range-stream", id)})
	}

	entries, _ := s.Read(ctx, "range-stream", 3, 5)
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(entries))
	}
	if entries[0].Sequence != 3 {
		t.Fatalf("expected first sequence 3, got %d", entries[0].Sequence)
	}
}

func TestRead_BeyondEnd(t *testing.T) {
	s := New()
	ctx := context.Background()

	s.Append(ctx, "short", []contracts.Event{event("short", "1")})
	entries, _ := s.Read(ctx, "short", 100, 0)
	if len(entries) != 0 {
		t.Fatalf("expected empty slice when reading beyond stream end")
	}
}

func TestRead_EmptyStream(t *testing.T) {
	s := New()
	entries, _ := s.Read(context.Background(), "empty", 1, 0)
	if len(entries) != 0 {
		t.Fatalf("expected empty slice for non-existent stream")
	}
}

func TestStreams(t *testing.T) {
	s := New()
	ctx := context.Background()

	s.Append(ctx, "alpha", []contracts.Event{event("alpha", "1")})
	s.Append(ctx, "beta", []contracts.Event{event("beta", "1")})
	s.Append(ctx, "gamma", []contracts.Event{event("gamma", "1")})

	streams, _ := s.Streams(ctx)
	if len(streams) != 3 {
		t.Fatalf("expected 3 streams, got %d", len(streams))
	}
	if streams[0] != "alpha" || streams[1] != "beta" || streams[2] != "gamma" {
		t.Fatalf("expected sorted streams, got %v", streams)
	}
}

func TestAppend_SeparateStreamsIndependent(t *testing.T) {
	s := New()
	ctx := context.Background()

	s.Append(ctx, "a", []contracts.Event{event("a", "1")})
	s.Append(ctx, "a", []contracts.Event{event("a", "2")})
	s.Append(ctx, "b", []contracts.Event{event("b", "1")})

	entriesA, _ := s.Read(ctx, "a", 1, 0)
	if len(entriesA) != 2 {
		t.Fatalf("expected 2 entries in stream a, got %d", len(entriesA))
	}
	if entriesA[0].Sequence != 1 || entriesA[1].Sequence != 2 {
		t.Fatalf("stream a sequences should be 1,2: got %d,%d", entriesA[0].Sequence, entriesA[1].Sequence)
	}

	entriesB, _ := s.Read(ctx, "b", 1, 0)
	if len(entriesB) != 1 {
		t.Fatalf("expected 1 entry in stream b, got %d", len(entriesB))
	}
	if entriesB[0].Sequence != 1 {
		t.Fatalf("stream b first sequence should be 1, got %d", entriesB[0].Sequence)
	}
}

func TestSubscribe_CatchUp(t *testing.T) {
	s := New()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	s.Append(ctx, "sub-stream", []contracts.Event{event("sub-stream", "1")})
	s.Append(ctx, "sub-stream", []contracts.Event{event("sub-stream", "2")})

	ch, err := s.Subscribe(ctx, "sub-stream", 1)
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	received := 0
	for range 2 {
		select {
		case <-ctx.Done():
			t.Fatal("timeout waiting for events")
		case entry, ok := <-ch:
			if !ok {
				t.Fatal("channel closed unexpectedly")
			}
			received++
			_ = entry
		}
	}
	if received != 2 {
		t.Fatalf("expected 2 events, got %d", received)
	}
}

func TestSubscribe_FromLaterSequence(t *testing.T) {
	s := New()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	s.Append(ctx, "catchup", []contracts.Event{event("catchup", "1")})
	s.Append(ctx, "catchup", []contracts.Event{event("catchup", "2")})
	s.Append(ctx, "catchup", []contracts.Event{event("catchup", "3")})

	ch, _ := s.Subscribe(ctx, "catchup", 3)

	entry, ok := <-ch
	if !ok {
		t.Fatal("channel closed before receiving event 3")
	}
	if entry.Sequence != 3 {
		t.Fatalf("expected sequence 3, got %d", entry.Sequence)
	}
}

func TestSubscribe_LiveEvent(t *testing.T) {
	s := New()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ch, _ := s.Subscribe(ctx, "live", 1)

	// Give poll a moment to start
	time.Sleep(100 * time.Millisecond)

	s.Append(ctx, "live", []contracts.Event{event("live", "1")})

	select {
	case entry := <-ch:
		if entry.Sequence != 1 {
			t.Fatalf("expected sequence 1, got %d", entry.Sequence)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for live event")
	}
}

func TestFileStore_Persistence(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	s, err := NewWithDir(dir)
	if err != nil {
		t.Fatalf("NewWithDir failed: %v", err)
	}

	s.Append(ctx, "persist-stream", []contracts.Event{event("persist-stream", "1")})
	s.Append(ctx, "persist-stream", []contracts.Event{event("persist-stream", "2")})

	s2, err := NewWithDir(dir)
	if err != nil {
		t.Fatalf("second NewWithDir failed: %v", err)
	}

	entries, _ := s2.Read(ctx, "persist-stream", 1, 0)
	if len(entries) != 2 {
		t.Fatalf("expected 2 persisted entries, got %d", len(entries))
	}
	if entries[0].Sequence != 1 || entries[1].Sequence != 2 {
		t.Fatalf("expected sequences 1,2: got %d,%d", entries[0].Sequence, entries[1].Sequence)
	}
}

func TestFileStore_PersistenceAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	s, _ := NewWithDir(dir)
	s.Append(ctx, "restart-stream", []contracts.Event{event("restart-stream", "1")})
	s.Append(ctx, "restart-stream", []contracts.Event{event("restart-stream", "2")})
	s.Append(ctx, "restart-stream", []contracts.Event{event("restart-stream", "3")})

	s2, _ := NewWithDir(dir)
	entries, _ := s2.Read(ctx, "restart-stream", 2, 2)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries from sequence 2, got %d", len(entries))
	}
	if entries[0].Sequence != 2 || entries[1].Sequence != 3 {
		t.Fatalf("expected sequences 2,3: got %d,%d", entries[0].Sequence, entries[0].Sequence)
	}
}

func TestStore_ImplementsInterface(t *testing.T) {
	var _ contracts.EventStore = (*Store)(nil)
}

func TestSafeStreamKey(t *testing.T) {
	s := New()
	if key := s.safeStreamKey("normal-name"); key != "normal-name" {
		t.Fatalf("expected 'normal-name', got %q", key)
	}
	if key := s.safeStreamKey("path/../traversal"); key != "path___traversal" {
		t.Fatalf("expected 'path___traversal', got %q", key)
	}
	if key := s.safeStreamKey("has:colons"); key != "has_colons" {
		t.Fatalf("expected 'has_colons', got %q", key)
	}
}

func TestNew_InMemoryStore(t *testing.T) {
	s := New()
	if s == nil {
		t.Fatal("New returned nil")
	}
	if s.baseDir != "" {
		t.Fatal("in-memory store should have empty baseDir")
	}
	ctx := context.Background()
	streams, _ := s.Streams(ctx)
	if len(streams) != 0 {
		t.Fatalf("expected 0 streams, got %d", len(streams))
	}
}

func TestNewWithDir_CreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub", "events")
	s, err := NewWithDir(dir)
	if err != nil {
		t.Fatalf("NewWithDir failed: %v", err)
	}
	if s.baseDir != dir {
		t.Fatalf("expected baseDir %q, got %q", dir, s.baseDir)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("expected directory")
	}
}

func TestAppend_IncrementsSequenceAcrossCalls(t *testing.T) {
	s := New()
	ctx := context.Background()

	seq1, _ := s.Append(ctx, "seq-test", []contracts.Event{event("seq-test", "1")})
	if seq1 != 1 {
		t.Fatalf("expected first call seq 1, got %d", seq1)
	}

	seq2, _ := s.Append(ctx, "seq-test", []contracts.Event{event("seq-test", "2")})
	if seq2 != 2 {
		t.Fatalf("expected second call seq 2, got %d", seq2)
	}

	seq3, _ := s.Append(ctx, "seq-test", []contracts.Event{
		event("seq-test", "3"),
		event("seq-test", "4"),
	})
	if seq3 != 3 {
		t.Fatalf("expected third call seq 3, got %d", seq3)
	}

	seq4, _ := s.Append(ctx, "seq-test", []contracts.Event{event("seq-test", "5")})
	if seq4 != 5 {
		t.Fatalf("expected fourth call seq 5, got %d", seq4)
	}
}

func TestRead_RespectsFromSequence(t *testing.T) {
	s := New()
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		s.Append(ctx, "from-seq", []contracts.Event{event("from-seq", fmt.Sprintf("e%d", i))})
	}

	entries, _ := s.Read(ctx, "from-seq", 4, 0)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries from seq 4, got %d", len(entries))
	}
	if entries[0].Sequence != 4 {
		t.Fatalf("expected first entry seq 4, got %d", entries[0].Sequence)
	}
}

func TestRead_RespectsLimit(t *testing.T) {
	s := New()
	ctx := context.Background()

	for i := 1; i <= 10; i++ {
		s.Append(ctx, "limit-test", []contracts.Event{event("limit-test", fmt.Sprintf("e%d", i))})
	}

	entries, _ := s.Read(ctx, "limit-test", 1, 3)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries with limit 3, got %d", len(entries))
	}
}

func TestRead_ReturnsEmptyForUnknownStream(t *testing.T) {
	s := New()
	entries, err := s.Read(context.Background(), "nonexistent", 1, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty result, got %d entries", len(entries))
	}
}

func TestSeqFilename(t *testing.T) {
	tests := []struct {
		seq  int64
		want string
	}{
		{0, "00000000000000000000.json"},
		{1, "00000000000000000001.json"},
		{999, "00000000000000000999.json"},
		{1234567890, "00000000001234567890.json"},
	}
	for _, tc := range tests {
		got := seqFilename(tc.seq)
		if got != tc.want {
			t.Errorf("seqFilename(%d) = %q, want %q", tc.seq, got, tc.want)
		}
	}
}

func TestSafeStreamKey_SanitizesNullBytes(t *testing.T) {
	s := New()
	key := s.safeStreamKey("has\x00null")
	if strings.Contains(key, "\x00") {
		t.Fatal("null byte not stripped")
	}
}

func TestSafeStreamKey_SanitizesBackslash(t *testing.T) {
	s := New()
	key := s.safeStreamKey(`path\to\thing`)
	if strings.Contains(key, `\`) {
		t.Fatal("backslash not sanitized")
	}
}

func TestRegisterSelf_RegistersInRegistry(t *testing.T) {
	reg := registry.New()
	s := New()
	RegisterSelf(reg, s, "node-1")

	mods := reg.FindByCapability(contracts.CapabilityEventStore)
	if len(mods) != 1 {
		t.Fatalf("expected 1 module with event.store capability, got %d", len(mods))
	}
	if mods[0].Info.ID != "core.eventstore" {
		t.Fatalf("expected module ID 'core.eventstore', got %q", mods[0].Info.ID)
	}
}

func TestRegisterSelf_NilRegistry(t *testing.T) {
	s := New()
	RegisterSelf(nil, s, "node-1")
}

func TestRegisterSelf_DuplicateIsNoop(t *testing.T) {
	reg := registry.New()
	s := New()
	RegisterSelf(reg, s, "node-1")
	RegisterSelf(reg, s, "node-1")

	mods := reg.FindByCapability(contracts.CapabilityEventStore)
	if len(mods) != 1 {
		t.Fatalf("expected 1 module after duplicate register, got %d", len(mods))
	}
}
