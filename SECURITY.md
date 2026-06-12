# Security Policy

## Status

MuxCore is **pre-1.0 beta software**. The `v0.1.0` tag marks the first
named release. APIs and module interfaces are not yet stable. The security features described below are
a mix of implemented, in-progress, and planned. This document distinguishes them clearly.

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| main    | :white_check_mark: |
| v0.1.x  | :white_check_mark: |
| < v0.1  | :x:                |

## Reporting a Vulnerability

**Do not open a public issue.** Report via GitHub Security Advisories:
https://github.com/Muxcore-Media/core/security/advisories

Acknowledgment within **72 hours**. Target patch: **7 days** critical, **30 days** moderate.

## Scope

### Implemented

- **HTTP API**: Pluggable auth middleware (bearer token), rate limiting, audit logging, security headers (X-Content-Type-Options, X-Frame-Options, Referrer-Policy, Permissions-Policy, CSP, HSTS when TLS active)
- **gRPC mesh**: TLS encryption (required in production), mTLS with CA verification (when configured), keepalive enforcement
- **Cluster discovery**: Join token authentication via gRPC metadata (constant-time comparison)
- **Storage**: Key sanitization (path traversal prevention), max object size enforcement (100MB), capability-based access control via CallPolicyProvider
- **Event bus**: Per-handler timeouts (30s), structured logging, source node validation, deny-by-default publish policy with capability enforcement via PublishPolicyProvider
- **Module capability enforcement**: Built-in call policy (mesh routing + storage access) and publish policy (event dispatch), both backed by the module registry. Deny-by-default: all inter-module calls and event publications are denied until a policy module is deployed.
- **Config**: Environment variable overrides with validation, seed node address validation, TLS cert validation at boot
- **Docker**: Non-root user, credential file exclusion from builds, HEALTHCHECK; read-only root filesystem and capability dropping applied via docker-compose.yml
- **CI/CD**: Read-only GITHUB_TOKEN, actions pinned by commit SHA, verified binary downloads with SHA256 checksums, govulncheck at pinned version, fuzz testing
- **RBAC enforcement layer**: Authorizer interface is enforced at the HTTP API and gRPC layers. The core provides the enforcement framework — the policy engine (role definitions, permission-to-role mapping, user-to-role assignment) is provided by an auth module via `SetAuthorizer()`/`SetIdentityProvider()`.

### In Progress

- **Auth failure rate limiting**: Per-IP brute-force protection with fixed 1-minute backoff after 5 failures

### Completed Since Last Audit

- **Cross-node routing**: PRs #54 and #55 resolved cross-node routing gaps. Mesh routing now functional across cluster nodes.
- **Cluster join authentication**: Token required; mTLS CA verification available; cross-node routing implemented
- **May 2026 audit fixes (PR #58, #59, #60)**:
  - Credential URL redaction in structured logs (slog.LogValuer on DatabaseConfig/CacheConfig)
  - Request body size enforcement via MaxBytesReader middleware (10MB limit)
  - CI lint failures now fail the build (removed `|| true` / `continue-on-error`)
  - Stream fallback read capped at MaxObjectSize (CWE-770)
  - JSON encode errors logged instead of silently dropped
  - Insecure heartbeat transport requires explicit env opt-in
  - Wiki call policy docs corrected to deny-by-default
  - Spool checksum verification support (TagModule.Checksum field)
  - gRPC auth interceptor supports configurable Authorizer+IdentityProvider enforcement
  - SidecarProxy.Health reports actual process exit status
  - Docker HEALTHCHECK works with both HTTP and HTTPS
  - Seed node auto-join TLS guard, gRPC keepalive, CRLF trace blocker, TLS boot validation
- **Module capability enforcement (PR #62)**: Built-in call policy and publish policy wired at bootstrap. Storage gRPC server enforces "storage" capability on all operations. Event bus deny-by-default with PublishPolicyProvider enforcement.
- **Core completion (PR #69)**:
  - Term-based cluster leader election — stale terms rejected, term propagated via gRPC metadata
  - Core version compatibility enforcement — `MinCoreVersion` checked at module registration
  - Startup invariant checks — misconfigurations caught before any subsystem initialises
  - Event bus backpressure — per-subscriber bounded channels (256), drop-on-full with counters
  - gRPC connection pool — heartbeat reuses connections; no per-request dial
  - Config hot-reload via SIGHUP — secrets never logged, unsafe changes blocked
  - Graceful shutdown order — HTTP drain before module stop prevents requests hitting stopped modules
  - Admin endpoints — all require authentication with `admin.access` permission
  - `/debug/pprof` — disabled by default, requires `MUXCORE_DEBUG_ENABLE=true`
  - Heartbeat term-extraction bug fix — response headers read correctly (was reading outgoing metadata)
  - `startup.RunAll` no longer calls `os.Exit` from library code

## Security Model

MuxCore's intended security boundary is **between modules, not inside them**.
Core provides the fabric — event bus, registry, lifecycle — and each module should
run with the capabilities it declared. A module that declares only
`downloader.torrent` should not be able to read the filesystem or call the
notification system.

**Current state (2026-05-27)**: Module capabilities are enforced at runtime via
call policy (mesh routing + storage access) and publish policy (event dispatch).
Modules run as sidecar processes — they connect to core via gRPC and execute
with the same OS-level privileges as the core process. Sandboxing is a
deployment concern (Docker, K8s, gVisor), not a core concern.

When reporting vulnerabilities, frame issues against both the intended and
actual boundaries. A bug in a module's business logic that stays within its
own capabilities is a regular bug, not a security vulnerability.

## Disclosure Policy

1. Reporter submits private report
2. Maintainers triage within 72 hours, assign severity
3. Fix developed in private fork; reporter credited (with permission)
4. GitHub Security Advisory published with fix release
5. CVE requested for critical vulnerabilities

We follow **coordinated disclosure**. Default window: 30 days before public disclosure.

## Safe Harbor

We will not pursue legal action against researchers who:
- Test against their own MuxCore instance
- Avoid accessing or modifying data that does not belong to them
- Make a good-faith effort to avoid degradation of service during testing
- Follow this policy's reporting and disclosure process

## Recognition

| Name | Issue | Date |
| ---- | ----- | ---- |
| ---  | ---   | ---  |
