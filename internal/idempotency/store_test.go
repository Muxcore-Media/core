package idempotency

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/pkg/contracts"
)

func TestStore_NewKey(t *testing.T) {
	s := New()
	s.Start(context.Background())
	defer s.Stop()

	ctx := context.Background()
	first, err := s.Store(ctx, "task:download:abc-123", []byte(`{"status":"ok"}`), time.Hour)
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}
	if !first {
		t.Fatal("expected first occurrence (true)")
	}

	exists, err := s.Exists(ctx, "task:download:abc-123")
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if !exists {
		t.Fatal("expected key to exist")
	}

	result, err := s.Result(ctx, "task:download:abc-123")
	if err != nil {
		t.Fatalf("Result failed: %v", err)
	}
	if string(result) != `{"status":"ok"}` {
		t.Fatalf("expected result %q, got %q", `{"status":"ok"}`, string(result))
	}
}

func TestStore_DuplicateKey(t *testing.T) {
	s := New()
	ctx := context.Background()

	first, err := s.Store(ctx, "dup-key", []byte("result-a"), time.Hour)
	if err != nil {
		t.Fatalf("first Store failed: %v", err)
	}
	if !first {
		t.Fatal("expected first occurrence (true)")
	}

	second, err := s.Store(ctx, "dup-key", []byte("result-b"), time.Hour)
	if err != nil {
		t.Fatalf("second Store failed: %v", err)
	}
	if second {
		t.Fatal("expected duplicate (false)")
	}

	// Result should return the original, not the overwrite
	result, _ := s.Result(ctx, "dup-key")
	if string(result) != "result-a" {
		t.Fatalf("expected original result %q, got %q", "result-a", string(result))
	}
}

func TestStore_Expiry(t *testing.T) {
	s := New()
	ctx := context.Background()

	first, err := s.Store(ctx, "expiring-key", []byte("data"), 50*time.Millisecond)
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}
	if !first {
		t.Fatal("expected first occurrence")
	}

	exists, _ := s.Exists(ctx, "expiring-key")
	if !exists {
		t.Fatal("expected key to exist immediately")
	}

	time.Sleep(100 * time.Millisecond)

	exists, _ = s.Exists(ctx, "expiring-key")
	if exists {
		t.Fatal("expected key to be expired")
	}

	result, _ := s.Result(ctx, "expiring-key")
	if result != nil {
		t.Fatal("expected nil result after expiry")
	}

	// Re-storing after expiry should succeed
	restored, err := s.Store(ctx, "expiring-key", []byte("fresh"), time.Hour)
	if err != nil {
		t.Fatalf("re-Store failed: %v", err)
	}
	if !restored {
		t.Fatal("expected first occurrence after expiry (true)")
	}
}

func TestStore_EmptyKey(t *testing.T) {
	s := New()
	_, err := s.Store(context.Background(), "", []byte("data"), time.Hour)
	if err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestStore_NoTTLNeverExpires(t *testing.T) {
	s := New()
	ctx := context.Background()

	s.Store(ctx, "no-ttl", []byte("persistent"), 0)

	time.Sleep(10 * time.Millisecond)
	exists, _ := s.Exists(ctx, "no-ttl")
	if !exists {
		t.Fatal("expected key with zero TTL to never expire")
	}
}

func TestStore_NonExistentKey(t *testing.T) {
	s := New()
	ctx := context.Background()

	exists, err := s.Exists(ctx, "does-not-exist")
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if exists {
		t.Fatal("expected false for non-existent key")
	}

	result, err := s.Result(ctx, "does-not-exist")
	if err != nil {
		t.Fatalf("Result failed: %v", err)
	}
	if result != nil {
		t.Fatal("expected nil result for non-existent key")
	}
}

func TestStore_ReaperEvictsExpired(t *testing.T) {
	s := New()
	s.Start(context.Background())
	defer s.Stop()

	ctx := context.Background()
	s.Store(ctx, "reap-me", []byte("bye"), 50*time.Millisecond)

	// Give the reaper time to evict
	time.Sleep(200 * time.Millisecond)

	exists, _ := s.Exists(ctx, "reap-me")
	if exists {
		t.Fatal("expected reaper to evict expired key")
	}
}

func TestStore_ConcurrentAccess(t *testing.T) {
	s := New()
	ctx := context.Background()

	done := make(chan struct{})
	const workers = 10
	const keysPerWorker = 20

	for w := range workers {
		go func(w int) {
			for k := range keysPerWorker {
				key := fmt.Sprintf("concurrent-%d-%d", w, k)
				s.Store(ctx, key, []byte("data"), time.Hour)
				s.Exists(ctx, key)
				s.Result(ctx, key)
			}
			done <- struct{}{}
		}(w)
	}

	for range workers {
		<-done
	}

	if s.Count() != workers*keysPerWorker {
		t.Fatalf("expected %d entries, got %d", workers*keysPerWorker, s.Count())
	}
}

func TestFileStore_Persistence(t *testing.T) {
	dir := t.TempDir()

	s, err := NewWithDir(dir)
	if err != nil {
		t.Fatalf("NewWithDir failed: %v", err)
	}
	s.Start(context.Background())

	ctx := context.Background()
	s.Store(ctx, "persist-me", []byte("saved-value"), time.Hour)
	s.Stop()

	// Re-create from the same dir
	s2, err := NewWithDir(dir)
	if err != nil {
		t.Fatalf("second NewWithDir failed: %v", err)
	}
	defer s2.Stop()

	exists, _ := s2.Exists(ctx, "persist-me")
	if !exists {
		t.Fatal("expected persisted key to survive restart")
	}

	result, _ := s2.Result(ctx, "persist-me")
	if string(result) != "saved-value" {
		t.Fatalf("expected saved result %q, got %q", "saved-value", string(result))
	}
}

func TestFileStore_ExpiryNotPersisted(t *testing.T) {
	dir := t.TempDir()

	s, err := NewWithDir(dir)
	if err != nil {
		t.Fatalf("NewWithDir failed: %v", err)
	}
	s.Start(context.Background())

	ctx := context.Background()
	s.Store(ctx, "short-lived", []byte("gone"), 50*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	s.Stop()

	// Re-create — should not load expired entries
	s2, err := NewWithDir(dir)
	if err != nil {
		t.Fatalf("second NewWithDir failed: %v", err)
	}
	defer s2.Stop()

	exists, _ := s2.Exists(ctx, "short-lived")
	if exists {
		t.Fatal("expected expired key to not be loaded from disk")
	}

	// The file should have been cleaned up
	_, err = os.Stat(filepath.Join(dir, "short-lived.json"))
	if !os.IsNotExist(err) {
		t.Fatal("expected expired entry file to be deleted on load")
	}
}

func TestCollection_ImplementsInterface(t *testing.T) {
	var _ contracts.IdempotencyProvider = (*Store)(nil)
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
	dir := filepath.Join(t.TempDir(), "sub", "idempotency")
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

func TestCount_ReturnsCorrectCount(t *testing.T) {
	s := New()
	ctx := context.Background()

	if s.Count() != 0 {
		t.Fatalf("expected 0, got %d", s.Count())
	}

	s.Store(ctx, "key-a", []byte("r"), time.Hour)
	s.Store(ctx, "key-b", []byte("r"), time.Hour)

	if s.Count() != 2 {
		t.Fatalf("expected 2, got %d", s.Count())
	}

	s.Store(ctx, "key-a", []byte("r2"), time.Hour)
	if s.Count() != 2 {
		t.Fatalf("expected 2 after duplicate store, got %d", s.Count())
	}
}

func TestStartStop_Lifecycle(t *testing.T) {
	s := New()
	ctx := context.Background()

	s.Start(ctx)
	s.Start(ctx)

	s.Stop()
	s.Stop()
}

func TestStartStop_WithCancel(t *testing.T) {
	s := New()
	ctx, cancel := context.WithCancel(context.Background())

	s.Start(ctx)
	cancel()

	time.Sleep(50 * time.Millisecond)

	s.Stop()
}

func TestRegisterSelf_RegistersInRegistry(t *testing.T) {
	reg := registry.New()
	s := New()
	RegisterSelf(reg, s, "node-1")

	mods := reg.FindByCapability(contracts.CapabilityIdempotency)
	if len(mods) != 1 {
		t.Fatalf("expected 1 module with idempotency capability, got %d", len(mods))
	}
	if mods[0].Info.ID != "core.idempotency" {
		t.Fatalf("expected module ID 'core.idempotency', got %q", mods[0].Info.ID)
	}
}

func TestRegisterSelf_DuplicateIsNoop(t *testing.T) {
	reg := registry.New()
	s := New()
	RegisterSelf(reg, s, "node-1")
	RegisterSelf(reg, s, "node-1")

	mods := reg.FindByCapability(contracts.CapabilityIdempotency)
	if len(mods) != 1 {
		t.Fatalf("expected 1 module after duplicate register, got %d", len(mods))
	}
}

func TestStore_AllowsReStoreAfterExpiry(t *testing.T) {
	s := New()
	ctx := context.Background()

	first, _ := s.Store(ctx, "rekey", []byte("v1"), 50*time.Millisecond)
	if !first {
		t.Fatal("expected first store to succeed")
	}

	time.Sleep(100 * time.Millisecond)

	second, err := s.Store(ctx, "rekey", []byte("v2"), time.Hour)
	if err != nil {
		t.Fatalf("re-store failed: %v", err)
	}
	if !second {
		t.Fatal("expected re-store after expiry to return true")
	}

	result, _ := s.Result(ctx, "rekey")
	if string(result) != "v2" {
		t.Fatalf("expected updated result %q, got %q", "v2", string(result))
	}
}

func TestResult_ReturnsNilForUnknownKey(t *testing.T) {
	s := New()
	result, err := s.Result(context.Background(), "no-such-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil, got %v", result)
	}
}

func TestExists_ReturnsFalseForExpiredKey(t *testing.T) {
	s := New()
	ctx := context.Background()

	s.Store(ctx, "exp-key", []byte("data"), 30*time.Millisecond)
	time.Sleep(60 * time.Millisecond)

	exists, _ := s.Exists(ctx, "exp-key")
	if exists {
		t.Fatal("expected false for expired key")
	}
}

func TestFileStore_EntriesSurviveRecreation(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	s, err := NewWithDir(dir)
	if err != nil {
		t.Fatalf("NewWithDir failed: %v", err)
	}

	s.Store(ctx, "key1", []byte("val1"), time.Hour)
	s.Store(ctx, "key2", []byte("val2"), time.Hour)
	s.Store(ctx, "key3", []byte("val3"), time.Hour)

	s2, err := NewWithDir(dir)
	if err != nil {
		t.Fatalf("second NewWithDir failed: %v", err)
	}
	defer s2.Stop()

	if s2.Count() != 3 {
		t.Fatalf("expected 3 persisted entries, got %d", s2.Count())
	}

	for _, key := range []string{"key1", "key2", "key3"} {
		exists, _ := s2.Exists(ctx, key)
		if !exists {
			t.Fatalf("expected key %q to persist", key)
		}
	}
}

func BenchmarkStore(b *testing.B) {
	s := New()
	ctx := context.Background()

	b.ResetTimer()
	for range b.N {
		key := fmt.Sprintf("bench-%d", b.N)
		s.Store(ctx, key, []byte("data"), time.Hour)
		s.Exists(ctx, key)
	}
}
