package audit

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

	data, err := os.ReadFile(path)
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
	f, _ := os.Open(filepath.Join(dir, "audit.jsonl"))
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

	f, _ := os.Open(filepath.Join(dir, "audit.jsonl"))
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

func TestFileLogger_Export_InvalidFormat(t *testing.T) {
	dir := t.TempDir()
	fl, _ := NewFileLogger(filepath.Join(dir, "audit.jsonl"))
	defer fl.Close()

	_, err := fl.Export(context.Background(), "xml")
	if err == nil {
		t.Error("expected error for unsupported export format")
	}
}
