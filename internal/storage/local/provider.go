// Package local provides a filesystem-backed StorageProvider for MuxCore.
// Each object is stored as a file under a base directory. Metadata is stored
// alongside as a .meta.json file. Thread-safe.
package local

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// Provider implements contracts.StorageProvider on the local filesystem.
// Objects are stored at {base}/{encoded_key} where encoded_key is the
// storage key with path separators escaped to prevent traversal.
type Provider struct {
	base string
}

// New creates a Provider that stores objects under baseDir.
// The directory is created if it does not exist.
func New(baseDir string) (*Provider, error) {
	abs, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, fmt.Errorf("local storage: resolve base dir: %w", err)
	}
	if err := os.MkdirAll(abs, 0700); err != nil {
		return nil, fmt.Errorf("local storage: create base dir: %w", err)
	}
	return &Provider{base: abs}, nil
}

// encodeKey converts a storage key to a safe filesystem path segment.
// Uses URL-style percent encoding for '/' and other unsafe characters.
func encodeKey(key string) string {
	safe := strings.NewReplacer(
		"/", "%2F",
		"\\", "%5C",
		"..", "%2E%2E",
		"\x00", "%00",
	).Replace(key)
	return safe
}

// decodeKey reverses encodeKey.
func decodeKey(encoded string) string {
	unsafe := strings.NewReplacer(
		"%2F", "/",
		"%5C", "\\",
		"%2E%2E", "..",
		"%00", "\x00",
	).Replace(encoded)
	return unsafe
}

func (p *Provider) objPath(key string) string {
	return filepath.Join(p.base, encodeKey(key))
}

func (p *Provider) metaPath(key string) string {
	return p.objPath(key) + ".meta.json"
}

// Put implements contracts.StorageProvider.
func (p *Provider) Put(_ context.Context, key string, data io.Reader, size int64) error {
	if key == "" {
		return fmt.Errorf("local: empty key")
	}
	dst := p.objPath(key)
	// Write to a temp file in the same directory, then rename for atomicity.
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("local: mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("local: create temp: %w", err)
	}
	tmpName := tmp.Name()
	cleaned := false
	defer func() {
		if !cleaned {
			os.Remove(tmpName) //nolint:errcheck
		}
	}()

	if _, err := io.Copy(tmp, data); err != nil {
		tmp.Close() //nolint:errcheck
		return fmt.Errorf("local: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("local: close temp: %w", err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return fmt.Errorf("local: rename: %w", err)
	}
	cleaned = true

	// Write metadata.
	info := contracts.ObjectInfo{
		Key:          key,
		Size:         size,
		LastModified: time.Now(),
	}
	if size <= 0 {
		if fi, err := os.Stat(dst); err == nil {
			info.Size = fi.Size()
		}
	}
	if err := p.writeMeta(key, info); err != nil {
		return fmt.Errorf("local: write meta %q: %w", p.metaPath(key), err)
	}
	return nil
}

func (p *Provider) writeMeta(key string, info contracts.ObjectInfo) error {
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	return os.WriteFile(p.metaPath(key), data, 0600)
}

func (p *Provider) readMeta(key string) (contracts.ObjectInfo, error) {
	data, err := os.ReadFile(p.metaPath(key))
	if err != nil {
		if os.IsNotExist(err) {
			// Build minimal info from file stat.
			fi, stErr := os.Stat(p.objPath(key))
			if stErr != nil {
				return contracts.ObjectInfo{}, contracts.ErrNotFound
			}
			return contracts.ObjectInfo{
				Key:          key,
				Size:         fi.Size(),
				LastModified: fi.ModTime(),
			}, nil
		}
		return contracts.ObjectInfo{}, fmt.Errorf("local: read meta %q: %w", p.metaPath(key), err)
	}
	var info contracts.ObjectInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return contracts.ObjectInfo{}, fmt.Errorf("local: unmarshal meta %q: %w", p.metaPath(key), err)
	}
	info.Key = key
	return info, nil
}

// Get implements contracts.StorageProvider.
func (p *Provider) Get(_ context.Context, key string) (io.ReadCloser, error) {
	if key == "" {
		return nil, fmt.Errorf("local: empty key")
	}
	f, err := os.Open(p.objPath(key))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, contracts.ErrNotFound
		}
		return nil, fmt.Errorf("local: open %q: %w", p.objPath(key), err)
	}
	return f, nil
}

// Delete implements contracts.StorageProvider.
func (p *Provider) Delete(_ context.Context, key string) error {
	if key == "" {
		return fmt.Errorf("local: empty key")
	}
	err1 := os.Remove(p.objPath(key))
	err2 := os.Remove(p.metaPath(key))
	if err1 != nil && os.IsNotExist(err1) {
		return contracts.ErrNotFound
	}
	if err1 != nil {
		return fmt.Errorf("local: delete object %q: %w", key, err1)
	}
	if err2 != nil && !os.IsNotExist(err2) {
		return fmt.Errorf("local: delete meta %q: %w", key, err2)
	}
	return nil
}

// Move implements contracts.StorageProvider.
func (p *Provider) Move(_ context.Context, src, dst string) error {
	if src == "" || dst == "" {
		return fmt.Errorf("local: empty key")
	}
	if err := os.Rename(p.objPath(src), p.objPath(dst)); err != nil {
		if os.IsNotExist(err) {
			return contracts.ErrNotFound
		}
		return fmt.Errorf("local: rename %q to %q: %w", src, dst, err)
	}
	// Move metadata.
	os.Rename(p.metaPath(src), p.metaPath(dst)) //nolint:errcheck // best-effort
	return nil
}

// Exists implements contracts.StorageProvider.
func (p *Provider) Exists(_ context.Context, key string) (bool, error) {
	if key == "" {
		return false, fmt.Errorf("local: empty key")
	}
	_, err := os.Stat(p.objPath(key))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Stat implements contracts.StorageProvider.
func (p *Provider) Stat(_ context.Context, key string) (contracts.ObjectInfo, error) {
	if key == "" {
		return contracts.ObjectInfo{}, fmt.Errorf("local: empty key")
	}
	return p.readMeta(key)
}

// List implements contracts.StorageProvider.
func (p *Provider) List(_ context.Context, prefix string) ([]contracts.ObjectInfo, error) {
	encodedPrefix := encodeKey(prefix)
	var results []contracts.ObjectInfo

	walkFn := func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable
		}
		if d.IsDir() {
			return nil
		}
		// Only match data files, not .meta.json files.
		if strings.HasSuffix(path, ".meta.json") {
			return nil
		}
		rel, err := filepath.Rel(p.base, path)
		if err != nil {
			return nil
		}
		encodedKey := rel
		if !strings.HasPrefix(encodedKey, encodedPrefix) {
			return nil
		}
		key := decodeKey(encodedKey)
		info, err := p.readMeta(key)
		if err != nil {
			return nil
		}
		results = append(results, info)
		return nil
	}

	if err := filepath.WalkDir(p.base, walkFn); err != nil {
		return nil, fmt.Errorf("local: list: %w", err)
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Key < results[j].Key
	})
	return results, nil
}

// Stream implements contracts.Streamable for byte-range reads.
func (p *Provider) Stream(_ context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	if key == "" {
		return nil, fmt.Errorf("local: empty key")
	}
	f, err := os.Open(p.objPath(key))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, contracts.ErrNotFound
		}
		return nil, fmt.Errorf("local: open %q for stream: %w", key, err)
	}
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			f.Close()
			return nil, fmt.Errorf("local: seek %q: %w", key, err)
		}
	}
	if length > 0 {
		return io.NopCloser(io.LimitReader(f, length)), nil
	}
	return f, nil
}

// Close implements io.Closer for cleanup.
func (p *Provider) Close() error { return nil }

// Ensure Provider implements the required interfaces.
var _ contracts.StorageProvider = (*Provider)(nil)
var _ contracts.Streamable = (*Provider)(nil)

// --- Convenience constructor for in-memory testing ---

// NewInMemory creates a Provider backed by a temporary directory.
// The directory is cleaned up on Close. Useful for tests.
func NewInMemory() (*Provider, error) {
	dir, err := os.MkdirTemp("", "muxcore-local-storage-*")
	if err != nil {
		return nil, fmt.Errorf("local: create temp dir: %w", err)
	}
	p := &Provider{base: dir}
	return p, nil
}
