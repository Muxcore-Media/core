# Module System

## Overview

Every capability in MuxCore is a **module** behind a **contract**. The core provides only orchestration — modules provide all functionality. Modules are independent Go modules in separate repos, not embedded in the core codebase.

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

## Auto-Registration

Modules register themselves at init time via `contracts.Register()`. Each module calls this in its `init()` function, providing a factory that receives core dependencies:

```go
package mymodule

import "github.com/Muxcore-Media/core/pkg/contracts"

func init() {
    contracts.Register(func(deps contracts.ModuleDeps) contracts.Module {
        return NewModule(deps.EventBus, deps.Registry, deps.Routes)
    })
}
```

### ModuleDeps

Core provides three services to every module during construction:

| Service | Interface | Purpose |
|---------|-----------|---------|
| `EventBus` | `contracts.EventBus` | Publish and subscribe to events |
| `Registry` | `contracts.ServiceRegistry` | Discover other modules at runtime |
| `Routes` | `contracts.RouteRegistrar` | Register HTTP handlers with the core API server |

Modules receive these via the factory and store what they need. A simple downloader module only needs the event bus. A web UI module needs the registry and routes.

## Service Registry

Modules discover each other at runtime through the `ServiceRegistry`:

```go
// Find all downloader modules
entries := deps.Registry.FindByKind(contracts.ModuleKindDownloader)
for _, entry := range entries {
    dl, ok := entry.Module.(contracts.Downloader)
    if ok {
        // Use the downloader
    }
}

// Get a specific module by ID
entry, err := deps.Registry.Resolve("downloader-qbittorrent")

// List every registered module
all := deps.Registry.ListAll()
```

### Route Registration

Modules that serve HTTP endpoints register them during `Start()`:

```go
func (m *Module) Start(ctx context.Context) error {
    m.routes.HandleFunc("/api/v1/modules", m.handleModules)
    m.routes.Handle("/", http.HandlerFunc(m.serveHTTP))
    return nil
}
```

## Communication Methods

### Events (pub/sub — core in-memory bus, or NATS module)
```
Module publishes "download.completed" → any subscriber can react
```

### Request/Reply
```
Module requests status → target module responds directly
```

### gRPC (direct service-to-service)
```
For high-throughput or streaming scenarios
```

## Module Discovery

Modules are discovered through the **Service Registry** at runtime. At compile time, modules register via blank imports in a build-tag-gated preset file:

```go
//go:build default

package presets

import (
    _ "github.com/Muxcore-Media/admin-ui"
    _ "github.com/Muxcore-Media/api-rest"
    _ "github.com/Muxcore-Media/scheduler-cron"
)
```

Build without tags for bare core. Build with `-tags default` for the starter set.

## Marketplace

Modules are published to marketplaces. A marketplace is a git repo with a `catalog.json` listing module repo URLs. The official marketplace is at `github.com/Muxcore-Media/marketplace-catalog`.

Each module repo has a `muxcore.json` with metadata (name, description, version, kind, capabilities, icon, dependencies).

**Official modules** are repos owned by the `Muxcore-Media` GitHub organization. Third-party modules are repos from any other org or user.

## Versioning & Compatibility

- All modules follow **semantic versioning** (MAJOR.MINOR.PATCH)
- Contracts are **versioned** (e.g., `Downloader/v1`)
- The registry maintains a **compatibility matrix**
- Modules can **negotiate capabilities** at connection time

### Capability Negotiation

```
Module A: "I support Downloader/v1, StreamingDownloader/v2"
Module B: "I support Downloader/v1"
→ They agree on Downloader/v1
```

### Multi-Kind Modules *(planned — #63)*

A single module can register under **multiple `ModuleKind` values** simultaneously. For example, the Jellyfin module registers as:

```go
func (m *Module) Info() contracts.ModuleInfo {
    return contracts.ModuleInfo{
        ID:    "jellyfin",
        Kinds: []ModuleKind{ModuleKindPlayback, ModuleKindProvider, ModuleKindAuth},
        Capabilities: []string{
            "playback.jellyfin", "playback.session",
            "provider.metadata", "provider.movie-images",
            "auth.jellyfin-credentials",
        },
    }
}
```

The registry indexes the module under ALL declared kinds. `FindByKind(ModuleKindProvider)` returns Jellyfin. The registry validates at registration that the module implements the required interfaces for each declared kind.

**Discovery paths:**
- **By kind:** `FindByKind(ModuleKindPlayback)` — broad category
- **By capability:** `FindByCapability("playback.jellyfin")` — fine-grained (#64)
- **By ID:** `Resolve("jellyfin")` — direct lookup

This follows Home Assistant's pattern where a single integration declares `PLATFORMS = ["sensor", "binary_sensor", "switch", "light"]` and is discoverable under all of them.

## Module Isolation

### Embedded Modules (Compile-time)
- Compiled into the core binary via blank imports
- Share the core process, in-memory event bus
- Fastest path
- Used for admin UI, REST API, scheduler

### External Modules (Future)
- Run as separate processes or containers
- Communicate over gRPC/NATS
- Crash-isolated, independently updatable

### Remote Modules (Future)
- Run on different machines
- Same communication protocols
- Discovery via the service mesh

## Module Security

- Each module gets **scoped API tokens**
- Modules declare required permissions in their manifest
- RBAC policies govern what modules can access
- External modules run with **least privilege** by default
