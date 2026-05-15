# Module System

## Overview

Every capability in MuxCore is a **module** behind a **contract**. The core provides only orchestration — modules provide all functionality.

## Module Lifecycle

```
Register → Init → Start → Running → Stopping → Stopped
                           ↓
                        Degraded (partial failure, self-healing)
```

### States

| State | Description |
|-------|-------------|
| `registered` | Module discovered, metadata loaded |
| `starting` | Module initializing, not yet serving |
| `running` | Module healthy and serving |
| `degraded` | Module running but with reduced capabilities |
| `stopping` | Module gracefully shutting down |
| `stopped` | Module terminated |

### Lifecycle Hooks

```go
type Module interface {
    Info() ModuleInfo       // Static metadata
    Init(ctx) error         // One-time setup, dependency resolution
    Start(ctx) error        // Begin serving
    Stop(ctx) error         // Graceful shutdown
    Health(ctx) error       // Health check
}
```

## Module Registration

Modules register with the **Service Registry** on startup:

1. Module announces itself (or is discovered by the registry)
2. Registry validates the module's contract compliance
3. Registry advertises the module's capabilities
4. Other modules discover it via the registry

## Communication Methods

### Events (NATS pub/sub)
```
Module publishes "download.completed" → any subscriber can react
```

### Request/Reply (NATS or gRPC)
```
Module requests status → target module responds directly
```

### gRPC (direct service-to-service)
```
For high-throughput or streaming scenarios
```

## Discovery

Modules discover each other through the Service Registry:

```go
// Module A discovers all downloader modules
services, err := registry.Discover(ctx, contracts.ModuleKindDownloader)
// Returns: [downloader-qbittorrent:1.0.0, downloader-native:0.5.0]
```

## Versioning & Compatibility

- All modules follow **semantic versioning** (MAJOR.MINOR.PATCH)
- Contracts are **versioned** (e.g., `Downloader/v1`)
- The registry maintains a **compatibility matrix**
- Modules can **negotiate capabilities** at connection time

### Capability Negotiation

When Module A connects to Module B, they negotiate:

```
Module A: "I support Downloader/v1, StreamingDownloader/v2"
Module B: "I support Downloader/v1"
→ They agree on Downloader/v1
```

## Module Isolation

### Embedded Modules
- Run in-process as Go packages
- Fastest path, shared memory
- Used for core-like modules (auth, registry)

### External Modules (Recommended)
- Run as separate processes or containers
- Communicate over gRPC/NATS
- Crash-isolated, independently updatable
- Each gets its own Docker container for mobility

### Remote Modules
- Run on different machines entirely
- Same communication protocols
- Discovery via the service mesh
- Enables distributed transcoding pools, remote storage, etc.

## Module Security

- Each module gets **scoped API tokens**
- Modules declare required permissions in their manifest
- RBAC policies govern what modules can access
- External modules run with **least privilege** by default
