# core/pkg/contracts

Shared domain types and event re-exports for MuxCore modules.

## Contract dependencies

This nested module re-exports or aliases canonical contract repos:

| Contract repo | Purpose |
|---------------|---------|
| `contracts-media` | Domain events (`events` package) |
| `contracts-scanner` | Scanner gRPC (`ScannerService`) |
| `contracts-automation` | Automation gRPC (`AutomationService`) |
| `contracts-metadata` | TMDB/metadata gRPC (`MetadataService`) |

**Consumers:** import contract stubs directly when possible:

```go
scannerv1 "github.com/Muxcore-Media/contracts-scanner/muxcore/scanner/v1"
mediacontracts "github.com/Muxcore-Media/contracts-media/events"
```

Use `core/pkg/contracts` only for transitional event aliases (`media_events.go`).

## Publishing

Forgejo/spool consumers resolve tagged modules, not monorepo replaces.

1. Tag each `contracts-*` repo (`v0.1.x`).
2. Cut a `core` release with `go.mod` **requires only** (no `replace`).
3. Modules pin `github.com/Muxcore-Media/core/pkg/contracts v0.5.x` and contract repos at matching tags.

See `_mvp/docs/RELEASE-BUILD.md` for the release-branch workflow.
