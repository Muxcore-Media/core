# Contracts & API Reference

## Overview

All module capabilities are defined by **versioned contracts**. Contracts are the interface definition language of MuxCore — they define what a module must implement and what callers can expect.

## Contract Layers

### 1. Go Interfaces (Core SDK)

For Go-native modules. Defined in `pkg/contracts/`.

### 2. Protobuf Definitions (Language Agnostic)

For external modules in any language. Defined in `proto/`.

### 3. OpenAPI Spec (External API)

For the REST API consumed by the UI and external tools.

---

## Core Contracts

### Module (module.go)

```go
type Module interface {
    Info() ModuleInfo
    Init(ctx context.Context) error
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
    Health(ctx context.Context) error
}

type ModuleKind string
const (
    ModuleKindAuth, ModuleKindProvider, ModuleKindDownloader,
    ModuleKindMediaManager, ModuleKindProcessor, ModuleKindPlayback,
    ModuleKindWorkflow, ModuleKindStorage, ModuleKindUI,
    ModuleKindAPI, ModuleKindEventBus, ModuleKindScheduler
)
```

Every module implements this. The core manages the lifecycle.

### ServiceRegistry (module.go)

```go
type ServiceRegistry interface {
    FindByKind(kind ModuleKind) []ModuleEntry
    FindByCapability(cap string) []ModuleEntry
    SupportsCapability(moduleID, cap string) bool
    Resolve(id string) (ModuleEntry, error)
    ListAll() []ModuleEntry
    RegisterMediaSchema(schema MediaTypeSchema) error
    MediaSchema(mediaType MediaType) (MediaTypeSchema, bool)
    MediaSchemas() []MediaTypeSchema
}

type ModuleEntry struct {
    Info   ModuleInfo
    State  ModuleState
    Module Module
}
```

Modules use this to discover each other at runtime. `FindByKind` returns all modules of a given kind. `FindByCapability` returns modules advertising a specific capability (e.g., `"playback.jellyfin"`). `Resolve` looks up a module by ID. `RegisterMediaSchema` lets modules declare metadata fields for their media type; core validates `MediaObject.Fields` against the registered schema. The returned `ModuleEntry.Module` can be type-asserted to a specific capability interface (e.g., `contracts.Downloader`).

### RouteRegistrar (module.go)

```go
type RouteRegistrar interface {
    Handle(pattern string, handler http.Handler)
    HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request))
}
```

Modules register HTTP handlers with the core API server during `Start()`. Core owns the HTTP mux; modules add routes to it.

### ModuleFactory + ModuleDeps (module.go)

```go
type ModuleFactory func(deps ModuleDeps) Module

type ModuleDeps struct {
    Registry ServiceRegistry
    EventBus EventBus
    Routes   RouteRegistrar
}
```

Modules call `contracts.Register(factory)` in their `init()` function. Core calls all registered factories with the runtime dependencies, creating module instances.

### EventBus (events.go)

```go
type EventBus interface {
    Publish(ctx context.Context, event Event) error
    Subscribe(ctx context.Context, eventType string, handler EventHandler) error
    Unsubscribe(ctx context.Context, eventType string, handler EventHandler) error
    Request(ctx context.Context, event Event, timeout time.Duration) (Event, error)
}
```

The central nervous system of the platform.

### StorageProvider (storage.go)

```go
type StorageProvider interface {
    Put(ctx context.Context, key string, data io.Reader, size int64) error
    Get(ctx context.Context, key string) (io.ReadCloser, error)
    Delete(ctx context.Context, key string) error
    Move(ctx context.Context, src, dst string) error
    Exists(ctx context.Context, key string) (bool, error)
    Stat(ctx context.Context, key string) (ObjectInfo, error)
    List(ctx context.Context, prefix string) ([]ObjectInfo, error)
}
```

Base interface all storage providers implement.

### Storage Capabilities (storage.go)

```go
type Streamable interface { Stream(ctx, key string, offset, length int64) (io.ReadCloser, error) }
type Seekable interface { Seek(ctx, key string, offset int64) (int64, error) }
type Watchable interface { Watch(ctx, prefix string) (<-chan StorageEvent, error) }
type AtomicMovable interface { AtomicMove(ctx, src, dst string) error }
type Hardlinkable interface { Hardlink(ctx, src, dst string) error }
```

Optional capability interfaces for storage providers.

### Downloader (downloader.go)

```go
type Downloader interface {
    Add(ctx context.Context, task DownloadTask) (string, error)
    Remove(ctx context.Context, id string, deleteData bool) error
    Pause(ctx context.Context, id string) error
    Resume(ctx context.Context, id string) error
    Status(ctx context.Context, id string) (DownloadInfo, error)
    List(ctx context.Context) ([]DownloadInfo, error)
}
```

### Indexer (indexer.go)

```go
type Indexer interface {
    Name() string
    Search(ctx context.Context, query SearchQuery) ([]IndexerResult, error)
    Capabilities(ctx context.Context) ([]string, error)
}
```

### MediaLibrary (media.go)

```go
type MediaLibrary interface {
    Add(ctx context.Context, obj MediaObject) error
    Remove(ctx context.Context, id string) error
    Get(ctx context.Context, id string) (MediaObject, error)
    List(ctx context.Context, mediaType MediaType, offset, limit int) ([]MediaObject, error)
    Search(ctx context.Context, query string) ([]MediaObject, error)
}
```

### MediaTypeSchema (media.go)

Modules that own a media type declare their metadata schema via `MediaTypeSchemaProvider`. Core validates `MediaObject.Fields` against the registered schema on `Add()`.

```go
type MediaTypeSchema struct {
    MediaType MediaType
    Fields    []MediaFieldSchema
    ModuleID  string
}

type MediaFieldSchema struct {
    Key         string
    Type        MediaFieldType  // string, int, float, bool, string_slice
    Description string
}

type MediaTypeSchemaProvider interface {
    MediaTypeSchema() MediaTypeSchema
}
```

The `MediaObject` struct has a `Fields map[string]any` — modules declare what keys are valid for their media type, core validates on storage, and consumers discover schemas at runtime via `ServiceRegistry.MediaSchema(mediaType)`.

Example: the movie module registers:
```go
Fields: []MediaFieldSchema{
    {Key: "tmdb_id", Type: FieldTypeString},
    {Key: "year",    Type: FieldTypeInt},
    {Key: "quality", Type: FieldTypeString},
}
```

A book module would register `author`, `isbn`, `pages`. A music module would register `artist`, `album`, `track_number`. Core never knows about these fields — it only validates types against the declared schema.

### Scheduler (scheduler.go)

```go
type Scheduler interface {
    Schedule(ctx context.Context, task Task) (string, error)
    Cancel(ctx context.Context, taskID string) error
    Status(ctx context.Context, taskID string) (TaskStatus, error)
}
```

### WorkflowEngine (workflow.go)

```go
type WorkflowEngine interface {
    Define(ctx context.Context, def WorkflowDefinition) error
    Run(ctx context.Context, workflowID string, params map[string]any) (string, error)
    Status(ctx context.Context, runID string) (WorkflowRun, error)
    Cancel(ctx context.Context, runID string) error
}
```

### AuthProvider / Authorizer (auth.go)

```go
type AuthProvider interface {
    Authenticate(ctx context.Context, credentials any) (Session, error)
    Validate(ctx context.Context, token string) (Session, error)
    Revoke(ctx context.Context, token string) error
}

type Authorizer interface {
    Can(ctx context.Context, session Session, action string, resource string) (bool, error)
}
```

### DatabaseProvider (database.go)

```go
type DatabaseProvider interface {
    Open(ctx context.Context, connString string) error
    Close(ctx context.Context) error
    Health(ctx context.Context) error
    Query(ctx context.Context, query string, args ...any) (Rows, error)
    Exec(ctx context.Context, query string, args ...any) (Result, error)
    Transaction(ctx context.Context, fn func(Tx) error) error
    Migrate(ctx context.Context, migrations []Migration) error
}
```

Core defines the contract. Implementations are modules: `database-postgres`, `database-sqlite`, `database-mysql`.

### CacheProvider (cache.go)

```go
type CacheProvider interface {
    Get(ctx context.Context, key string) ([]byte, error)
    Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
    Delete(ctx context.Context, key string) error
    Exists(ctx context.Context, key string) (bool, error)
    Incr(ctx context.Context, key string, delta int64) (int64, error)
    Lock(ctx context.Context, key string, ttl time.Duration) (Lock, error)
    Publish(ctx context.Context, channel string, msg []byte) error
    Subscribe(ctx context.Context, channel string) (<-chan []byte, error)
}
```

Core defines the contract. Implementations are modules: `cache-redis`, `cache-valkey`, `cache-memcached`.

---

## Protobuf Contracts

Protobuf definitions planned for Phase 3 (language-agnostic module SDK).

---

## Contract Versioning

*(planned)* Contracts follow **semantic versioning**:

- **v1.0.0** → `Downloader/v1`
- **v1.1.0** → `Downloader/v1` (backwards compatible)
- **v2.0.0** → `Downloader/v2` (breaking change)

Compatibility enforcement is planned for the service registry.

## SDK

The **Go SDK** (`sdk/go/`) is reserved for future SDK tooling. Currently, modules import contracts directly from `pkg/contracts/`.

Future SDKs planned for:
- TypeScript/JavaScript (UI plugins)
- Python (AI/ML modules)
- Rust (performance-critical modules)
