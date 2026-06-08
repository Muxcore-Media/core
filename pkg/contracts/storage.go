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

// StorageOrchestrator is the high-level storage interface exposed to modules.
// It handles provider routing, caching, and capability negotiation internally.
type StorageOrchestrator interface {
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Put(ctx context.Context, key string, data io.Reader, size int64) error
	Delete(ctx context.Context, key string) error
	Move(ctx context.Context, src, dst string) error
	Exists(ctx context.Context, key string) (bool, error)
	Stat(ctx context.Context, key string) (ObjectInfo, error)
	List(ctx context.Context, prefix string) ([]ObjectInfo, error)
	ProviderCount() int
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
	StorageEventCreated  StorageEventType = "created"
	StorageEventDeleted  StorageEventType = "deleted"
	StorageEventModified StorageEventType = "modified"
)

type StorageEvent struct {
	Type StorageEventType
	Key  string
}

// EventStorageTierTransition is emitted when an object moves between tiers.
const EventStorageTierTransition = "storage.tier.transition"

// Storage tier constants.
type StorageTier string

const (
	StorageTierHot     StorageTier = "hot"
	StorageTierWarm    StorageTier = "warm"
	StorageTierCold    StorageTier = "cold"
	StorageTierArchive StorageTier = "archive"
)

// TieredProvider extends StorageProvider with tiering support.
// Storage modules that support multiple tiers implement this.
type TieredProvider interface {
	StorageProvider

	// Tier returns which tier this provider handles.
	Tier() StorageTier

	// Promote moves an object from this tier to a higher one.
	Promote(ctx context.Context, key string) error

	// Relegate moves an object from this tier to a lower one.
	Relegate(ctx context.Context, key string) error
}

// TierTransitionPayload is the payload for storage.tier.transition events.
type TierTransitionPayload struct {
	Key      string      `json:"key"`
	FromTier StorageTier `json:"from_tier"`
	ToTier   StorageTier `json:"to_tier"`
	Reason   string      `json:"reason"`
}
