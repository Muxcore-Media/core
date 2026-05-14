# MuxCore

**Distributed media orchestration platform with a plugin-first architecture.**

MuxCore is not "another \*arr stack." It is a distributed media orchestration platform where every capability is abstracted behind interfaces and contracts. Modules can be embedded, external processes, remote network services, or distributed agents — the system is HA-aware and horizontally scalable from day one.

Conceptually, MuxCore is closer to Kubernetes, Nomad, Home Assistant, and Jellyfin than to a monolithic media manager.

## Core Philosophy

**Core = orchestration only.** MuxCore should do as little as possible — it provides the fabric. Everything else is a module.

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

## Module Types

| Type | Description | Replaces |
|------|-------------|----------|
| **Provider** | Indexers, metadata, subtitles, notifications | Prowlarr, Bazarr, Notifiarr |
| **Downloader** | Torrent engines, Usenet bridges, debrid | qBittorrent, SABnzbd |
| **Media Manager** | Movie, TV, music, book management | Radarr, Sonarr, Lidarr, Readarr |
| **Processor** | Transcoding, analysis, AI tagging | Tdarr |
| **Playback** | Streaming, DLNA, watch state sync | Jellyfin |
| **Workflow** | Request → Download → Process → Import pipelines | _New capability_ |
| **Storage** | Local FS, S3, SMB, NFS, Ceph, Glacier | _New capability_ |

## Design Principles

- **Event-driven everything** — modules communicate via `media.requested`, `download.completed`, `transcode.failed` events, not direct calls
- **Module contracts over implementations** — every capability is an interface; modules negotiate capabilities at runtime
- **Never touch paths** — storage is abstracted behind object IDs, never filesystem paths
- **Distributed by default** — modules communicate over gRPC/NATS, crash-isolated, independently updatable
- **Capability-based interfaces** — small interfaces (`Streamable`, `Seekable`, `Watchable`) rather than giant monolithic ones

## Tech Stack

- **Language:** Go
- **External API:** REST + OpenAPI
- **Internal Mesh:** gRPC + protobuf
- **Event Bus:** NATS (pub/sub, request/reply, streaming)
- **Database:** PostgreSQL (persistent state) + Redis (ephemeral/caching)
- **Storage:** Abstracted blob layer (S3-compatible)
- **Frontend:** Vue 3 + TypeScript + Tailwind + Pinia

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
├── ui/            ← Vue 3 web UI
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
