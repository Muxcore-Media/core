# MuxCore

**Distributed, media-agnostic, module-first. The media orchestration platform the *arr stack couldn't be.**

## The Problem

The *arr stack hits a resource ceiling. It's monolithic C# — you can't split the load across nodes. Each media type needs its own program: Radarr for movies, Sonarr for TV, Lidarr for music, Readarr for books. Your 1080p and 4K libraries? Two separate instances. Content pipelines are rigid: torrents and Usenet, take it or leave it. As your library grows, the interface slows to a crawl.

This wasn't bad engineering. It was just designed for a smaller world.

## What MuxCore Is

MuxCore is the distributed rewrite. Every capability — downloading, indexing, metadata, transcoding, playback, storage — is a **module behind a contract**. Modules communicate over an event bus, not direct calls. Fire up more nodes to split the load. One platform for movies, TV, music, books, podcasts, comics, and any media type you name.

The core itself does as little as possible. It provides the fabric — event bus, API gateway, scheduler, service registry, module lifecycle — and gets out of the way. Everything else is a module you install from the marketplace.

## Architecture

```
                ┌────────────────────┐
                │     Web UI         │
                └─────────┬──────────┘
                          │
                ┌─────────▼──────────┐
                │    API Gateway     │
                │     MuxCore        │
                └───────┬────────────┘
                        │
         ┌──────────────┼──────────────┐
         │              │              │
 ┌───────▼──────┐ ┌─────▼─────┐ ┌──────▼──────┐
 │ Event Bus    │ │ Scheduler │ │ Service Reg │
 └───────┬──────┘ └─────┬─────┘ └──────┬──────┘
         │              │              │
         └──────┬───────┴───────┬──────┘
                │               │
     ┌──────────▼───┐   ┌──────▼────────┐
     │ Modules       │   │ Worker Agents │
     │               │   │               │
     │ Torrent       │   │ Transcoding   │
     │ Indexers      │   │ Analysis      │
     │ Metadata      │   │ ML Tasks      │
     │ Subtitle      │   │ File Ops      │
     │ Media Server  │   │ Etc           │
     └───────────────┘   └───────────────┘
```

## For Self-Hosters

- **Setup wizard** — pick your starter modules and you're off. No config files to hand-edit.
- **Module marketplace** — browse, install, and configure modules from the web UI. Don't hunt GitHub for connectors.
- **One platform, all media** — stop running separate instances of Radarr, Sonarr, Lidarr, and Readarr. Name a media type, attach a module, done.
- **Distributed by default** — add a second node and the scheduler splits the load. No single point of failure.
- **Snappy interface** — admin UI built with HTMX + Go templates + Tailwind CSS. User-facing media modules use SvelteKit + Tailwind CSS. No sluggish web portals.

## For Developers

Every capability in MuxCore is a Go interface. Implement it, register it, and the platform discovers it.

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

- **Write once, run anywhere** — modules can be embedded in the core process, external binaries, remote services, or distributed agents.
- **gRPC + protobuf** for the internal mesh. **NATS** for the event bus. **Go SDK** to start, multi-language later.
- **Capability negotiation** — modules declare what they support. The platform adapts.
- **Publish to the marketplace** — one registry, discoverable by every MuxCore instance.

See [Module System](https://github.com/Muxcore-Media/core/wiki/Module-System) and [Contracts](https://github.com/Muxcore-Media/core/wiki/Contracts) for the full interface surface.

## Module Types

| Type | Description |
|------|-------------|
| **Authentication** | Plex auth, OAuth/OIDC, LDAP, local accounts — tie into existing user infrastructure instead of rebuilding it. The core provides the contract; modules handle the actual authentication. |
| **Provider** | Indexers, metadata, subtitles, notifications — any data source |
| **Downloader** | Torrent engines, Usenet bridges, debrid services, direct HTTP |
| **Media Manager** | User-defined media types backed by modules. Create `"movie"`, `"comic-book"`, `"movie-4k"` — any string, any module. Multiple instances of the same type coexist with different configurations. |
| **Processor** | Transcoding, media analysis, thumbnail generation, AI tagging |
| **Playback** | Streaming, DLNA, watch state sync, transcoding proxy |
| **Workflow** | End-to-end pipelines: request → search → download → verify → transcode → import → notify |
| **Storage** | Local FS, S3, SMB, NFS, Ceph, Glacier — abstracted behind object IDs |

## Design Principles

- **Event-driven everything** — modules communicate via `media.requested`, `download.completed`, `transcode.failed` events, not direct calls
- **Module contracts over implementations** — every capability is an interface; modules negotiate capabilities at runtime
- **Never touch paths** — storage is abstracted behind object IDs, never filesystem paths
- **Distributed by default** — modules communicate over gRPC/NATS, crash-isolated, independently updatable
- **Capability-based interfaces** — small interfaces (`Streamable`, `Seekable`, `Watchable`) rather than giant monolithic ones

## Tech Stack

| Layer | Technology |
|-------|------------|
| Language | Go |
| External API | REST + OpenAPI |
| Internal Mesh | gRPC + protobuf |
| Event Bus | NATS (pub/sub, request/reply, streaming) |
| Database | PostgreSQL (persistent) + Redis (ephemeral/caching) |
| Storage | Abstracted blob layer (S3-compatible) |
| Core Admin UI | HTMX + Go templates + Tailwind CSS |
| Module UIs | SvelteKit + Tailwind CSS (user-facing media modules); any framework for third-party modules |
| Unified CSS | Core publishes a Tailwind config — modules extend it for consistent design |

## Repository Structure

```
muxcore/
├── core/          ← this repo (orchestration platform)
├── sdk/           ← Go and multi-language SDKs
├── proto/         ← protobuf contract definitions
├── modules/       ← module implementations
│   ├── downloader-qbittorrent/
│   ├── downloader-native/
│   ├── indexer-jackett/
│   ├── media-movies/
│   ├── transcoder-ffmpeg/
│   └── notifier-discord/
├── agents/        ← distributed worker agents
├── ui/            ← Module UIs (SvelteKit for user-facing media modules)
├── deploy/        ← deployment configs
└── docs/          ← documentation
```

## Getting Started

```bash
go install github.com/Muxcore-Media/core/cmd/muxcored@latest
muxcored
```

## License

TBD

---

**MuxCore is a distributed event-driven media orchestration platform. Not a replacement for Sonarr.**
