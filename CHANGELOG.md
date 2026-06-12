# Changelog

All notable changes to the MuxCore project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Cluster adapter wrapping `DiscoveryServer` to implement `contracts.Cluster` with event polling for cross-node module call routing
- Local filesystem `StorageProvider` (`internal/storage/local`) with atomic writes, metadata tracking, and streaming support
- Storage tiering — `DiscoverTiers`, `Promote`, `Relegate` with tier-aware provider routing (hot > warm > cold > archive)
- Standard gRPC health probe (`grpc.health.v1`) for K8s compatibility
- `SetProvider` method on orchestrator for built-in providers
- Configuration env vars: `MUXCORE_STORAGE_DIR`, `MUXCORE_STORAGE_{READ,WRITE,DELETE}_TIMEOUT`, `MUXCORE_AUDIT_MAX_SIZE_MB`, `MUXCORE_AUDIT_MAX_ROTATED_FILES`

### Changed

- Event bus WAL flushes and drains in-flight events on close (`MemoryBus.Close()`)
- WAL replay timeout is now configurable via `WALReplayTimeout` field
- Discovery server double-close guarded with `sync.Once`
- Checksum verification for module resolution is now mandatory
- Missing security capability warnings upgraded to ERROR level
- gRPC error responses sanitized to prevent info leakage
- Storage key namespace isolation enforced per-module
- Audit exports streamed instead of buffered fully in memory

### Fixed

- Wire `SetAuditLogger`, `SetAuthorizer`, `SetRateLimiter`, `SetAuthFunc` on API server
- Wire `Authorizer`/`IdentityProvider` to gRPC `AuthInterceptor`
- Wire `InitAll`/`StartAll` for module lifecycle state transitions
- Module `TrackProxy`/`Spawn` race condition resolved with `pendingProcesses` map
- `Subscribe` stream.Send serialized through a single goroutine (fixes "transport: SendHeader called multiple times" race)
- Security audit findings from SEC-AUDIT-2026-06-08, including:
  - Repo URL allow-list validation for module resolution
  - Built-in default rate limiter (100 req/s per IP token bucket)
  - Removal of `MUXCORE_GRPC_REQUIRE_EVENTS_AUTH` and `MUXCORE_GRPC_REQUIRE_DISCOVERY_AUTH` env var bypasses
- 17 security audit findings addressed (SEC-001 through SEC-016)
- Code quality audit findings across 8 files (redundant guards, dead code, excessive comments)

### Security

- Checksum verification made mandatory for module resolution (SEC-001/SEC-002)
- Removed authentication bypass env vars (SEC-004)
- gRPC error sanitization to prevent information leakage (SEC-003)
- Per-module storage key namespace isolation (SEC-005)
- Streamed audit exports to prevent OOM (SEC-011)

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
- Code quality audit findings across 8 files
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
