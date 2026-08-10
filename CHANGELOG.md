# Changelog

All notable changes to the MuxCore project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Python module SDK scaffold (`sdk/python` **v0.1.0**): `muxcore.sdk.run` registers via `ModuleRegistration`, mirrors Go lifecycle (TLS/insecure + signal shutdown). Nested tag: `sdk/python/v0.1.0`.
- TypeScript module SDK scaffold (`sdk/typescript` **v0.1.0**): `@muxcore-media/sdk` `run()` with `@grpc/grpc-js` + proto-loader. Nested tag: `sdk/typescript/v0.1.0`.

## [v0.5.4] — 2026-08-10 — remote storage sidecar wiring

### Added
- `contracts.CapabilityStorage` (`storage`) for registry discovery of storage sidecars.
- `internal/storage/remote` gRPC `StorageProvider` (+ `Streamable`) client for `muxcore.storage.v1.StorageService`.
- Storage orchestrator `SidecarDialer` / `SetSidecarDialer`: discover and dial modules with role/capability `storage` via announce addr (e.g. `storage-s3`).
- muxcored wires the dialer through `bootstrap.DialSidecar` (same pattern as auth/policy sidecars).

Nested tag: `pkg/contracts/v0.5.4`.

## [v0.5.3] — 2026-08-10 — media.import.failed event

### Added
- `contracts.EventImportFailed` (`media.import.failed`) + `ImportFailedPayload` for automation → notification wiring. Nested tag: `pkg/contracts/v0.5.3`.

## [v0.5.2] — 2026-08-10 — Settings mesh + chunked Storage.Put

### Fixed
- SDK `Storage.Put` streams 1 MiB chunks instead of one giant message (avoids gRPC ResourceExhausted on large media imports). Nested tag: `sdk/go/client/v0.5.2`.

### Added
- `contracts.SettingsUpdater` + `sdk/go/module.RegisterSettings` / `SettingsHandlerFromProvider` for first-class admin settings mesh wiring. Nested tags: `pkg/contracts/v0.5.2`, `sdk/go/module/v0.5.2`.

### Changed
- Make sidecar policy wait tunable; cover empty-registry fast path.
- Cover muxcored management gRPC registration and HTTP/gRPC start/shutdown.
- Cover muxcored `initGRPCMesh` auto-mTLS and insecure boot paths.
- Cover muxcored storage/HTTP/audit/module-manager init helpers.
- Cover muxcored init/lifecycle helpers (flags, stores, policy wiring).
- Expand `module/mgr` resurrect/resolve tests (coverage ~76%).
- Expand `internal/module` tests for health-check loop remediation (coverage).

## [v0.5.1] — 2026-08-10 — Staging mTLS bootstrap + SDK client dial

### Fixed

- Module SDK mTLS dial: register `--muxcore-tls-{cert,key,ca}` flags; load client certs when connecting (spawn already passed cert paths but dial ignored them)
- Spawn writes `ca.crt` beside module certs and passes `--muxcore-tls-ca` so sidecars can verify the auto-CA server cert
- Auto-mTLS bootstrap: when `grpc.mtls_enabled` is set without `cert_file`/`key_file`, create the internal CA first, issue a `muxcored` server cert (also used for HTTP when unset), and wire mTLS via `ca.crt`

### Changed

- Further `cmd/muxcored` decomposition: `init.go` + `lifecycle.go`; `module/mgr` coverage raised (~44% → ~73%)

## [v0.5.0] — 2026-08-08 — Discovery allowlist + Members module list

### Added

- Public discovery allowlist entries for `ListAll`, `Watch`, and `FindByRole` (alongside existing `Members` / `Resolve` / `FindByCapability`) so operator UIs and peer fan-out work without a bearer mesh identity
- `Members` refreshes the local node's `Modules` / `ModuleHealth` from `moduleIDs()` so single-node deployments no longer report an empty module list

### Fixed

- Admin Cluster SSE / dashboard health empty-module regressions on host MVP (single-node membership never received heartbeat module updates)

## [v0.4.0] — 2026-06-08 — 48-gap remediation complete

### Added

- SLSA build provenance generation
- Comprehensive audit logging across core infrastructure (P0-P3)
- Sidecar module architecture with runtime module loading via gRPC
- Service bridge for storage, discovery, and module registration
- Cross-node gRPC mesh routing
- Leader election, backpressure, and write-ahead log (WAL)
- Module capability enforcement — call policy, publish policy, storage access
- Production-readiness audit phases 1-3 (19+23 items)
- New provider contracts: `IdentityProvider`, `StructuredLogger`, `RetryProvider`, `IdempotencyProvider`, `FeatureFlagProvider`, `SerializationProvider`, `EncryptionProvider`, `DistributedLockProvider`, `DataRedactionProvider`, `EventStore`, `CallPolicyProvider`, `TracingProvider`, `ConfigWatcher`, `CircuitBreaker`, `DeadLetterProvider`
- Configurable WAL replay timeout
- Dead node eviction in cluster discovery
- Module list in heartbeats for cross-node routing

### Changed

- CI pipeline overhauled for `golangci-lint` v2
- SDK fully aligned and dependencies bumped
- Audit logger: sync flush interval, configurable scan policy, rollback contract
- `Subscribe` stream.Send serialized through single goroutine to prevent race
- `MemoryBus.Close()` flushes WAL and drains in-flight events
- Discovery server uses `sync.Once` for double-close protection

### Fixed

- 17 security audit findings (SEC-001 through SEC-016)
- Anti-vibe audit findings (AV-002 through AV-014)
- AI code slop findings across 8 files
- Critical call policy bypass and storage data race
- Contract interfaces hardened — credentials exposure, unsafe defaults, DB exposure
- Docker HEALTHCHECK `curl -k` flag removed
- Remaining medium/low audit findings (5 items from Wave 5)
- Thread-unsafe `trustedOrigins` global fixed with mutex
- `os.Exit(1)` in post-startup code paths replaced with `context.CancelFunc`
- gRPC TLS hardening, insecure fallback removed
- HTTP API server TLS support added
- Cluster join token authentication
- Config validation: `[]string`/`strings.Join` replaced with `errors.Join`
- Spool host allow-list for SSRF protection (H-1)
- Storage `Delete` error handling, WAL permissions, conn pool stale detection

### Security

- TLS required for seed node connections (no insecure fallback)
- gRPC event and discovery services default to auth required
- Call policy bypass fixed (critical)
- Capability enforcement for module call, publish, and storage access
- Module source scan expanded for dangerous patterns (go:generate, etc.)
- Security capabilities promoted from warnings to errors
- HMAC key copied internally, `ClearSigningKey` added
- Encryption provider enforcement at startup
- Panic recovery output sanitized
- Internal event publishers tagged with system identity
- TOCTOU race in config reload event dispatch fixed
- gRPC logging interceptor error output sanitized
- `MUXCORE_SPOOL_ALLOWED_HOSTS` env var for SSRF protection

## [v0.3.0] — 2026-05-25 — Security audit, contract gap resolution & fabric rename

### Added

- `MetadataProvider`, `ArtworkProvider`, `MediaResolver`, `MediaDiscovery`, `Transcoder` contracts (Phase 1)
- `IndexerAggregator`, `DownloadRouter`, `SecretsProvider`, `MetricsProvider` contracts (Phase 2)
- Streaming, filtering, and routing support in existing contracts (Phase 3)
- Fabric metaphor branding across codebase

### Changed

- Full rebrand from previous naming to fabric metaphor
- README rewritten for fabric-only core v1
- Technical content moved to GitHub wiki
- Domain contracts stripped from core — fabric-only v1

### Fixed

- 36 security and quality findings from deep audit
- 4 CI failures from security audit PR
- Wave 1-5 security fixes: config, CI, Docker hardening, Go security, TLS, auth, etc.
- Unbalanced paren in `Permissions-Policy` header
- `timeout_ms` and `interval_seconds` respected in RPC implementations
- Unbounded `authFailures` map growth in middleware
- Bootstrap test relaxed to not require external modules
- Mock implementations updated for new interface signatures
- Proto-lint CI job and `buf.yaml` removed

### Security

- 36 security findings resolved across all audit waves
- TLS hardening (gRPC and HTTP API)
- Cluster join token authentication
- Replace `os.Exit(1)` with `context.CancelFunc` in post-startup paths
- SECURITY.md rewritten with comprehensive policy
- AI transparency notice restored

## [v0.2.0] — 2026-05-25 — Module extraction & gRPC mesh

### Added

- gRPC mesh with `ModuleMesh`, `Health`, `Events`, and `Discovery` services
- Rate limiter extracted into standalone module
- Cache layer extracted into standalone module
- Health monitor extracted into standalone module
- Cross-node module mesh routing

### Changed

- Core modules extracted from built-in implementations into pluggable modules
- Module table and dependency counts updated in docs

### Fixed

- Doc updates for module table accuracy

## [v0.1.1] — 2026-05-25 — Module contracts

### Added

- `RateLimiterProvider` contract — per-key rate limiting for API middleware
- `CacheLayer` contract — read-through cache for storage orchestrator
- `HealthMonitor` contract — periodic module health checking
- SDK mock package `go.mod` for external importability

### Fixed

- Local replace directive removed from mock `go.mod`

## [v0.1.0] — 2026-05-25 — Initial release

### Added

- Project bootstrap — project skeleton, CI, test infrastructure, module structure
- Core contracts and gRPC mesh — module type contracts, rate limiter/cache/health monitor extraction
- `pkg/contracts/` — public API surface for module development
- CI pipeline with lint, test, build, coverage, docker, and vulnerability scanning
- Integration test with bootstrap and default preset modules
- Test coverage for 4 previously untested packages
- `sdk/go/mock` — mock implementations for testing
- `sdk/go/client` — SDK client package
- `proto/` — Protobuf definitions and generated gRPC code
- Dependency resolution from GitHub (removed local replace directives)

### Changed

- CI switched from self-hosted to GitHub-hosted runners
- Docs/wiki directory migrated to GitHub wiki
- Dependency count clarified (1 runtime dep: uuid)

[Unreleased]: https://github.com/Muxcore-Media/core/compare/v0.4.0...HEAD
[v0.4.0]: https://github.com/Muxcore-Media/core/compare/v0.3.0...v0.4.0
[v0.3.0]: https://github.com/Muxcore-Media/core/compare/v0.2.0...v0.3.0
[v0.2.0]: https://github.com/Muxcore-Media/core/compare/v0.1.1...v0.2.0
[v0.1.1]: https://github.com/Muxcore-Media/core/compare/v0.1.0...v0.1.1
[v0.1.0]: https://github.com/Muxcore-Media/core/releases/tag/v0.1.0
