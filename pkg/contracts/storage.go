package contracts

import (
	"context"
	"io"
)

type ObjectInfo struct {
	Key          string
	Size         int64
	ContentType  string
	ETag         string
	LastModified int64
	Metadata     map[string]string
}

// Core blob storage interface — all storage providers implement this.
type StorageProvider interface {
	Put(ctx context.Context, key string, data io.Reader, size int64) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	Move(ctx context.Context, src, dst string) error
	Exists(ctx context.Context, key string) (bool, error)
	Stat(ctx context.Context, key string) (ObjectInfo, error)
	List(ctx context.Context, prefix string) ([]ObjectInfo, error)
}

// Capability interfaces allow modules to negotiate features at runtime.

type Streamable interface {
	Stream(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error)
}

type Seekable interface {
	Seek(ctx context.Context, key string, offset int64) (int64, error)
}

type Watchable interface {
	Watch(ctx context.Context, prefix string) (<-chan StorageEvent, error)
}

type AtomicMovable interface {
	AtomicMove(ctx context.Context, src, dst string) error
}

type Hardlinkable interface {
	Hardlink(ctx context.Context, src, dst string) error
}

type StorageEventType string

const (
	StorageEventCreated StorageEventType = "created"
	StorageEventDeleted  StorageEventType = "deleted"
	StorageEventModified StorageEventType = "modified"
)

type StorageEvent struct {
	Type StorageEventType
	Key  string
}
