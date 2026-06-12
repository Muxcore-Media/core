package local

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// --- Interface Compliance ---

var _ contracts.StorageProvider = (*Provider)(nil)
var _ contracts.Streamable = (*Provider)(nil)

// --- Helpers ---

func setupProvider(t *testing.T) *Provider {
	t.Helper()
	p, err := NewInMemory()
	if err != nil {
		t.Fatalf("NewInMemory: %v", err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

func mustPut(t *testing.T, p *Provider, key, content string) {
	t.Helper()
	err := p.Put(context.Background(), key, strings.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatalf("Put(%q): %v", key, err)
	}
}

func mustGet(t *testing.T, p *Provider, key string) string {
	t.Helper()
	rc, err := p.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll(%q): %v", key, err)
	}
	return string(data)
}

// --- Constructor Tests ---

func TestNew(t *testing.T) {
	t.Run("creates directory", func(t *testing.T) {
		dir := t.TempDir()
		base := filepath.Join(dir, "nested", "path")
		p, err := New(base)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		defer p.Close()
		if _, err := os.Stat(base); os.IsNotExist(err) {
			t.Fatal("directory was not created")
		}
	})

	t.Run("error on invalid path", func(t *testing.T) {
		_, err := New("\x00invalid")
		if err == nil {
			t.Fatal("expected error for invalid path")
		}
	})
}

func TestNewInMemory(t *testing.T) {
	t.Run("creates usable provider", func(t *testing.T) {
		p, err := NewInMemory()
		if err != nil {
			t.Fatalf("NewInMemory: %v", err)
		}
		defer p.Close()
		mustPut(t, p, "hello", "world")
		if got := mustGet(t, p, "hello"); got != "world" {
			t.Fatalf("got %q, want %q", got, "world")
		}
	})
}

// --- Basic Operations ---

func TestPutGet(t *testing.T) {
	p := setupProvider(t)
	ctx := context.Background()

	t.Run("round-trip", func(t *testing.T) {
		content := "hello world"
		mustPut(t, p, "test-key", content)
		got := mustGet(t, p, "test-key")
		if got != content {
			t.Fatalf("got %q, want %q", got, content)
		}
	})

	t.Run("empty content", func(t *testing.T) {
		mustPut(t, p, "empty", "")
		got := mustGet(t, p, "empty")
		if got != "" {
			t.Fatalf("got %q, want empty", got)
		}
	})

	t.Run("large content", func(t *testing.T) {
		data := strings.Repeat("A", 1<<16)
		mustPut(t, p, "large", data)
		got := mustGet(t, p, "large")
		if got != data {
			t.Fatalf("large round-trip failed: len(got)=%d, len(want)=%d", len(got), len(data))
		}
	})

	t.Run("binary content", func(t *testing.T) {
		binary := []byte{0x00, 0x01, 0x02, 0xFF, 0xFE}
		err := p.Put(ctx, "binary", bytes.NewReader(binary), int64(len(binary)))
		if err != nil {
			t.Fatalf("Put binary: %v", err)
		}
		rc, err := p.Get(ctx, "binary")
		if err != nil {
			t.Fatalf("Get binary: %v", err)
		}
		defer rc.Close()
		got, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		if !bytes.Equal(got, binary) {
			t.Fatalf("binary mismatch: got %v, want %v", got, binary)
		}
	})

	t.Run("non-existent key", func(t *testing.T) {
		_, err := p.Get(ctx, "nonexistent")
		if !errors.Is(err, contracts.ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
	})
}

func TestPutSizeAutoDetect(t *testing.T) {
	p := setupProvider(t)

	t.Run("size zero auto-detects", func(t *testing.T) {
		data := "auto-size"
		err := p.Put(context.Background(), "autosize", strings.NewReader(data), 0)
		if err != nil {
			t.Fatalf("Put with size=0: %v", err)
		}
		info, err := p.Stat(context.Background(), "autosize")
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if info.Size != int64(len(data)) {
			t.Fatalf("size: got %d, want %d", info.Size, len(data))
		}
	})

	t.Run("negative size auto-detects", func(t *testing.T) {
		data := "negative-size"
		err := p.Put(context.Background(), "negsize", strings.NewReader(data), -1)
		if err != nil {
			t.Fatalf("Put with size=-1: %v", err)
		}
		info, err := p.Stat(context.Background(), "negsize")
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if info.Size != int64(len(data)) {
			t.Fatalf("size: got %d, want %d", info.Size, len(data))
		}
	})
}

func TestDelete(t *testing.T) {
	p := setupProvider(t)
	ctx := context.Background()

	t.Run("existing key", func(t *testing.T) {
		mustPut(t, p, "todelete", "data")
		err := p.Delete(ctx, "todelete")
		if err != nil {
			t.Fatalf("Delete: %v", err)
		}
		_, err = p.Get(ctx, "todelete")
		if !errors.Is(err, contracts.ErrNotFound) {
			t.Fatalf("want ErrNotFound after delete, got %v", err)
		}
	})

	t.Run("non-existing key", func(t *testing.T) {
		err := p.Delete(ctx, "does-not-exist")
		if !errors.Is(err, contracts.ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
	})

	t.Run("idempotent", func(t *testing.T) {
		mustPut(t, p, "idemp", "data")
		if err := p.Delete(ctx, "idemp"); err != nil {
			t.Fatal(err)
		}
		err := p.Delete(ctx, "idemp")
		if !errors.Is(err, contracts.ErrNotFound) {
			t.Fatalf("second delete should return ErrNotFound, got %v", err)
		}
	})
}

func TestExists(t *testing.T) {
	p := setupProvider(t)
	ctx := context.Background()

	t.Run("key exists", func(t *testing.T) {
		mustPut(t, p, "exists-key", "data")
		ok, err := p.Exists(ctx, "exists-key")
		if err != nil {
			t.Fatalf("Exists: %v", err)
		}
		if !ok {
			t.Fatal("expected true")
		}
	})

	t.Run("key does not exist", func(t *testing.T) {
		ok, err := p.Exists(ctx, "no-such-key")
		if err != nil {
			t.Fatalf("Exists: %v", err)
		}
		if ok {
			t.Fatal("expected false")
		}
	})
}

func TestList(t *testing.T) {
	p := setupProvider(t)
	ctx := context.Background()

	keys := []string{"a/1", "a/2", "b/1", "c/1"}
	for _, k := range keys {
		mustPut(t, p, k, "data")
	}

	t.Run("all keys", func(t *testing.T) {
		infos, err := p.List(ctx, "")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(infos) != len(keys) {
			t.Fatalf("got %d results, want %d", len(infos), len(keys))
		}
		for i, info := range infos {
			if info.Key != keys[i] {
				t.Fatalf("result[%d].Key = %q, want %q", i, info.Key, keys[i])
			}
		}
	})

	t.Run("with prefix", func(t *testing.T) {
		infos, err := p.List(ctx, "a/")
		if err != nil {
			t.Fatalf("List(a/): %v", err)
		}
		if len(infos) != 2 {
			t.Fatalf("got %d results, want 2", len(infos))
		}
	})

	t.Run("non-matching prefix", func(t *testing.T) {
		infos, err := p.List(ctx, "z/")
		if err != nil {
			t.Fatalf("List(z/): %v", err)
		}
		if len(infos) != 0 {
			t.Fatalf("got %d results, want 0", len(infos))
		}
	})
}

func TestMove(t *testing.T) {
	p := setupProvider(t)
	ctx := context.Background()

	t.Run("within provider", func(t *testing.T) {
		mustPut(t, p, "src", "move-data")
		err := p.Move(ctx, "src", "dst")
		if err != nil {
			t.Fatalf("Move: %v", err)
		}
		if got := mustGet(t, p, "dst"); got != "move-data" {
			t.Fatalf("got %q, want %q", got, "move-data")
		}
		_, err = p.Get(ctx, "src")
		if !errors.Is(err, contracts.ErrNotFound) {
			t.Fatal("source should not exist after move")
		}
	})

	t.Run("non-existent source", func(t *testing.T) {
		err := p.Move(ctx, "no-src", "some-dst")
		if !errors.Is(err, contracts.ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
	})
}

func TestStat(t *testing.T) {
	p := setupProvider(t)
	ctx := context.Background()

	t.Run("returns object info", func(t *testing.T) {
		mustPut(t, p, "stat-key", "stat-data")
		info, err := p.Stat(ctx, "stat-key")
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if info.Key != "stat-key" {
			t.Fatalf("Key = %q, want %q", info.Key, "stat-key")
		}
		if info.Size != 9 {
			t.Fatalf("Size = %d, want 9", info.Size)
		}
		if info.LastModified.IsZero() {
			t.Fatal("LastModified should not be zero")
		}
	})

	t.Run("non-existent key", func(t *testing.T) {
		_, err := p.Stat(ctx, "no-stat-key")
		if !errors.Is(err, contracts.ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
	})
}

func TestStream(t *testing.T) {
	p := setupProvider(t)
	ctx := context.Background()
	content := "0123456789"
	mustPut(t, p, "stream-key", content)

	t.Run("full read", func(t *testing.T) {
		rc, err := p.Stream(ctx, "stream-key", 0, 0)
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}
		defer rc.Close()
		data, _ := io.ReadAll(rc)
		if string(data) != content {
			t.Fatalf("got %q, want %q", string(data), content)
		}
	})

	t.Run("offset only", func(t *testing.T) {
		rc, err := p.Stream(ctx, "stream-key", 3, 0)
		if err != nil {
			t.Fatalf("Stream(offset=3): %v", err)
		}
		defer rc.Close()
		data, _ := io.ReadAll(rc)
		if string(data) != "3456789" {
			t.Fatalf("got %q, want %q", string(data), "3456789")
		}
	})

	t.Run("offset with length", func(t *testing.T) {
		rc, err := p.Stream(ctx, "stream-key", 2, 4)
		if err != nil {
			t.Fatalf("Stream(offset=2, length=4): %v", err)
		}
		defer rc.Close()
		data, _ := io.ReadAll(rc)
		if string(data) != "2345" {
			t.Fatalf("got %q, want %q", string(data), "2345")
		}
	})

	t.Run("non-existent key", func(t *testing.T) {
		_, err := p.Stream(ctx, "no-stream", 0, 0)
		if !errors.Is(err, contracts.ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
	})
}

// --- Key Encoding Edge Cases ---

func TestKeyEncoding(t *testing.T) {
	p := setupProvider(t)
	ctx := context.Background()

	tests := []struct {
		name string
		key  string
	}{
		{"slashes", "a/b/c/d"},
		{"backslashes", "a\\b\\c"},
		{"dotdot", "../etc/passwd"},
		{"mixed seps", "a/b\\c/../d"},
		{"special chars", "hello world!@#$%^&*()"},
		{"unicode", "こんにちは/世界"},
		{"leading slash", "/leading"},
		{"trailing slash", "trailing/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := "content"
			mustPut(t, p, tt.key, data)
			if got := mustGet(t, p, tt.key); got != data {
				t.Fatalf("got %q, want %q", got, data)
			}
			ok, err := p.Exists(ctx, tt.key)
			if err != nil {
				t.Fatalf("Exists: %v", err)
			}
			if !ok {
				t.Fatal("Exists returned false")
			}
		})
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	keys := []string{
		"simple",
		"a/b",
		"a\\b",
		"../escape",
		"a/b/c/d/e",
		"\x00null",
		"a/b/../c",
	}
	for _, key := range keys {
		encoded := encodeKey(key)
		decoded := decodeKey(encoded)
		if decoded != key {
			t.Errorf("encode/decode round-trip failed for %q: got %q", key, decoded)
		}
	}
}

func TestEmptyKey(t *testing.T) {
	p := setupProvider(t)
	ctx := context.Background()

	t.Run("put returns error", func(t *testing.T) {
		err := p.Put(ctx, "", strings.NewReader("data"), 4)
		if err == nil {
			t.Fatal("expected error for empty key")
		}
	})

	t.Run("get returns error", func(t *testing.T) {
		_, err := p.Get(ctx, "")
		if err == nil {
			t.Fatal("expected error for empty key")
		}
	})

	t.Run("exists returns error", func(t *testing.T) {
		_, err := p.Exists(ctx, "")
		if err == nil {
			t.Fatal("expected error for empty key")
		}
	})
}

// --- Atomic Write Integrity ---

func TestAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	p, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	mustPut(t, p, "atomic", "original data")

	t.Run("no temp files left behind", func(t *testing.T) {
		matches, _ := filepath.Glob(filepath.Join(dir, ".tmp-*"))
		if len(matches) != 0 {
			t.Fatalf("found %d temp files, want 0", len(matches))
		}
	})

	t.Run("file content correct", func(t *testing.T) {
		data, err := os.ReadFile(p.objPath("atomic"))
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if string(data) != "original data" {
			t.Fatalf("got %q, want %q", string(data), "original data")
		}
	})
}

// --- Concurrent Access ---

func TestConcurrentAccess(t *testing.T) {
	p := setupProvider(t)
	ctx := context.Background()
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := "concurrent-key"
			err := p.Put(ctx, key, strings.NewReader("data"), 4)
			if err != nil {
				t.Errorf("concurrent Put: %v", err)
			}
		}(i)
	}

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rc, err := p.Get(ctx, "concurrent-key")
			if err == nil {
				rc.Close()
			}
		}()
	}

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.Delete(ctx, "concurrent-key")
		}()
	}

	wg.Wait()

	t.Run("list after concurrent ops", func(t *testing.T) {
		_, err := p.List(ctx, "")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
	})
}

func TestConcurrentDifferentKeys(t *testing.T) {
	p := setupProvider(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	n := 50

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			key := fmt.Sprintf("ckey/%d", idx)
			data := "data"
			err := p.Put(ctx, key, strings.NewReader(data), int64(len(data)))
			if err != nil {
				t.Errorf("Put: %v", err)
			}
		}(i)
	}
	wg.Wait()

	infos, err := p.List(ctx, "ckey/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != n {
		t.Fatalf("got %d keys, want %d", len(infos), n)
	}
}

func TestConcurrentStream(t *testing.T) {
	p := setupProvider(t)
	ctx := context.Background()
	content := strings.Repeat("ABCDEFGH", 1024)
	mustPut(t, p, "concurrent-stream", content)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rc, err := p.Stream(ctx, "concurrent-stream", 0, 1024)
			if err != nil {
				t.Errorf("Stream: %v", err)
				return
			}
			defer rc.Close()
			io.ReadAll(rc)
		}()
	}
	wg.Wait()
}

// --- Close ---

func TestClose(t *testing.T) {
	p := setupProvider(t)
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
