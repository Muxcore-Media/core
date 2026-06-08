# MuxCore

> **AI transparency:** Portions of this codebase may be written with AI
> assistance. Every change — human, AI, or hybrid — goes through the same
> rigorous pipeline: mandatory code review, automated testing, Dependabot
> security scanning, and a capability-based security model that limits blast
> radius regardless of code origin.

**A distributed fabric for media orchestration. Core is the loom. Everything else is a thread you weave in.**

---

## The Problem

The *arr stack hits a ceiling. It's monolithic — you can't split the load across machines. Each media type needs its own program: Radarr for movies, Sonarr for TV, Lidarr for music. Your 1080p and 4K libraries? Two separate instances. Content pipelines are rigid. When a machine goes down, nothing takes over.

---

## What MuxCore Is

MuxCore is a single platform that replaces the entire *arr stack. Every feature — downloading, indexing, playback, transcoding, notifications — is a pluggable module. Add what you need. Replace what you don't. Scale from a single machine to a cluster without rebuilding.

Core's iron rule: **if a new media type requires changing the platform, the architecture is wrong.** Core knows nothing about movies, TV, music, or books. It provides eight basic services — event routing, storage, scheduling, module discovery, and a few others — and gets out of the way. Everything media-specific lives in modules.

| What you're used to | How MuxCore does it |
|---|---|
| One program per media type | One platform, any media type |
| Hardcoded workflows | Configurable pipelines you define |
| One machine, one ceiling | Add machines, split the load |
| 1080p and 4K need two Sonarr instances | One library, any number of qualities |
| If a node goes down, nothing takes over | Workers redistribute tasks automatically |
| Storage must be local or NFS-mounted | Storage can be local, S3, cloud, or mixed |

---

## Quick Start

```bash
go install github.com/Muxcore-Media/core/cmd/muxcored@latest
muxcored
```

That starts the loom with no modules — just the platform. Add modules to do actual work.  
→ **[Full getting started guide](https://github.com/Muxcore-Media/core/wiki/Getting-Started)**

---

## Documentation

| New to MuxCore? | Building modules? | Running it? |
|---|---|---|
| [Core Concepts](https://github.com/Muxcore-Media/core/wiki/Core-Concepts) | [Module System](https://github.com/Muxcore-Media/core/wiki/Module-System) | [Getting Started](https://github.com/Muxcore-Media/core/wiki/Getting-Started) |
| [Event System](https://github.com/Muxcore-Media/core/wiki/Event-System) | [Writing Modules](https://github.com/Muxcore-Media/core/wiki/Writing-Modules) | [Deployment](https://github.com/Muxcore-Media/core/wiki/Deployment) |
| [Storage](https://github.com/Muxcore-Media/core/wiki/Storage) | [Contracts Reference](https://github.com/Muxcore-Media/core/wiki/Contracts) | [Security](https://github.com/Muxcore-Media/core/wiki/Security) |
| | [Workflow Engine](https://github.com/Muxcore-Media/core/wiki/Workflow-Engine) | [Roadmap](https://github.com/Muxcore-Media/core/wiki/Roadmap) |

---

## License

GPL-3.0

---

**MuxCore is a distributed fabric for media orchestration. The loom never changes. The threads are yours to define.**
