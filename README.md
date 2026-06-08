# MuxCore

> **AI transparency:** Portions of this codebase may be written with AI
> assistance. Every change — human, AI, or hybrid — goes through the same
> rigorous pipeline: mandatory code review, automated testing, Dependabot
> security scanning, and a capability-based security model that limits blast
> radius regardless of code origin. We treat AI as a productivity tool, not
> a replacement for judgment, and we take the security of every line seriously.
> 
**A distributed fabric for media orchestration. Core is the loom. Everything else is a thread you weave in.**

MuxCore is built from the ground up to make **zero assumptions**. No hardcoded media types, no baked-in download backends, no fixed UI. If someone can write a module for it, the fabric can orchestrate it.

## The Problem

The *arr stack hits a resource ceiling. It's monolithic, almost entirely single-threaded C# — you can't split the load across nodes, and you can't saturate the cores you already have. Each media type needs its own program: Radarr for movies, Sonarr for TV, Lidarr for music, Readarr for books. Your 1080p and 4K libraries? Two separate instances. Content pipelines are rigid: torrents and Usenet, take it or leave it.

## What MuxCore Is

MuxCore is a **distributed fabric** — an event-driven platform where every capability is a module (a thread) behind a contract (a pattern). Core provides the loom: event bus, registry, lifecycle manager, storage orchestrator, API server, and gRPC mesh. Everything else — downloading, indexing, playback, transcoding, metadata, notifications — is a module.

Core's iron rule: **if a new media paradigm requires a core change, the architecture is wrong.** Domain concepts live in independent versioned contract repos. Modules import what they need. Core imports none of them.

## The Fabric Metaphor

| Concept | What It Is |
|---------|------------|
| **The Loom** | Core — the fixed infrastructure. Event bus, registry, scheduler, storage, API, mesh. Never changes. |
| **Threads** | Modules — all capabilities. Downloaders, indexers, transcoders, media managers. Any number, any role. |
| **Patterns** | Contracts — interfaces threads agree to weave to. Fabric patterns in core, domain patterns in 17 contract repos. |
| **Signals** | Events — how threads communicate. Never directly. Always through the signal layer. |
| **Tapestries** | Workflows — multi-step orchestrations weaving many threads together. |
| **The Registry** | Holds every thread. `FindByRole("downloader")` or `FindByCapability("cache.local")`. |

→ **[Read the full philosophy](https://github.com/Muxcore-Media/core/wiki/Philosophy)** — five patterns with worked code examples showing how the loom never changes.

## Getting Started

```bash
# Bare loom (zero threads)
go install github.com/Muxcore-Media/core/cmd/muxcored@latest
muxcored

# With threads: import the modules you want in your own main.go
```

→ **[Full getting started guide](https://github.com/Muxcore-Media/core/wiki/Getting-Started)** — build presets, Docker setup, configuration.

## Documentation

| Page | Description |
|------|-------------|
| [Philosophy](https://github.com/Muxcore-Media/core/wiki/Philosophy) | Why the loom never changes — five patterns with code examples |
| [Getting Started](https://github.com/Muxcore-Media/core/wiki/Getting-Started) | Installation, build presets, Docker, configuration |
| [Architecture](https://github.com/Muxcore-Media/core/wiki/Architecture) | Fabric layers: loom, threads, patterns, signals, tapestries |
| [Writing Modules](https://github.com/Muxcore-Media/core/wiki/Writing-Modules) | Weave a new thread — complete guide |
| [Module System](https://github.com/Muxcore-Media/core/wiki/Module-System) | Lifecycle, discovery, contract declarations |
| [Contracts](https://github.com/Muxcore-Media/core/wiki/Contracts) | Fabric patterns (core) vs domain patterns (17 repos) |
| [Module Types](https://github.com/Muxcore-Media/core/wiki/Module-Types) | All 28 module roles, contracts, infrastructure services |
| [Event System](https://github.com/Muxcore-Media/core/wiki/Event-System) | Signals: fabric events vs domain events, pub/sub patterns |
| [Marketplace](https://github.com/Muxcore-Media/core/wiki/Marketplace) | Thread directory, catalog format, compatibility |
| [Storage](https://github.com/Muxcore-Media/core/wiki/Storage-Abstraction) | Virtual filesystem, capability negotiation |
| [Workflow Engine](https://github.com/Muxcore-Media/core/wiki/Workflow-Engine) | Tapestries: multi-step pipelines, retries, idempotency |
| [Security Model](https://github.com/Muxcore-Media/core/wiki/Security-Model) | RBAC, API tokens, SSO/OIDC, sandboxing |
| [Deployment](https://github.com/Muxcore-Media/core/wiki/Deployment) | Phase 1-3 deployment strategy |
| [Roadmap](https://github.com/Muxcore-Media/core/wiki/Roadmap) | MVP scope and long-term vision |

## License

GPL-3.0

---

**MuxCore is a distributed fabric for media orchestration. Core is the loom. Everything else is a thread you weave in.**
