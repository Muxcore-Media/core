# AGENTS.md — MuxCore Core

The distributed fabric runtime (\`muxcored\`). Sidecar modules register over gRPC; capabilities enforce the security boundary.

Workspace deploy: [`../AGENTS.md`](../AGENTS.md). Module ports: [`../_mvp/PORTS.md`](../_mvp/PORTS.md).

## Key subsystems

| Path | Purpose |
|------|---------|
| `cmd/muxcored/` | Server bootstrap |
| `internal/module/mgr/` | Sidecar process manager |
| `internal/registry/` | Capability index + dependency resolution |
| `internal/grpcmesh/` | gRPC mesh, discovery, auth |
| `internal/events/` | In-memory event bus |
| `pkg/contracts/` | Core lifecycle interfaces (domain events moved to `contracts-media`) |
| `proto/` | Core infra protobuf |
| `sdk/go/client/` | Go mesh client |
| `sdk/go/module/` | Sidecar module SDK |

## Agent rules

- gRPC sidecar model only; in-process `contracts.Register` is deprecated.
- Audit is fire-and-forget (goroutines, never block core).
- TLS required in production.
- Domain `media.*` / `download.*` event constants: canonical source is `github.com/Muxcore-Media/contracts-media/events`; `pkg/contracts/media_events.go` re-exports for compatibility.

## Build & test

```bash
cd core
make test
make lint
make proto   # after proto changes
```
