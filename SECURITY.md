# Security Policy

## Status

MuxCore is **pre-1.0 alpha software**. The security features described below are
a mix of implemented, in-progress, and planned. This document distinguishes them clearly.

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| main    | :white_check_mark: |
| < 1.0   | :x:                |

## Reporting a Vulnerability

**Do not open a public issue.** Report via GitHub Security Advisories:
https://github.com/Muxcore-Media/core/security/advisories

Acknowledgment within **72 hours**. Target patch: **7 days** critical, **30 days** moderate.

## Scope

### Implemented

- **HTTP API**: Pluggable auth middleware (bearer token), rate limiting, audit logging, security headers (X-Content-Type-Options, X-Frame-Options, Referrer-Policy, Permissions-Policy)
- **gRPC mesh**: TLS encryption (required in production), mTLS with CA verification (when configured)
- **Cluster discovery**: Join token authentication via gRPC metadata
- **Storage**: Key sanitization (path traversal prevention), max object size enforcement (100MB)
- **Event bus**: Per-handler timeouts (30s), structured logging, source node validation
- **Config**: Environment variable overrides with validation, seed node address validation
- **Docker**: Non-root user, credential file exclusion from builds, HEALTHCHECK; read-only root filesystem and capability dropping applied via docker-compose.yml
- **CI/CD**: Read-only GITHUB_TOKEN, actions by version tag (not commit SHA), govulncheck (floating @latest), fuzz testing

### In Progress

- **RBAC enforcement**: Authorizer interface exists; enforcement at API level only (not gRPC or event bus)
- **Cluster join authentication**: Token required; mTLS CA verification available; cross-node routing not yet implemented
- **Auth failure rate limiting**: Per-IP brute-force protection with fixed 1-minute backoff after 5 failures

### Planned (Not Yet Implemented)

- **Module capability enforcement**: Capabilities are declared but not runtime-enforced. Capability checks in event dispatch, mesh routing, and storage access are planned.
- **Module sandboxing** (gVisor): Not implemented — modules run in-process with no isolation.
- **OIDC / SSO**: No implementation exists yet.
- **Event authorization**: No per-event-type access control.
- **gRPC interceptors**: Auth, rate limiting, and logging not yet wired as gRPC interceptors.

## Security Model

MuxCore's intended security boundary is **between modules, not inside them**.
Core provides the fabric — event bus, registry, lifecycle — and each module should
run with the capabilities it declared. A module that declares only
`downloader.torrent` should not be able to read the filesystem or call the
notification system.

**Current state (2026-05-26)**: Module capabilities are self-declared and not
enforced at runtime. The boundaries described above are the design target, not
the current reality. Capability enforcement is planned for a future release.

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
