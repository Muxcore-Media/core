# MuxCore

**Distributed, media-agnostic, module-first. The media orchestration platform the *arr stack couldn't be.**

Core is the highway. Modules are the cars. Zero module code ships in the core binary.

> **AI transparency:** Portions of this codebase may be written with AI
> assistance. Every change — human, AI, or hybrid — goes through the same
> rigorous pipeline: mandatory code review, automated testing, Dependabot
> security scanning, and a capability-based security model that limits blast
> radius regardless of code origin. We treat AI as a productivity tool, not
> a replacement for judgment, and we take the security of every line seriously.

## The Problem

The *arr stack hits a resource ceiling. It's monolithic, almost entirely
single-threaded C# — you can't split the load across nodes, and you can't
saturate the cores you already have. Each media type needs its own program:
Radarr for movies, Sonarr for TV, Lidarr for music, Readarr for books. Your
1080p and 4K libraries? Two separate instances. Content pipelines are rigid:
torrents and Usenet, take it or leave it. As your library grows, the interface
slows to a crawl.

This wasn't bad engineering. It was just designed for a smaller world.

MuxCore is built from the ground up to make **zero assumptions** about your
setup. There is no hardcoded list of media types, no baked-in download
backends, no fixed UI. Everything is a module — you define what media you
have, how it's acquired, and how it's served. 3D movies, comic books,
archival images, VR180 footage, interactive fiction, audiobooks — if someone
can write a module for it, core can orchestrate it.

## What MuxCore Is

MuxCore is the distributed rewrite. Every capability — downloading, indexing, metadata, transcoding, playback, storage — is a **module behind a contract**. Modules communicate over an event bus, not direct calls. Fire up more nodes to split the load. One platform for movies, TV, music, books, podcasts, comics, and any media type you name.

Core itself has **one job**: provide the fabric and get out of the way. Event bus, module registry, lifecycle manager, API server with `/health`. Everything else is a module you install from a marketplace.

## Architecture

```
                ┌────────────────────┐
                │     Web UI         │  ← module (admin-ui)
                └─────────┬──────────┘
                          │
                ┌─────────▼──────────┐
                │    API Server      │  ← /health only in core
                │     MuxCore        │     REST routes in api-rest module
                └───────┬────────────┘
                        │
         ┌──────────────┼──────────────┐
         │              │              │
 ┌───────▼──────┐ ┌─────▼─────┐ ┌──────▼──────┐
 │ Event Bus    │ │ Scheduler │ │ Registry    │
 │ (in-memory)  │ │ (module)  │ │ (ServiceReg)│
 └───────┬──────┘ └─────┬─────┘ └──────┬──────┘
         │              │              │
         └──────┬───────┴───────┬──────┘
                │
        ┌───────▼───────┐
        │   Modules     │
        │               │
        │ Torrent       │
        │ Indexers      │
        │ Metadata      │
        │ Sup.Content   │
        │ Media Server  │
        └───────────────┘
```

## Core vs Modules

**Core** (`github.com/Muxcore-Media/core`) is infrastructure only. One direct dependency (`uuid`). No NATS, no cron, no web framework, no module code. Builds with `-tags default` pull in additional module-level dependencies (e.g., `robfig/cron` from scheduler-cron), but those come from the modules, not core itself.

| Package | Purpose |
|---------|---------|
| `pkg/contracts` | All interfaces: `Module`, `EventBus`, `Downloader`, `Indexer`, `ServiceRegistry`, `RouteRegistrar`, etc. |
| `internal/registry` | Module registration and runtime discovery |
| `internal/module` | Lifecycle manager: init → start → stop with dependency ordering |
| `internal/events` | In-memory event bus for bootstrapping and single-node |
| `internal/api` | Minimal HTTP server with `/health` and `RouteRegistrar` |
| `cmd/muxcored` | Bootstrap entry point |

**Modules** are independent repos under `github.com/Muxcore-Media/`. Each has its own `go.mod`, `muxcore.json` metadata, and depends on core only via `pkg/contracts`. Modules register themselves at init time via `contracts.Register()`.

Core compiles with zero modules. Build with `-tags default` for the essential starter set (admin UI + REST API + cron scheduler).

## For Self-Hosters

- **Pick your modules** — start with the default preset or build from scratch. No unused code.
- **Module marketplace** — add marketplace URLs in config. Browse and install modules from the admin UI. Official modules are verified by the Muxcore-Media org.
- **One platform, all media** — stop running separate instances of Radarr, Sonarr, Lidarr, and Readarr. Name a media type, attach a module, done.
- **Horizontally scalable** — add a second node, load the NATS event bus module, and the scheduler splits the load. No single point of failure.
- **Snappy interface** — admin UI built with HTMX + Go templates + Tailwind CSS. User-facing media modules use SvelteKit + Tailwind CSS.

## For Developers

Every capability in MuxCore is a Go interface in `pkg/contracts`. Implement it, call `contracts.Register()` in your `init()`, and the platform discovers your module.

```go
package mydownloader

import "github.com/Muxcore-Media/core/pkg/contracts"

func init() {
    contracts.Register(func(deps contracts.ModuleDeps) contracts.Module {
        return NewModule(deps.EventBus)
    })
}

type Module struct {
    bus contracts.EventBus
}

func (m *Module) Info() contracts.ModuleInfo {
    return contracts.ModuleInfo{
        ID:   "my-downloader",
        Name: "My Downloader",
        Kinds: []contracts.ModuleKind{contracts.ModuleKindDownloader},
        // ...
    }
}
// ... implement contracts.Module + contracts.Downloader
```

- **Interfaces only** — modules import `pkg/contracts` and nothing else from core. No internal packages.
- **Auto-discovery** — modules find each other via `ServiceRegistry.FindByKind()`. The core registry is the single source of truth.
- **gRPC + protobuf** for the internal mesh (planned). **NATS** available as a module for distributed messaging. **Go SDK** (`pkg/contracts`) for writing modules in Go. Module connectors planned for non-Go languages.
- **Capability negotiation** — modules declare what they support. The platform adapts.
- **Publish to a marketplace** — create a `muxcore.json`, push to GitHub, add your repo to a marketplace catalog.

### Creating a Module

```
modules/my-module/
  go.mod           ← depends on github.com/Muxcore-Media/core
  module.go        ← your code
  muxcore.json     ← marketplace metadata
```

```json
// muxcore.json
{
  "name": "My Module",
  "description": "What it does",
  "version": "1.0.0",
  "kind": "downloader",
  "capabilities": ["downloader.torrent"],
  "dependencies": [],
  "homepage": "https://github.com/you/my-module"
}
```

### Creating a Marketplace

A marketplace is a git repo with a `catalog.json`:

```json
{
  "name": "My Marketplace",
  "description": "Custom modules for my setup",
  "modules": [
    "https://github.com/Muxcore-Media/downloader-qbittorrent",
    "https://github.com/you/my-module"
  ]
}
```

Users add your repo URL. The platform fetches the catalog, reads each module's `muxcore.json`, and displays them. Modules from `github.com/Muxcore-Media` get an Official badge.

See [Module System](https://github.com/Muxcore-Media/core/wiki/Module-System) and [Contracts](https://github.com/Muxcore-Media/core/wiki/Contracts) for the full interface surface.

## Module Types

| Type | Description |
|------|-------------|
| **Authentication** | Plex auth, OAuth/OIDC, LDAP, local accounts — tie into existing user infrastructure |
| **Provider** | Indexers, metadata, supplementary content (subtitles, lyrics, chapters), notifications — any data source |
| **Downloader** | Torrent engines, Usenet bridges, debrid services, direct HTTP |
| **Media Manager** | User-defined media types backed by modules. Create `"movie"`, `"comic-book"`, `"movie-4k"` — any string, any module |
| **Indexer** | Search torrent/usenet indexers via Torznab/Newznab APIs |
| **Processor** | Transcoding, media analysis, thumbnail generation, AI tagging |
| **Playback** | Streaming, DLNA, watch state sync, transcoding proxy |
| **Workflow** | End-to-end pipelines: request → search → download → verify → transcode → import → notify |
| **Storage** | Local FS, S3, SMB, NFS, Ceph, Glacier — abstracted behind object IDs |
| **UI** | Admin dashboards, web interfaces |
| **API** | REST, gRPC, GraphQL endpoints |
| **Event Bus** | Distributed messaging backends (NATS, Kafka, RabbitMQ) |
| **Scheduler** | Task scheduling (cron, distributed queues) |

## Design Principles

- **Event-driven everything** — modules communicate via `media.requested`, `download.completed`, `transcode.failed` events, not direct calls
- **Module contracts over implementations** — every capability is an interface; modules negotiate capabilities at runtime
- **Never touch paths** — storage is abstracted behind object IDs, never filesystem paths
- **Distributed by default** — modules communicate over gRPC/NATS, crash-isolated, independently updatable
- **Capability-based interfaces** — small interfaces (`Streamable`, `Seekable`, `Watchable`) rather than giant monolithic ones
- **Core as fabric** — core does one thing: wire modules together. If you're adding a feature, it probably belongs in a module

## Tech Stack

| Layer | Technology |
|-------|------------|
| Language | Go |
| Core dependency | `github.com/google/uuid` (1 dep) |
| Module contracts | `pkg/contracts` in core |
| Internal Mesh | gRPC + protobuf |
| Event Bus (core) | In-memory pub/sub |
| Event Bus (distributed) | NATS (via `eventbus-nats` module) |
| Database | Module-provided via DatabaseProvider contract (`database-postgres`, `database-sqlite`, etc.) |
| Cache | Module-provided via CacheProvider contract (`cache-redis`, `cache-valkey`, etc.) |
| Storage | Core orchestrator + module providers (S3, local, Ceph via StorageProvider contract) |
| Core Admin UI | HTMX + Go templates + Tailwind CSS (via `admin-ui` module) |
| Module UIs | SvelteKit + Tailwind CSS (user-facing media modules); any framework for third-party |

## Getting Started

```bash
# Bare core (zero modules)
go install github.com/Muxcore-Media/core/cmd/muxcored@latest
muxcored

# Default preset (admin UI + REST API + cron scheduler)
git clone https://github.com/Muxcore-Media/core
cd core
go build -tags default ./cmd/muxcored
./muxcored
```

Or with Docker (builds with default preset):

```bash
docker compose up
```

## Official Modules

| Module | Repo | Kind |
|--------|------|------|
| Admin UI | [admin-ui](https://github.com/Muxcore-Media/admin-ui) | ui |
| REST API | [api-rest](https://github.com/Muxcore-Media/api-rest) | api |
| Local Auth | [auth-local](https://github.com/Muxcore-Media/auth-local) | auth |
| qBittorrent | [downloader-qbittorrent](https://github.com/Muxcore-Media/downloader-qbittorrent) | downloader |
| NATS Event Bus | [eventbus-nats](https://github.com/Muxcore-Media/eventbus-nats) | eventbus |
| Jackett Indexer | [indexer-jackett](https://github.com/Muxcore-Media/indexer-jackett) | provider |
| Jellyfin | [jellyfin](https://github.com/Muxcore-Media/jellyfin) | playback |
| Media Library | [media-library](https://github.com/Muxcore-Media/media-library) | media_manager |
| Movie Manager | [media-manager-movies](https://github.com/Muxcore-Media/media-manager-movies) | media_manager |
| Discord Notifier | [notifier-discord](https://github.com/Muxcore-Media/notifier-discord) | provider |
| Gossip Cluster | [cluster-gossip](https://github.com/Muxcore-Media/cluster-gossip) | provider |
| Cron Scheduler | [scheduler-cron](https://github.com/Muxcore-Media/scheduler-cron) | scheduler |
| Workflow Engine | [workflow-engine](https://github.com/Muxcore-Media/workflow-engine) | workflow |
| PostgreSQL | [database-postgres](https://github.com/Muxcore-Media/database-postgres) | provider |
| Redis Cache | [cache-redis](https://github.com/Muxcore-Media/cache-redis) | provider |
| Audit Logger | [audit-logger](https://github.com/Muxcore-Media/audit-logger) | provider |
| Prometheus | [prometheus-metrics](https://github.com/Muxcore-Media/prometheus-metrics) | provider |
| Storage Tiering | [storage-tiering](https://github.com/Muxcore-Media/storage-tiering) | provider |
| Worker Pool | [worker-pool](https://github.com/Muxcore-Media/worker-pool) | scheduler |

Marketplace catalog: [marketplace-catalog](https://github.com/Muxcore-Media/marketplace-catalog)

## License

GPL-3.0

---

**MuxCore is a distributed event-driven media orchestration platform. Core is the highway. Modules are the cars.**
