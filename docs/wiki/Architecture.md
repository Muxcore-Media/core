# Architecture

## High-Level Architecture

```
                ┌────────────────────┐
                │  Core Admin UI    │
                │ (HTMX + Go tmpl)  │
                └─────────┬──────────┘
                          │
                ┌─────────▼──────────┐
                │  Module UIs        │
                │ (SvelteKit / any)  │
                └─────────┬──────────┘
                          │
                ┌─────────▼──────────┐
                │    API Gateway     │
                │  (REST + OpenAPI)  │
                └───────┬────────────┘
                        │
         ┌──────────────┼──────────────┐
         │              │              │
 ┌───────▼──────┐ ┌─────▼─────┐ ┌──────▼──────┐
 │ Event Bus    │ │ Scheduler │ │ Service Reg │
 │ (in-memory)  │ │ (module)  │ │             │
 └───────┬──────┘ └─────┬─────┘ └──────┬──────┘
         │              │              │
         └──────┬───────┴───────┬──────┘
                │               │
     ┌──────────▼─────────────────────────┐
     │ Modules (all capabilities)        │
     │                                   │
     │ Downloaders    Transcoding        │
     │ Indexers       AI Analysis        │
     │ Metadata       File Ops           │
     │ Sup.Content    ML Tasks           │
     │ Notifications  Replication        │
     │ Media Server   Worker Pool        │
     │ Storage                           │
     └───────────────────────────────────┘
```

## Core Services

### API Gateway
- External: REST + OpenAPI 3.1
- Internal mesh: gRPC + protobuf
- Handles auth, rate limiting, routing
- Composes UI panels from registered modules

### Cluster Membership
- Not built into core — provided by a cluster module implementing `contracts.Cluster`
- Gossip-based membership available as a module (`cluster-gossip`) with leader election and failure detection
- Core discovers the cluster module from the registry at bootstrap; single-node deployments run without one
- Modules receive the cluster via `ModuleDeps.Cluster` — nil when running standalone

### Event Bus
- Core provides an in-memory pub/sub bus for bootstrapping and single-node
- NATS available as a module (`eventbus-nats`) for distributed messaging
- Pub/sub for loose coupling, request/reply for synchronous queries

### Scheduler
- Cron-based scheduling provided by the `scheduler-cron` module
- Publishes `scheduler.task.execute` events on the bus
- Replaceable — swap in a distributed scheduler module without touching core

### Service Registry
- Module registration and discovery
- Health checking
- Capability advertisement
- Version tracking

### Auth Gateway
- Validates sessions and enforces RBAC based on session roles/permissions
- Actual authentication is delegated to **auth modules** (Plex, OAuth/OIDC, LDAP, local accounts)
- Multiple auth modules can be active simultaneously — users sign in via any configured provider
- Per-module permissions, API token management, audit logging

### Storage Orchestrator
- Abstracts all storage behind object IDs
- Capability-based provider negotiation
- Multi-provider routing via policies
- Cache layer management

### Workflow Engine
- Defines and executes multi-step pipelines
- Retry with backoff
- Idempotency guarantees
- Compensation on failure

### Module Registry
- Module lifecycle management (load, init, start, stop)
- Dependency resolution
- Version compatibility checking
- Process isolation (external modules)
- **Multi-kind registration** — a single module can register under multiple `ModuleKind` values (e.g., Jellyfin as both `playback` AND `provider` AND `auth`). The registry indexes by all declared kinds.
- **Capability-based discovery** — `FindByCapability(cap string)` complements `FindByKind(kind)`, enabling fine-grained service discovery (e.g., "find all modules that provide supplementary content")

## Internal Communication

All internal communication goes through the event bus or gRPC:

```
Module A  ──(NATS pub)──>  Event Bus  ──(NATS sub)──>  Module B
Module A  ──(gRPC)─────>  Module B (request/reply)
Module A  ──(NATS req)──>  Event Bus  ──(NATS rep)──>  Module A
```

**Modules never call each other directly.** This ensures:
- Crash isolation — one module failure doesn't cascade
- Independent updates — modules can be upgraded separately
- Language agnosticism — future modules can be in any language
- Testability — modules can be mocked via event replay

## Design Decisions

### Compile-Time Modules vs External Services

**Decision: Compile-time modules for MVP, external services planned for later phases**

| Approach | Pros | Cons |
|----------|------|------|
| **Compile-time (chosen for MVP)** | No network overhead, simple deployment, single binary option | Must recompile to add modules, all Go |
| External services (planned) | Language agnostic, crash isolation, independent updates, HA-friendly | More complexity, network overhead |

Modules are compiled into the core binary via blank imports + build tags. The `-tags default` preset bundles essential modules. Future phases will support external modules over gRPC/NATS.

### Event-Driven vs Direct RPC

**Decision: Event-driven with request/reply for queries**

- Events for state changes: `download.completed`, `media.requested`
- Request/reply for queries: "what's the status of download X?"
- Events are the primary integration pattern

### Capability-Based Interfaces

**Decision: Small interfaces, not giant monolithic ones**

Instead of one giant `StorageProvider` interface, use capability interfaces:
- `Streamable`, `Seekable`, `Watchable`, `AtomicMovable`, `Hardlinkable`

Modules detect capabilities at runtime and adapt.
