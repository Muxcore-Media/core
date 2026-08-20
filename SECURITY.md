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

**Current state (2026-08-20)**: Module capabilities are enforced at runtime via
call policy (mesh routing + storage access) and publish policy (event dispatch).
Modules run as sidecar processes — they connect to core via gRPC and execute
with the same OS-level privileges as the core process **unless** an optional
sandbox runner is enabled.

### Module sandboxing (optional)

| Env | Values | Behavior |
| --- | ------ | -------- |
| `MUXCORE_MODULE_SANDBOX` | `none` (default), `gvisor`, `firecracker` | Selects spawn wrapper |
| `MUXCORE_SANDBOX_GVISOR_BIN` | path (default `runsc`) | gVisor binary |
| `MUXCORE_SANDBOX_FIRECRACKER_BIN` | path (default `firecracker-spawn`) | Firecracker helper |
| `MUXCORE_SANDBOX_OCI_AUTO` | `1` | gVisor: write OCI bundle (`sandbox/oci`) and `runsc run --bundle` |
| `MUXCORE_SANDBOX_SECCOMP` | `1` | Apply default seccomp (`sandbox/seccomp/default.json`) on `runsc do exec`; OCI gVisor bundles always embed the profile |
| `MUXCORE_SANDBOX_FC_ROOTFS` | path | Firecracker rootfs image or directory; Wrap fail-closed if unset/missing unless `ROOTFS_AUTO=1` |
| `MUXCORE_SANDBOX_ROOTFS_AUTO` | `1` | Build minimal BusyBox rootfs (`sandbox/rootfs`) into OCI bundles / Firecracker when `FC_ROOTFS` unset |
| `MUXCORE_SANDBOX_ROOTFS_TEMPLATE` | path | Copy an existing rootfs tree into the OCI bundle instead of synthesizing |
| `MUXCORE_SANDBOX_ROOTFS_PROFILE` | `busybox` (default), `alpine-minimal` | Distro-oriented layout on top of BusyBox baseline |
| `MUXCORE_SANDBOX_FC_KERNEL` | path | Guest kernel for Firecracker microVM config |
| `MUXCORE_SANDBOX_FC_VSOCK_CID` | int (default 3) | vsock guest CID in generated Firecracker config |
| `MUXCORE_SANDBOX_FC_VSOCK_UDS` | path | Host vsock UDS path |
| `MUXCORE_SANDBOX_FC_DRY_RUN` | `1` | Write Firecracker config and exit (CI/fixture) |
| `MUXCORE_SANDBOX_ROOTFS_BUSYBOX` | path | Operator Mode B: local static busybox binary for `MakeRootfs` |
| `MUXCORE_SANDBOX_ROOTFS_BUSYBOX_URL` | URL | Operator Mode B: download static busybox into the rootfs |

- **Default (`none`)**: no-op runner — modules inherit loom privileges (Docker/K8s remain the deployment sandbox).
- **`gvisor` / `firecracker`**: spawn path calls `sandbox.Runner.Wrap` before `exec`. If the selected binary is missing, spawn **fails closed** (no silent unsandboxed fallback). Manager construction uses `sandbox.FromEnv()`.
- **Minimal rootfs**: `core/internal/sandbox/rootfs.MakeRootfs` / `MakeDistroRootfs` builds a BusyBox-oriented layout with optional `alpine-minimal` profile (`MUXCORE_SANDBOX_ROOTFS_PROFILE`). Fixture/CI mode embeds a stub busybox script (offline). Operator mode copies a real static busybox. Script: `core/scripts/make-sandbox-rootfs.sh`.
- **OCI**: `sandbox/oci.WriteBundle` writes `config.json` + module binary; with `ROOTFS_AUTO` / `ROOTFS_TEMPLATE` it also applies the BusyBox/distro layout. Operators may validate with `runsc spec validate --bundle <dir>`.
- **Firecracker**: helper `firecracker-spawn` writes a microVM `config.json` (boot-source, rootfs drive, **vsock** UDS). `MUXCORE_SANDBOX_FC_DRY_RUN=1` validates config without launching. Full guest boot remains operator-owned when Firecracker binary is present. Core does not ship runsc or Firecracker binaries.

### Marketplace artifact trust

| Env | Behavior |
| --- | -------- |
| (default) | DeployTag verifies SHA-256 checksum pins when present |
| `MUXCORE_SPOOL_REQUIRE_SIGNATURE=1` | Require ed25519 detached signature (tag `signature` field or sidecar `.sig` / `.minisig`) |
| `MUXCORE_SPOOL_PUBLIC_KEY` | Path to a single ed25519 public key (raw/hex/base64/PEM or minisign `.pub` shape) |
| `MUXCORE_SPOOL_TRUSTED_KEYS_DIR` | Directory of trusted public key files (tried until one verifies) |
| `MUXCORE_SPOOL_ALLOWED_PUBLISHERS` | Comma-separated publisher allowlist; when set, tag `publisher` must match |

Signature verification is wired on the DeployTag / boot spawn path. Minisign sidecars support both legacy (`Ed`, raw bytes) and hashed (`ED`, Blake2b-512 prehash) modes, including trusted-comment global signature checks.

### Multi-tenant scaffolding (optional)

| Env | Behavior |
| --- | -------- |
| `TENANT_MODE=1` | Request middleware injects `tenant_id`; **per-tenant storage partitions** under `data/tenants/{id}/` for request-media SQLite and userdata-local files (`X-Tenant-ID` / claims; missing → `default`). Default off = single household. |
| `MUXCORE_TENANT_CLUSTER_MAP` | JSON `{"tenant-a":"host:9090"}` — maps tenant_id → remote muxcored gRPC endpoint(s) (comma-separated failover list allowed) |
| `MUXCORE_TENANT_REGION_MAP` | JSON `{"tenant-a":"us-east"}` — preferred region; placement picks mesh nodes by `region` label before cluster map fallback |
| `MUXCORE_NODE_REGION` | string | This node's region label (e.g. `us-east`) |
| `MUXCORE_TENANT_STORAGE_SYNC` | JSON per-tenant mirror policy — `file://` targets (fixture) or `s3://` peers (production storage-s3) |
| `MUXCORE_TENANT_CLUSTER_STRICT=1` | Fail closed when a mapped remote is unreachable (TCP probe) |
| `MUXCORE_TENANT_ID` | Process-scoped tenant for module dials when request context has no tenant_id |

Household isolation on one host is implemented (partitions, auth-local `tenant_id`, BFF headers, cross-tenant deny without `admin`). Multi-cluster routing: `core/pkg/tenant` `Router` / `ResolveDial`; SDK `client.Dial` rewrites addresses when the map is set. **Multi-region placement** (`Placer`, `MUXCORE_TENANT_REGION_MAP`) and **cross-cluster storage sync** (`MirrorTenant`, `MUXCORE_TENANT_STORAGE_SYNC`) ship for operator-driven multi-region deploys. Admin Cluster page documents env knobs.

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
