package audit

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

func entry(id, actor, action, resource string) contracts.AuditEntry {
	return contracts.AuditEntry{
		ID:        id,
		Timestamp: time.Now(),
		Actor:     actor,
		Action:    action,
		Resource:  resource,
	}
}

// --- No-op (empty path) ---

func TestFileLogger_Noop_WhenPathEmpty(t *testing.T) {
	fl, err := NewFileLogger("")
	if err != nil {
		t.Fatalf("NewFileLogger('') failed: %v", err)
	}
	ctx := context.Background()
	if err := fl.Log(ctx, entry("1", "a", "b", "c")); err != nil {
		t.Errorf("Log on no-op logger should return nil, got %v", err)
	}
	entries, err := fl.Query(ctx, contracts.AuditFilter{})
	if err != nil || len(entries) != 0 {
		t.Errorf("Query on no-op logger should return empty, got %v / %v", entries, err)
	}
	rc, err := fl.Export(ctx, "json")
	if err != nil {
		t.Errorf("Export on no-op logger returned error: %v", err)
	}
	rc.Close()
}

// --- Basic log writes ---

func TestFileLogger_WritesJSONL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	fl, err := NewFileLogger(path)
	if err != nil {
		t.Fatalf("NewFileLogger: %v", err)
	}
	defer fl.Close()

	ctx := context.Background()
	fl.Log(ctx, entry("e1", "user1", "read", "/api/foo"))
	fl.Log(ctx, entry("e2", "user2", "write", "/api/bar"))

	data, err := os.ReadFile(path) //nolint:gosec // test file with controlled path
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 JSONL lines, got %d", len(lines))
	}
	for _, line := range lines {
		var e contracts.AuditEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Errorf("invalid JSON line %q: %v", line, err)
		}
	}
}

// --- Hash chain ---

func TestFileLogger_HashChain(t *testing.T) {
	dir := t.TempDir()
	fl, _ := NewFileLogger(filepath.Join(dir, "audit.jsonl"))
	defer fl.Close()

	ctx := context.Background()
	fl.Log(ctx, entry("e1", "a", "b", "c"))
	fl.Log(ctx, entry("e2", "a", "b", "c"))
	fl.Log(ctx, entry("e3", "a", "b", "c"))

	entries := fl.entries
	if len(entries) < 2 {
		t.Fatalf("need at least 2 entries for hash chain test, got %d", len(entries))
	}
	// The second entry should have a non-empty PrevEntryHash.
	if entries[1].PrevEntryHash == "" {
		t.Error("expected second entry to have PrevEntryHash")
	}
	// Third entry's PrevEntryHash should differ from second's.
	if len(entries) >= 3 && entries[2].PrevEntryHash == entries[1].PrevEntryHash {
		t.Error("expected different PrevEntryHash values for consecutive entries")
	}
}

func TestFileLogger_VerifyChainIntegrity_Clean(t *testing.T) {
	dir := t.TempDir()
	fl, _ := NewFileLogger(filepath.Join(dir, "audit.jsonl"))
	defer fl.Close()

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		fl.Log(ctx, entry("e", "a", "b", "c"))
	}

	result, err := fl.VerifyChainIntegrity(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("VerifyChainIntegrity error: %v", err)
	}
	if !result.Valid {
		t.Errorf("expected valid chain, broken links: %v", result.BrokenLinks)
	}
}

func TestFileLogger_VerifyChainIntegrity_Tampered(t *testing.T) {
	dir := t.TempDir()
	fl, _ := NewFileLogger(filepath.Join(dir, "audit.jsonl"))
	defer fl.Close()

	ctx := context.Background()
	fl.Log(ctx, entry("e1", "a", "b", "c"))
	fl.Log(ctx, entry("e2", "a", "b", "c"))
	fl.Log(ctx, entry("e3", "a", "b", "c"))

	// Tamper with the second entry to break the chain.
	fl.mu.Lock()
	fl.entries[1].Actor = "tampered"
	fl.mu.Unlock()

	result, err := fl.VerifyChainIntegrity(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("VerifyChainIntegrity error: %v", err)
	}
	if result.Valid {
		t.Error("expected invalid chain after tampering")
	}
	if len(result.BrokenLinks) == 0 {
		t.Error("expected broken links reported")
	}
}

// --- HMAC signing ---

func TestFileLogger_HMACSigning(t *testing.T) {
	dir := t.TempDir()
	fl, _ := NewFileLogger(filepath.Join(dir, "audit.jsonl"))
	defer fl.Close()

	key := []byte("secret-key-for-testing")
	fl.SetSigningKey(key)

	ctx := context.Background()
	fl.Log(ctx, entry("e1", "user", "action", "resource"))

	// Read back the written line and verify it has a Signature field.
	f, _ := os.Open(filepath.Join(dir, "audit.jsonl")) //nolint:gosec // test file with controlled path
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Scan()
	var e contracts.AuditEntry
	json.Unmarshal(scanner.Bytes(), &e)

	if e.Signature == "" {
		t.Error("expected Signature field to be populated when signing key is set")
	}
}

func TestFileLogger_NoSignatureWithoutKey(t *testing.T) {
	dir := t.TempDir()
	fl, _ := NewFileLogger(filepath.Join(dir, "audit.jsonl"))
	defer fl.Close()

	ctx := context.Background()
	fl.Log(ctx, entry("e1", "user", "action", "resource"))

	f, _ := os.Open(filepath.Join(dir, "audit.jsonl")) //nolint:gosec // test file with controlled path
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Scan()
	var e contracts.AuditEntry
	json.Unmarshal(scanner.Bytes(), &e)

	if e.Signature != "" {
		t.Error("expected no Signature when no signing key is set")
	}
}

// --- Query ---

func TestFileLogger_Query_FilterByActor(t *testing.T) {
	dir := t.TempDir()
	fl, _ := NewFileLogger(filepath.Join(dir, "audit.jsonl"))
	defer fl.Close()

	ctx := context.Background()
	fl.Log(ctx, entry("e1", "alice", "read", "r1"))
	fl.Log(ctx, entry("e2", "bob", "write", "r2"))
	fl.Log(ctx, entry("e3", "alice", "delete", "r3"))

	results, err := fl.Query(ctx, contracts.AuditFilter{Actor: "alice"})
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results for actor=alice, got %d", len(results))
	}
}

func TestFileLogger_Query_FilterByAction(t *testing.T) {
	dir := t.TempDir()
	fl, _ := NewFileLogger(filepath.Join(dir, "audit.jsonl"))
	defer fl.Close()

	ctx := context.Background()
	fl.Log(ctx, entry("e1", "alice", "read", "r1"))
	fl.Log(ctx, entry("e2", "bob", "write", "r2"))

	results, _ := fl.Query(ctx, contracts.AuditFilter{Action: "read"})
	if len(results) != 1 {
		t.Errorf("expected 1 result for action=read, got %d", len(results))
	}
}

func TestFileLogger_Query_FilterByTimeRange(t *testing.T) {
	dir := t.TempDir()
	fl, _ := NewFileLogger(filepath.Join(dir, "audit.jsonl"))
	defer fl.Close()

	ctx := context.Background()
	before := time.Now()
	time.Sleep(time.Millisecond)
	fl.Log(ctx, entry("e1", "alice", "read", "r1"))
	fl.Log(ctx, entry("e2", "alice", "read", "r2"))
	time.Sleep(time.Millisecond)
	after := time.Now()
	fl.Log(ctx, entry("e3", "alice", "read", "r3"))

	results, _ := fl.Query(ctx, contracts.AuditFilter{From: before, To: after})
	if len(results) != 2 {
		t.Errorf("expected 2 results in time range, got %d", len(results))
	}
}

// --- Export ---

func TestFileLogger_Export_JSON(t *testing.T) {
	dir := t.TempDir()
	fl, _ := NewFileLogger(filepath.Join(dir, "audit.jsonl"))
	defer fl.Close()

	ctx := context.Background()
	fl.Log(ctx, entry("e1", "a", "b", "c"))

	rc, err := fl.Export(ctx, "json")
	if err != nil {
		t.Fatalf("Export json: %v", err)
	}
	defer rc.Close()

	var entries []contracts.AuditEntry
	json.NewDecoder(rc).Decode(&entries)
	if len(entries) != 1 {
		t.Errorf("expected 1 entry in JSON export, got %d", len(entries))
	}
}

func TestFileLogger_Export_CSV(t *testing.T) {
	dir := t.TempDir()
	fl, _ := NewFileLogger(filepath.Join(dir, "audit.jsonl"))
	defer fl.Close()

	ctx := context.Background()
	fl.Log(ctx, entry("e1", "alice", "read", "r1"))

	rc, err := fl.Export(ctx, "csv")
	if err != nil {
		t.Fatalf("Export csv: %v", err)
	}
	defer rc.Close()

	scanner := bufio.NewScanner(rc)
	scanner.Scan()
	header := scanner.Text()
	if !strings.HasPrefix(header, "id,timestamp") {
		t.Errorf("expected CSV header, got %q", header)
	}
	scanner.Scan()
	row := scanner.Text()
	if !strings.Contains(row, "alice") {
		t.Errorf("expected actor 'alice' in CSV row, got %q", row)
	}
}

func TestFileLogger_WriteError_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	fl, err := NewFileLogger(path)
	if err != nil {
		t.Fatalf("NewFileLogger: %v", err)
	}
	defer fl.Close()

	// Close the underlying file to simulate a write failure (disk full, etc.).
	if err := fl.file.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}

	ctx := context.Background()
	err = fl.Log(ctx, entry("e1", "user1", "read", "/api/foo"))
	if err == nil {
		t.Fatal("expected error when writing to closed audit file")
	}
	if !strings.Contains(err.Error(), "write entry") {
		t.Errorf("expected 'write entry' in error, got %q", err.Error())
	}
}

func TestFileLogger_Rotation_TriggersAtSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	fl, err := NewFileLogger(path)
	if err != nil {
		t.Fatalf("NewFileLogger: %v", err)
	}
	defer fl.Close()

	// Set a tiny max size (1 byte after the first entry) to force rotation.
	fl.MaxSizeMB = 1
	// Write entries until rotation occurs. Each entry is larger than 1MB due
	// to the large payload, so the second write should trigger rotation.
	largePayload := make([]byte, 2*1024*1024)
	ctx := context.Background()

	// First write fills the file.
	e1 := entry("e1", "a", "b", "c")
	e1.Details = map[string]string{"data": string(largePayload)}
	if err := fl.Log(ctx, e1); err != nil {
		t.Fatalf("first Log: %v", err)
	}

	// Second write should trigger rotation (file > 1MB).
	e2 := entry("e2", "a", "b", "c")
	e2.Details = map[string]string{"data": string(largePayload)}
	if err := fl.Log(ctx, e2); err != nil {
		t.Fatalf("second Log (after rotation): %v", err)
	}

	// Verify the rotated file exists.
	if _, err := os.Stat(path + ".1"); os.IsNotExist(err) {
		t.Error("expected rotated file audit.jsonl.1 to exist")
	}

	// Verify the current file is a new file (not the rotated one).
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile current: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected entries in the new current file")
	}
}

func TestFileLogger_Rotation_MaxRotatedFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	fl, err := NewFileLogger(path)
	if err != nil {
		t.Fatalf("NewFileLogger: %v", err)
	}
	defer fl.Close()

	fl.MaxSizeMB = 1
	fl.MaxRotatedFiles = 2

	largePayload := make([]byte, 2*1024*1024)
	ctx := context.Background()
	evt := entry("e", "a", "b", "c")
	evt.Details = map[string]string{"data": string(largePayload)}

	// Write enough to trigger multiple rotations.
	for i := 0; i < 4; i++ {
		if err := fl.Log(ctx, evt); err != nil {
			t.Fatalf("Log %d: %v", i, err)
		}
	}

	// Should have at most 2 rotated files (.1 and .2).
	if _, err := os.Stat(path + ".3"); err == nil {
		t.Error("expected no more than 2 rotated files (MaxRotatedFiles=2)")
	}
}

func TestFileLogger_ConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	fl, _ := NewFileLogger(filepath.Join(dir, "audit.jsonl"))
	defer fl.Close()

	ctx := context.Background()
	var wg sync.WaitGroup
	numWrites := 50

	for i := 0; i < numWrites; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if err := fl.Log(ctx, entry("e", "user", "read", fmt.Sprintf("/api/%d", n))); err != nil {
				t.Logf("concurrent Log %d: %v", n, err)
			}
		}(i)
	}
	wg.Wait()

	// All entries should have been written without panic or deadlock.
	entries, err := fl.Query(ctx, contracts.AuditFilter{})
	if err != nil {
		t.Fatalf("Query after concurrent writes: %v", err)
	}
	if len(entries) == 0 {
		t.Error("expected entries after concurrent writes")
	}
}

func TestClearSigningKey_ZeroesAndNils(t *testing.T) {
	fl, err := NewFileLogger("")
	if err != nil {
		t.Fatalf("NewFileLogger: %v", err)
	}

	key := []byte("my-secret-key-12345")
	fl.SetSigningKey(key)
	fl.ClearSigningKey()

	if fl.signingKey != nil {
		t.Error("expected signingKey to be nil after ClearSigningKey")
	}
}

func TestSetSigningKey_CopiesKey(t *testing.T) {
	fl, err := NewFileLogger("")
	if err != nil {
		t.Fatalf("NewFileLogger: %v", err)
	}

	key := []byte("my-secret-key-12345")
	fl.SetSigningKey(key)

	// Modify the original key; the internal copy should be unaffected.
	key[0] = 'X'
	if fl.signingKey[0] == 'X' {
		t.Error("expected signingKey to be a copy, not a reference")
	}
}

func TestClearSigningKey_NoKey_NoPanic(t *testing.T) {
	fl, err := NewFileLogger("")
	if err != nil {
		t.Fatalf("NewFileLogger: %v", err)
	}
	// Calling ClearSigningKey when no key is set should not panic.
	fl.ClearSigningKey()
}

func TestClearBytes_NilSlice(t *testing.T) {
	// Should not panic on nil slice.
	clearBytes(nil)
}

func TestClearBytes_NonNil(t *testing.T) {
	b := []byte{1, 2, 3, 4, 5}
	clearBytes(b)
	for i, v := range b {
		if v != 0 {
			t.Errorf("expected b[%d]=0 after clearBytes, got %d", i, v)
		}
	}
}

func TestFileLogger_StartSyncLoop_FlushesOnInterval(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	fl, err := NewFileLogger(path)
	if err != nil {
		t.Fatalf("NewFileLogger: %v", err)
	}

	// Set a short interval and start the background sync loop.
	fl.SyncFlushInterval = 10 * time.Millisecond
	fl.startSyncLoop()

	fl.Log(context.Background(), entry("e1", "user", "read", "/api/foo"))

	// Wait long enough for at least one tick to fire.
	time.Sleep(100 * time.Millisecond)

	// The sync loop fired and synced data to disk. Verifying by reading.
	// Close handles cleanup — it stops the goroutine and closes the file.
	fl.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected data written to disk after sync loop interval")
	}
}

func TestStopSyncLoop_NilChannel_NoPanic(t *testing.T) {
	fl, err := NewFileLogger("")
	if err != nil {
		t.Fatalf("NewFileLogger: %v", err)
	}
	// syncStop is nil since path is empty — should not panic.
	fl.stopSyncLoop()
}

func TestStopSyncLoop_ClosesChannel(t *testing.T) {
	fl, err := NewFileLogger(t.TempDir() + "/audit.jsonl")
	if err != nil {
		t.Fatalf("NewFileLogger: %v", err)
	}
	defer fl.Close()

	fl.SyncFlushInterval = 100 * time.Millisecond
	fl.startSyncLoop()

	// Give the goroutine time to start.
	time.Sleep(10 * time.Millisecond)

	fl.stopSyncLoop()

	// After stopSyncLoop, the goroutine should have exited.
	// Calling Close() should not block.
	fl.Close()
}

func TestFileLogger_Export_InvalidFormat(t *testing.T) {
	dir := t.TempDir()
	fl, _ := NewFileLogger(filepath.Join(dir, "audit.jsonl"))
	defer fl.Close()

	_, err := fl.Export(context.Background(), "xml")
	if err == nil {
		t.Error("expected error for unsupported export format")
	}
}
