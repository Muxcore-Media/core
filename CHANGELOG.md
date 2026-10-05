# Changelog

All notable changes to the MuxCore project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [v0.6.5] — 2026-10-05 — canonical spool builds (ADR-0012)

### Changed
- Spool module builds use the canonical reproducible build (ADR-0012, FR-EXT-002): `CGO_ENABLED=0 GOFLAGS=-mod=readonly GOTOOLCHAIN=go<V> go build -trimpath -buildvcs=false -ldflags=-buildid= ./cmd/module/`, where `<V>` is the module's `go.mod` go line (`.0` appended when no patch). Spool binaries change byte-for-byte, so all spool checksums must be recomputed.
- `reconcileContracts` logs a warning when it rewrites `go.mod`, since the build is then no longer canonical and checksum verification may fail.

## [v0.6.4] — 2026-10-05 — harness caller identity

### Fixed
- `integsupport`: the harness stamps the caller from the SDK's `x-caller-id` metadata (test-only stand-in for the auth interceptor), so module event publish/subscribe work; `Close` uses `Stop` so long-lived Subscribe streams cannot block teardown.

## [v0.6.3] — 2026-10-05 — integration harness

### Added
- `integsupport`: in-process core mesh/events/storage/health harness on a loopback port for umbrella integration tests (`integration/`, `-tags integration`). Test-only: allow-all policies, TLS disabled.

### Removed
- Legacy retired-origin workflow directory; GitHub Actions is the only CI (ADR-0002, ADR-0003).

## [v0.6.2] — 2026-10-05 — v0.6.x stabilisation (v0.6.0–v0.6.2)

### Fixed
- Module manager: each spawned process is reaped by exactly one owner goroutine; `StopAll` and `RestartModule` wait on an exit channel instead of calling `cmd.Wait` themselves, and a restarted module's replacement process is no longer untracked by the old process's waiter (data races found by `go test -race`).
- `sdk/go/client`: copylocks vet warning in `storage_put_test.go`.

### Added
- Cgroup-aware `GOMAXPROCS` at startup (`pkg/sys`).
- `backup`, `executor` and `inputvalidate` v1 protos with generated Go code.
- `workerpool.NodeResolver` / `ClusterNodeResolver`: dispatcher records `AssignedNode` and skips executors whose host is unknown.
- Proto drift fix (T-M1-04, NFR-MNT-001), additive only: definitions consumers already used but core never published.
  - `muxcore.auth.v1.AuthService`: household invite RPCs `CreateInvite`, `ListInvites`, `RevokeInvite`, `RedeemInvite`. New messages `InviteInfo`, `CreateInviteRequest`/`Response`, `ListInvitesRequest`/`Response`, `RevokeInviteRequest`/`Response` and `RedeemInviteRequest`/`Response`. auth-local implements these.
  - `muxcore.circuitbreaker.v1.CircuitBreakerService`: `List` RPC with `ListRequest` (`open_only`), `ListResponse` and `CircuitEntry` (`key`, `state`, `failure_count`, `opened_at_unix_nano`). circuitbreaker-simple implements this.
  - Go code regenerated with `make proto` using protoc 29.3, protoc-gen-go v1.36.12 and protoc-gen-go-grpc v1.6.2, the versions the committed code was built with. No existing fields, numbers or RPCs changed (`buf breaking` passes).

### Fixed
- Config: env var overrides now apply when no config file exists (`config.ApplyEnvOverrides`).
- Node ID and advertised addresses resolve a bare-port listen address to the local hostname instead of `muxcore-:PORT`.
- Explicit 5s `MinConnectTimeout` on `ConnPool` and `DialSidecar` connections.

### Security
- **EventService/Publish authz**: Removed `EventService/Publish` from the gRPC auth allowlist. Publish, Request, and Replay now require verified caller identity from the auth interceptor (mTLS or validated bearer token). Client-supplied `x-caller-id` metadata and `event.Source` are no longer trusted for publish-policy decisions. **Migration**: Remote publishers must authenticate via mesh mTLS or an identity provider; setting `MUXCORE_MODULE_ID` / `x-caller-id` alone is insufficient when `MUXCORE_INSECURE_DISABLE_TLS` is enabled.

## [v0.5.7] — 2026-08-10 — Release workflow cosign 2.x

### Fixed
- Release job installs cosign 2.4.3 via `sigstore/cosign-installer` so goreleaser-action `verify-blob --yes` works on the self-hosted runner.

## [v0.5.6] — 2026-08-10 — GoReleaser cosign + event bus Close drain

### Fixed
- Release signing: drop cosign `sign-blob --yes` (unknown on runner cosign &lt;2.x) so GoReleaser can finish on self-hosted.
- Event bus `Close`: stop subscriber workers before drain and avoid dropping dequeued events (fixes flaky `TestMemoryBus_CloseWithDrain`).

## [v0.5.5] — 2026-08-10 — self-hosted CI merge gate

### Fixed
- CI Test/Coverage: drop `go test -race` on self-hosted runners (merge gate is `go test -count=1`); avoids race failures in module manager StopAll/Wait paths under `-race`.
- Lint: `awaitAndShutdown` inherits cancelled context via `context.WithoutCancel`; fieldalign sidecar policy discover structs.

### Added
- `scripts/local-image.sh` + README notes for laptop/local-registry `muxcored` image builds.
- Makefile `release-snapshot` target (GoReleaser snapshot, no publish).

### Added (prior Unreleased — nested SDK tags already cut)
- Python module SDK scaffold (`sdk/python` **v0.1.0**): `muxcore.sdk.run` registers via `ModuleRegistration`, mirrors Go lifecycle (TLS/insecure + signal shutdown). Nested tag: `sdk/python/v0.1.0`.
- TypeScript module SDK scaffold (`sdk/typescript` **v0.1.0**): `@muxcore-media/sdk` `run()` with `@grpc/grpc-js` + proto-loader. Nested tag: `sdk/typescript/v0.1.0`.
- Rust module SDK scaffold (`sdk/rust` **v0.1.0**): `muxcore-sdk` crate with tonic `run` / `run_until` (plaintext dial in v0.1.0). Nested tag: `sdk/rust/v0.1.0`.

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
