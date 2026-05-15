# Welcome to the MuxCore Wiki

**MuxCore is a distributed, media-agnostic, module-first media orchestration platform.**

## Why MuxCore

The *arr stack was built for a smaller world. It's monolithic — one process, one node, one ceiling. Each media type needs its own program. Your 1080p and 4K libraries? Two separate Sonarr instances. Content pipelines are rigid, the interface bogs down under a large library, and when a node goes down nothing takes over.

MuxCore is the distributed rewrite. Every capability is a module behind a contract. Modules communicate over an event bus, not direct calls. Fire up more nodes. Name any media type. The platform adapts.

## How It Works

- **Every capability is a module behind a contract** — downloading, indexing, transcoding, playback, storage are all interfaces
- **Modules communicate via an event bus** — `media.requested`, `download.completed`, `transcode.failed`, not direct calls
- **Storage is fully abstracted** — modules never touch filesystem paths, everything goes through object IDs
- **HA-aware and horizontally scalable** — add nodes, split the load, survive failures
- **Multiple modules per capability** — two downloaders, three transcoding agents, five storage backends, all active simultaneously

## Quick Navigation

| Page | Description |
|------|-------------|
| [Architecture](Architecture) | High-level architecture and system design |
| [Module System](Module-System) | How modules work, lifecycle, contracts |
| [Module Types](Module-Types) | Provider, Downloader, Media Manager, Processor, Playback, Workflow, Storage |
| [Storage Abstraction](Storage-Abstraction) | Virtual filesystem, capability negotiation, storage layers |
| [Event System](Event-System) | Pub/sub, event types, event-driven workflows |
| [Workflow Engine](Workflow-Engine) | Orchestration, retries, idempotency, pipelines |
| [Security Model](Security-Model) | RBAC, API tokens, SSO/OIDC, sandboxing |
| [Contracts](Contracts) | Interface definitions and API contracts |
| [Deployment](Deployment) | Phase 1-3 deployment strategy |
| [Roadmap](Roadmap) | MVP scope and long-term vision |

## Conceptual Precedent

MuxCore is conceptually closer to:

- **Kubernetes** — orchestration, scheduling, service discovery
- **HashiCorp Nomad** — workload placement, distributed workers
- **Home Assistant** — modular integrations, UI composition
- **Jellyfin/Plex** — playback, transcoding, media serving

than to a monolithic media manager.

## Quick Start

```bash
go install github.com/Muxcore-Media/core/cmd/muxcored@latest
muxcored
```
