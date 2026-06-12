package mock

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// Storage is an in-memory mock of contracts.StorageOrchestrator for testing.
// Data is stored in a map keyed by storage key. Thread-safe.
type Storage struct {
	mu    sync.RWMutex
	data  map[string][]byte
	infos map[string]contracts.ObjectInfo
}

// NewStorage creates an empty in-memory storage mock.
func NewStorage() *Storage {
	return &Storage{
		data:  make(map[string][]byte),
		infos: make(map[string]contracts.ObjectInfo),
	}
}

func (s *Storage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, ok := s.data[key]
	if !ok {
		return nil, fmt.Errorf("key not found: %s", key)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *Storage) Put(ctx context.Context, key string, data io.Reader, size int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	buf := new(bytes.Buffer)
	if _, err := io.Copy(buf, data); err != nil {
		return err
	}
	s.data[key] = buf.Bytes()
	s.infos[key] = contracts.ObjectInfo{Key: key, Size: int64(buf.Len())}
	return nil
}

func (s *Storage) Delete(ctx context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
	delete(s.infos, key)
	return nil
}

func (s *Storage) Move(ctx context.Context, src, dst string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.data[src]
	if !ok {
		return fmt.Errorf("source not found: %s", src)
	}
	s.data[dst] = data
	s.infos[dst] = s.infos[src]
	delete(s.data, src)
	delete(s.infos, src)
	return nil
}

func (s *Storage) Exists(ctx context.Context, key string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.data[key]
	return ok, nil
}

func (s *Storage) Stat(ctx context.Context, key string) (contracts.ObjectInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	info, ok := s.infos[key]
	if !ok {
		return contracts.ObjectInfo{}, fmt.Errorf("key not found: %s", key)
	}
	return info, nil
}

func (s *Storage) List(ctx context.Context, prefix string) ([]contracts.ObjectInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var results []contracts.ObjectInfo
	for key, info := range s.infos {
		if prefix == "" || len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			results = append(results, info)
		}
	}
	return results, nil
}

func (s *Storage) Stream(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	data, err := s.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer data.Close()
	buf := new(bytes.Buffer)
	if _, err := io.Copy(buf, data); err != nil {
		return nil, fmt.Errorf("mock storage: read data: %w", err)
	}
	b := buf.Bytes()
	if offset >= int64(len(b)) {
		return io.NopCloser(bytes.NewReader(nil)), nil
	}
	end := offset + length
	if end > int64(len(b)) {
		end = int64(len(b))
	}
	return io.NopCloser(bytes.NewReader(b[offset:end])), nil
}

func (s *Storage) CapabilityCheck(ctx context.Context, key string) ([]string, error) {
	return []string{"streamable"}, nil
}

var _ contracts.StorageOrchestrator = (*Storage)(nil)
