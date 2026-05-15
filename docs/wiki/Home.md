# Welcome to the MuxCore Wiki

**MuxCore is a distributed media orchestration platform with a plugin-first architecture.**

It is not "another \*arr stack." Every capability is abstracted behind interfaces and contracts. Modules can be embedded, external processes, remote network services, or distributed agents.

## The Core Differentiator

Where existing media stacks are monoliths with hardcoded integrations, MuxCore is an **orchestration platform** where:

- Every capability is a **module behind a contract**
- Modules communicate via an **event bus**, not direct calls
- Storage is **fully abstracted** — modules never touch filesystem paths
- The system is **HA-aware and horizontally scalable** from day one
- You can have **multiple modules per capability** (e.g., two downloader modules, three transcoding agents)

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
