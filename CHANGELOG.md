# Changelog

All notable changes to the MuxCore project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Additive `AuthService.ListSessions` and `RevokeSession` contracts for T-M5-11 (FR-AUTH-007): paginated active sessions with non-credential IDs, user-scoped idempotent revocation, and an explicit admin end-user authentication requirement. Existing auth providers remain compatible through `UnimplementedAuthServiceServer`; provider implementations and admin-ui integration are separate work.

### Security
- Spool tag fetches now resolve and validate destinations at connection time and dial only checked IP addresses, closing the DNS validation/connection gap and hostname redirect bypass (NFR-SEC-009, RULE-VAL-2). Exact operator host/port allow-lists retain LAN and loopback access; metadata, link-local, and unsafe address encodings remain forbidden even when listed. Each fetch keeps one policy snapshot through redirects. The client ignores implicit HTTP(S) proxy settings because proxy-side DNS would defeat address pinning; URL userinfo is rejected. HTTPS certificate verification, the ten-second timeout, the existing third-redirect refusal, and the one-MiB response read limit remain in place.

## [sdk/go/module v0.6.6] — 2026-10-05 — netguard and pathguard

### Added
- Go module SDK: new package `sdk/go/module/netguard` (RULE-VAL-2, NFR-SEC-009), a stdlib-only SSRF guard for outbound HTTP to configured or user-supplied URLs. Profiles `UserURL` (zero value; refuses loopback, RFC 1918, CGNAT, ULA, link-local, multicast, reserved, documentation and cloud-metadata destinations, single-label and intranet-suffix names, userinfo) and `Integration` (admin-configured endpoints; private LAN only with `Options.AllowPrivate`, loopback only with `Options.AllowLoopback`; link-local, multicast, unspecified and metadata always refused). `ValidateURL`, `CheckHost`, `CheckAddr` check without network access; `NewClient` returns an `*http.Client` that validates every request and redirect hop (max 5), resolves and checks every address at dial time and connects only to a checked address (DNS-rebinding safe), times out after 30 s by default and ignores environment proxies unless `Options.UseEnvProxy`. Non-canonical IPv4 literals (decimal, octal, hex, short forms) are refused; IPv4-mapped, NAT64 and 6to4 addresses are judged by their embedded IPv4 address. Optional host allow-list (`Options.AllowedHosts`). Refusals wrap `ErrBlocked` (`*BlockedError` carries the reason).
- Go module SDK: new package `sdk/go/module/pathguard` (RULE-VAL-1, NFR-SEC-008): `Confine(path, roots)` (absolute input, refuses `..` segments and NUL, resolves symlinks on the nearest existing ancestor and on the roots, component-wise containment so `/data/movies2` is not under `/data/movies`, returns the real path), `Join(root, rel)` for untrusted relative names, and `SanitizeComponent(s, maxLen)` for single file-name parts (`[A-Za-z0-9._-]`). Errors wrap `ErrOutsideRoots` or `ErrInvalidPath` (dangling symlinks are refused).

## [v0.6.15] — 2026-10-05 — sdk/go/module v0.6.5 meshtls; image mount points

### Added
- Go module SDK: new package `sdk/go/module/meshtls` (ADR-0016/0017) so sidecars serve and dial TLS from the `MUXCORE_*` environment instead of plaintext: `Insecure()`, `ServerOption()` (TLS from `MUXCORE_TLS_CERT`/`KEY`, client certificates verified against `MUXCORE_TLS_CA` when presented), `DialOption(serverName)` (roots from `MUXCORE_TLS_CA`, client certificate when set, `MUXCORE_TLS_SERVER_NAME` override), and the conveniences `NewServer` and `Dial`. Plaintext is refused in the household/staging profiles.

### Fixed
- `Dockerfile`: pre-create `/app/ca` (0700) and `/app/mesh-ca` (0755) owned by `muxcore`, the mount points compose volumes use, so the compose `mesh-init` chown service is no longer needed.

## [v0.6.14] — 2026-10-05 — dev restarts; SDK unregister and re-register (sdk/go/module v0.6.4)

### Fixed
- Restart crash loop in the `dev` profile (found by the first compose run, T-M2-10): a sidecar that restarted without unregistering (e.g. `podman restart media-rename`) was refused forever with "module X already registered", because only a verified certificate could replace an existing ID (ADR-0018 row 5). In `dev`, an unverified re-registration of the same ID now atomically replaces the stale sidecar entry, with a warning, provided that entry was itself registered without a verified certificate (an unverified peer cannot displace a certificate-registered module). The replacement keeps the entry's registration order, so the ID stays the provider of record for exclusive security capabilities; a different ID still cannot replace or take over. `household` is unchanged: same-ID replace requires the verified certificate. In-process modules are never replaced.
- Go module SDK (`sdk/go/module`): on SIGTERM/SIGINT `Run` now unregisters from core first (best effort, 3 s timeout, shutdown not blocked by an unreachable core) and then stops the module; previously it stopped the module first and unregistered with an unbounded call. It also unregisters (best effort) when `Init`/`Start` fail after registration, or when shutdown interrupts the initial `Register`.
- Go module SDK: modules re-register when core restarts. `Run` supervises the registration every `Config.SupervisionInterval` (default 15 s; negative disables) by asking core's `HealthService/Check` for the module ID (`STATUS_UNHEALTHY` = unknown to core; `x-caller-id` in dev, the certificate under mTLS); when core has forgotten the module, or (for a core that cannot answer) after core comes back from being unreachable, it re-registers on the same connection and TLS identity, with exponential backoff (up to 60 s) on failure. If core refuses the probe (`PermissionDenied`/`Unauthenticated`) it is not retried, so it cannot accumulate authentication failures; supervision then falls back to the connection state. New `RunContext(ctx, Config)` (Run with a caller-supplied shutdown context).

## [v0.6.13] — 2026-10-05 — bounded gRPC shutdown

### Fixed
- Shutdown no longer hangs in `GracefulStop` when long-lived streams (EventService.Subscribe) stay open: after 10 s the gRPC server is stopped hard (found by the umbrella restore drill).

## [v0.6.12] — 2026-10-05 — module enrollment (sdk/go/module v0.6.2, sdk/go/client v0.6.1)

### Security
- Module enrollment (ADR-0017 decision 3, NFR-SEC-002, T-M3-03c). `BootstrapRegisterRequest` gains `csr_pem` and `dns_names` (additive; `registration.pb.go` regenerated with protoc 29.3 / protoc-gen-go v1.36.12). With a CSR, core signs the module's own public key (CN = module ID; the CSR's subject/SANs are ignored; ECDSA P-256/P-384, Ed25519 or RSA ≥ 2048), returns the certificate chain and CA, and returns no key; the module keeps its key and core writes none. SANs are always `localhost`, `127.0.0.1`, `::1` and the module ID, plus requested `dns_names` allowed by the new `MUXCORE_ENROLL_SAN_ALLOW` (exact names/IPs or `*.suffix`; default none); others are dropped with a warning. The allow-list also applies to the legacy path (no CSR; key generated in memory).
- New enrollment tokens `mct_2_<id>_<hex(HMAC-SHA256(MUXCORE_ENROLL_SECRET, id))>` (new `MUXCORE_ENROLL_SECRET`, at least 16 bytes; a shorter value is fatal at startup). They require a CSR and are single-use through the persisted ledger `<ca dir>/enrolled.json` (0600, atomic temp+fsync+rename, advisory file lock, re-read on every use so a reset applies to a running core; a corrupt ledger fails closed). Requests are checked (CSR, token/module-ID match) before the token is consumed. Without the secret only the existing in-memory 5-minute `mct_1_` tokens are accepted, unchanged. New internal package `internal/enroll`; `CertAuthority.ConfigureEnrollment`, `SignModuleCSR`, `EnrollmentSANs`, `Dir`.
- New `muxcored enroll token <id>` (prints the token; requires the secret; warns if the ID is already enrolled), `muxcored enroll reset <id>`, `muxcored enroll list` (`--ca-dir`, else the CA dir muxcored resolves at startup).
- Go module SDK (`sdk/go/module`, ADR-0017 decision 4, T-M3-03d): new package `meshid` with `Ensure(ctx, Config) (Paths, error)`: uses `MUXCORE_TLS_CERT`/`KEY` when set; else reuses `module.crt`/`module.key` in `MUXCORE_TLS_DIR` (default `<MUXCORE_DATA_DIR or ./data>/mesh-id`) without contacting core; else enrolls with `MUXCORE_BOOTSTRAP_TOKEN` over server-verified TLS (CA from `MUXCORE_TLS_CA` or `MUXCORE_CA_EXPORT_DIR/ca.crt`, no client certificate), generating an ECDSA P-256 key locally, verifying the issued certificate (key, CN, chain to the pinned CA), and storing it (files 0600, directory 0700). It exports `MUXCORE_TLS_CERT`/`KEY`/`CA` for the process. Optional `MUXCORE_ENROLL_DNS_NAMES`, `MUXCORE_TLS_SERVER_NAME`. No-op with the insecure flag. `modulesdk.Run` calls it before registering (and so before Init/Start) unless `Config.Insecure`. Requires core with this release.
- SDK household guard (ADR-0016, T-M3-02d): `modulesdk.Run` and `meshid.Ensure` return `ErrInsecureInHousehold` when `MUXCORE_PROFILE` is `household`/`staging` and the module is insecure; an unset profile with the insecure setting still resolves to dev (phase 0).
- Go client SDK (`sdk/go/client`): `Dial` now loads TLS from `MUXCORE_TLS_CA`/`MUXCORE_TLS_CERT`/`MUXCORE_TLS_KEY` (`MUXCORE_TLS_SERVER_NAME`) by default (new `TransportCredentialsFromEnv`); with none set, `MUXCORE_INSECURE_DISABLE_TLS` selects plaintext. `WithInsecure` and `WithGRPCOption` credentials still override; `WithInsecure` under `MUXCORE_PROFILE=household` returns `ErrInsecureInHousehold`. **Behaviour change**: a malformed `MUXCORE_TLS_*` environment now fails `Dial` even when explicit credentials are passed.

## [v0.6.11] — 2026-10-05 — authenticated registration, tenant headers

### Security
- Authenticated registration (ADR-0018, NFR-SEC-002, T-M3-03b). `ModuleRegistration/Register` and `Unregister` stay open at the interceptor but the service now applies the ADR-0018 rules using the profile and `VerifiedModuleID`: a verified certificate whose CN differs from the registering module ID is rejected `PermissionDenied` in both profiles. Without a verified certificate, registering a security capability (`call.policy`, `publish.policy`, `authorizer`, `identity`, `auth`) warns in `dev` and is rejected `PermissionDenied` in `household`; other capabilities are allowed in `dev` and allowed with a warning in `household` (stage 1). New `MUXCORE_REQUIRE_MODULE_CERTS=true|1` (default off; household only, warned and ignored in dev) enables stage 2: every registration without a verified certificate is rejected. A second provider (different ID) of a security capability warns in `dev` (the first-registered provider stays wired) and is rejected `FailedPrecondition` in `household`, checked atomically with the registry update. The verified owner re-registering its own sidecar ID atomically replaces the entry (keeping its provider-of-record position); without a certificate a duplicate ID is still refused, and in-process modules are never replaced. A rejected registration no longer replaces the tracked sidecar proxy of the registered module. `Unregister` is open in `dev`; in `household` it requires a verified certificate with CN equal to the module ID (there is no user-admin path: the method is unauthenticated at the interceptor). `muxcored` sets the policy from the profile (`Manager.SetRegistrationPolicy`; an unset manager uses the dev rules).
- Deterministic security wiring: call/publish policy, authorizer, identity and auth providers were taken from `entries[0]` of a map iteration (random when several modules declared the capability). New `registry.Provider(capability)` returns the provider of record (first registered still present), and `WireCallPolicy`/`WirePublishPolicy`/`WireAuth` wire it. `Registry.List`, `ListByRole`, `ListByCapability` (and `FindByCapability`, `ListAll`, `StartupOrder`) now return a stable order: registration order, then module ID. New `Registry.RegisterWith(module, deps, RegisterOptions{Replace, Exclusive})` with `ErrAlreadyRegistered` / `ErrExclusiveConflict`; `module.Manager.RegisterWith` passes it through and audits `replaced=true`.
- `VerifiedModuleID` moved to the leaf package `internal/peerid` (so the module manager can use it); `grpcmesh.VerifiedModuleID` delegates.
- `pkg/tenant` no longer trusts client tenant headers (ADR-0019 §3, NFR-SEC-007, T-M3-06d): `Middleware` and `ResolveFromRequest` honour `X-Tenant-ID`, `X-Auth-Claims-Tenant` and the `tenant_id` query parameter only with the new `TENANT_TRUST_HEADERS=1` (logged once when ignored). New `WithAuthenticatedTenant(ctx, tenant)` / `AuthenticatedTenantFrom(ctx)` let callers that resolved the tenant from an authenticated identity record it; both functions prefer it over any header. **Behaviour change** for multi-tenant (`TENANT_MODE=1`) deployments that relied on BFF-forwarded tenant headers: they get `default` unless the module sets the authenticated tenant or opts in.

## [v0.6.10] — 2026-10-05 — security profiles

### Security
- Security profiles (ADR-0016, NFR-SEC-003, T-M3-02a). New `internal/profile`: `MUXCORE_PROFILE=dev|household` (`staging` = household). Phase-0 inference: unset with `MUXCORE_INSECURE_DISABLE_TLS` (or deprecated `MUXCORE_DEV_TLS_SKIP`) → `dev` with a deprecation warning; unset without it → `household`. `sqlite`/`postgres` (legacy installer DB selector) are treated as unset with a warning pointing at `MUXCORE_DB_BACKEND`; any other value is fatal. Core logs the resolved profile and how it was resolved.
- `household`: the insecure flag is fatal at startup; TLS is always on. Without certificate files core creates its CA under `<MUXCORE_DATA_DIR>/ca` (new `MUXCORE_DATA_DIR`, default `./data`; an existing legacy `~/.muxcore/ca` CA is reused with a warning; `MUXCORE_GRPC_CA_CERT_DIR` still overrides) and issues its own server certificate (SANs: loopback, `localhost`, `muxcore`, `muxcored`, host name, gRPC listen host, plus new `MUXCORE_TLS_SERVER_SANS`), also used for HTTP when no HTTP cert is set. The gRPC server verifies client certificates against the core CA when presented (`VerifyClientCertIfGiven`, so `VerifiedModuleID` identifies modules); explicit `mtls_enabled` keeps `RequireAndVerifyClientCert`. New `MUXCORE_CA_EXPORT_DIR` exports the public `ca.crt`. `dev`: insecure allowed, startup banner, `x-caller-id` accepted as module principal only with the insecure flag. `/health` and `/version` report `"profile"` and `"insecure"`.
- Sidecar dial credentials (T-M3-02b): core dialled sidecar providers (call/publish policy, authorizer, identity, auth, storage) with its *server* TLS credentials (no root CAs), so TLS sidecar wiring could not verify. `bootstrap.DialSidecar(moduleID, addr, *grpcmesh.SidecarClientTLS, max)` now verifies the sidecar chain against the core CA (+ configured CA) and requires the certificate CN to equal the registered module ID, and presents core's certificate. Plaintext remains only for the dev/insecure profile (localhost only).
- Signatures in household (FR-EXT-003, T-M3-02c): marketplace `DeployTag` and orphan resurrection require a valid signature from a configured trusted key (`Manager.VerifyMarketplaceSignature`, `spool.VerifyArtifactSignatureRequired`); boot-time curated tags stay checksum-anchored (ADR-0012). `dev` keeps the `MUXCORE_SPOOL_REQUIRE_SIGNATURE` opt-in. Marketplace deploys are effectively disabled in household until signed spool entries and keys exist.
- `BootstrapRegister` no longer leaves the issued module private key in `os.TempDir()` (it wrote `module.crt`/`module.key` there, shared by all callers, and never removed them): the key is now generated and returned in memory (SANs: loopback, `localhost`, module ID); issuers without in-memory support use a private 0700 temp dir removed before the response.

## [v0.6.9] — 2026-10-05 — modules as service principals

### Security
- Mesh module identity now comes from the transport (ADR-0017 decisions 1–2, NFR-SEC-002/003, T-M3-03a). New `grpcmesh.VerifiedModuleID(ctx)` returns the CN of a client certificate that verified against the core CA. The gRPC auth interceptor resolves principals as follows: a user bearer token (`authorization`) goes through the identity provider and `Authorizer.Can` exactly as before; otherwise a verified certificate makes the CN the module principal and any client-supplied `x-caller-id` is ignored (logged at debug when it differs); otherwise, only with `MUXCORE_INSECURE_DISABLE_TLS` (or the deprecated `MUXCORE_DEV_TLS_SKIP`) on a plaintext connection, `x-caller-id` is accepted as the module principal with a one-time warning. Over TLS without a client certificate `x-caller-id` is never trusted. The identity provider is no longer sent `x-caller-id`.
- Module principals are authorized by core method rules plus the existing call/publish policy, not by the user `Authorizer`: they may use EventService, StorageService, ModuleMesh, HealthService, discovery reads and `AuditService/Log`. Lifecycle, Spool/marketplace, audit `Query`/`Export`/`VerifyChain` and discovery `Leave` require a user bearer token. Inbound `ModuleMesh/Call` and `StreamCall` from module principals now pass the call policy (deny when none is configured).
- Auth-failure backoff is keyed by the authenticated module ID or the peer address, never by an unverified `x-caller-id`, so rotating it no longer evades backoff and a remote caller cannot lock out another module.

### Fixed
- Module calls to core (`EventService.Publish`, storage) failed `Unauthenticated` in every mode once auth-local ≥ v0.1.14 was loaded, because core asked auth-local to confirm a caller ID on a connection whose TLS peer was core itself.

## [v0.6.8] — 2026-10-05 — audit HMAC, metrics listener

### Security
- Audit log HMAC is now wired (NFR-OPS-004): set `MUXCORE_AUDIT_HMAC_KEY` or `MUXCORE_AUDIT_HMAC_KEY_FILE` (or `audit.hmac_key_file`) and each entry carries an HMAC-SHA256 over (previous hash || canonical entry bytes). `audit.VerifyEntries` / `VerifyAll` report the first broken entry; with a key a missing or wrong signature counts as broken. A configured but unreadable/empty key file aborts startup; a world-accessible key file only warns. Without a key behaviour is unchanged (hash chain only). Also fixes chain verification of signed entries (the signature was included in the verified hash).
- `/metrics` (NFR-SEC-011) is now served on its own listener, default `127.0.0.1:9464` (`MUXCORE_METRICS_ADDR`), no longer on the main API port and no longer an unauthenticated public path. Binding a non-loopback address requires a bearer token (`MUXCORE_METRICS_TOKEN` / `MUXCORE_METRICS_TOKEN_FILE`, constant-time compare) or core refuses to start. **Breaking for scrapers** that used `<api-addr>/metrics`.

## [sdk/go/module v0.6.1] — 2026-10-05 — upgrade-test helpers

### Added
- `sdk/go/module/moduletest` (to be released as `sdk/go/module/v0.6.1`): stdlib-only SQLite upgrade-test helpers `CopyFixture`, `Schema`, `RequireSchemaSuperset`, `RequireIntegrity` (ADR-0015, NFR-DATA-002, FR-INS-005). `modernc.org/sqlite` is a test-only dependency of the nested module.

## [v0.6.7] — 2026-10-05 — release workflow SBOM tool

### Fixed
- Release workflow installs `syft` before GoReleaser (the SBOM step failed with "executable file not found", so v0.6.6 published no binaries).

## [v0.6.6] — 2026-10-05 — release workflow toolchain

### Fixed
- Release workflow reads the Go version from `go.mod` (it pinned 1.26.5 while `go.mod` requires 1.26.6, so v0.6.2–v0.6.5 published no binaries).

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
