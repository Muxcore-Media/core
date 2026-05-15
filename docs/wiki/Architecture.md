# Architecture

## High-Level Architecture

```
                ┌────────────────────┐
                │     Web UI         │
                │   (Vue 3 + TS)     │
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
 │   (NATS)     │ │           │ │             │
 └───────┬──────┘ └─────┬─────┘ └──────┬──────┘
         │              │              │
         └──────┬───────┴───────┬──────┘
                │               │
     ┌──────────▼───┐   ┌──────▼────────┐
     │ Modules       │   │ Worker Agents │
     │               │   │               │
     │ Downloaders   │   │ Transcoding   │
     │ Indexers      │   │ AI Analysis   │
     │ Metadata      │   │ File Ops      │
     │ Subtitles     │   │ ML Tasks      │
     │ Notifications │   │ Replication   │
     │ Media Server  │   │               │
     │ Storage       │   │               │
     └───────────────┘   └───────────────┘
```

## Core Services

### API Gateway
- External: REST + OpenAPI 3.1
- Internal mesh: gRPC + protobuf
- Handles auth, rate limiting, routing
- Composes UI panels from registered modules

### Event Bus (NATS)
- Pub/sub for loose coupling
- Request/reply for synchronous queries
- Streaming for ordered event processing
- Clustered for HA

### Scheduler
- Cron-based scheduled tasks
- One-shot delayed tasks
- Distributed task ownership via Redis locks

### Service Registry
- Module registration and discovery
- Health checking
- Capability advertisement
- Version tracking

### Auth/RBAC
- OIDC / SSO support
- API token management
- Per-module permissions
- Audit logging

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

### External Services vs Embedded Plugins

**Decision: External services over gRPC/NATS (not Go `.so` plugins)**

| Approach | Pros | Cons |
|----------|------|------|
| Go plugins (.so) | Fast, shared memory | Fragile, platform issues, version coupling, poor sandboxing |
| **External services (chosen)** | Distributed by default, language agnostic, crash isolation, independent updates, HA-friendly | More complexity, network overhead |

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
