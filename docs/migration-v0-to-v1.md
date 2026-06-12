# Migration Guide: v0.x → v1.0

This guide covers breaking changes and migration steps for moving from
MuxCore v0.x to v1.0.0-rc.1.

---

## Overview

v1.0 represents the first stable release of MuxCore. The major changes are:

- **Sidecar module architecture** — All modules run as separate processes,
  communicating via gRPC. The in-process `Fabric` pattern is deprecated.
- **Security by default** — TLS required, deny-by-default call/publish policies,
  RBAC enforced on all HTTP/gRPC endpoints.
- **Production operational model** — WAL-based event durability, configurable
  rate limiting, structured audit logging, Prometheus metrics.

---

## Breaking Changes

### 1. TLS Is Required

**Old:** TLS was optional. Insecure mode was the default.

**New:** `muxcored` refuses to start without TLS configuration. Set
`MUXCORE_DEV_TLS_SKIP=true` for development only — never in production.

**Migration:**
```bash
# Production: provide cert and key
export MUXCORE_SERVER_TLS_CERT=/path/to/cert.pem
export MUXCORE_SERVER_TLS_KEY=/path/to/key.pem

# Development only:
export MUXCORE_DEV_TLS_SKIP=true
```

### 2. Sidecar Module Architecture

**Old:** Modules implemented in-process via `contracts.Fabric`.

**New:** Modules register via gRPC `ModuleRegistration.Register()`. The
`Fabric` struct has been removed from core. Modules are discovered by
capability through `DiscoveryService.FindByCapability()`.

**Migration:**
- Update module binaries to the sidecar SDK (`sdk/go/client`).
- Replace `fabric.Register(mod)` with gRPC registration in the module's
  entry point.
- Replace `fabric.Resolve[Contract](ctx)` with
  `DiscoveryService.FindByCapability(ctx, "capability.name")`.
- See the [Writing Modules](https://github.com/Muxcore-Media/core/wiki/Writing-Modules.md)
  wiki page for the current sidecar registration pattern.

### 3. Deny-by-Default Policies

**Old:** Inter-module calls and event publishes were allowed by default.

**New:** Both require a registered policy module:
- `call-policy-default` for inter-module calls
- `publish-policy-default` for event publishes

Without these, all calls and publishes are denied.

**Migration:**
```bash
# Deploy the policy modules alongside muxcored.
# call-policy-default enforces allow/deny rules from policies.yaml:
muxcore policies.yaml
```

See `muxcore.json` for spool-based deployment configuration and
`policies.yaml` in each module repo for policy format.

### 4. RBAC on HTTP API

**Old:** HTTP API had no access control (other than /health and /version).

**New:** All HTTP endpoints (except /health, /version, /metrics) require a
valid session token in the `Authorization: Bearer <token>` header. Permissions
are enforced via RBAC using the `auth-local` module.

**Migration:**
- Deploy `auth-local` alongside `muxcored`.
- Create initial admin user:
  ```bash
  authctl adduser admin
  authctl addrole admin admin
  ```
- Update API clients to obtain and send bearer tokens.

### 5. Event Bus Changes

**Old:** `MemoryBus` was purely in-memory with no persistence.

**New:** `MemoryBus` supports WAL-backed persistence for event durability.
The bus requires a publish policy module. The `Fabric` event methods have
been removed — use `bus.Publish()` and `bus.Subscribe()` directly.

**Migration:**
- Set `MUXCORE_EVENT_JOURNAL_PATH` for WAL-backed persistence (optional).
- Deploy `publish-policy-default` if modules need to publish events.
- Replace `fabric.Publish(event)` with `bus.Publish(ctx, event)`.
- Replace `fabric.Subscribe(type, handler)` with `bus.Subscribe(ctx, type, handler)`.

### 6. Configuration File Format

**Old:** Flat configuration with env-var overrides.

**New:** Structured `muxcore.json` file with env-var overrides. Some fields
have been renamed or moved.

**Key changes:**
```jsonc
// Old (v0.x):
{
  "http_addr": ":8080",
  "grpc_addr": ":9090",
  "log_level": "info"
}

// New (v1.0):
{
  "server": { "addr": ":8080", "read_timeout": 15, "write_timeout": 15 },
  "grpc": { "addr": ":9090", "seed_nodes": ["node1:9090", "node2:9090"] },
  "log": { "level": "info", "format": "text" },
  "audit": { "path": "/var/log/muxcore/audit.jsonl", "max_size_mb": 100, "max_rotated": 5 }
}
```

The configuration file supports hot-reload on SIGHUP for safe changes
(log level, log format, audit path, seed nodes).

### 7. gRPC Service Changes

**Old:** Some gRPC services had auth bypass env vars.

**New:** All gRPC services require authentication by default. The bypass
env vars `MUXCORE_GRPC_REQUIRE_EVENTS_AUTH` and
`MUXCORE_GRPC_REQUIRE_DISCOVERY_AUTH` have been removed.

**Migration:**
- Ensure all gRPC clients send `x-caller-id` metadata.

### 8. Storage Contracts

**Old:** Storage providers were registered in-process.

**New:** Storage providers are discovered through the registry by capability.
Core's `StorageOrchestrator` auto-discovers providers registered with the
`storage.local` capability.

**Migration:**
- Storage modules should register with capability `storage.local`.
- Core discovers them automatically via `reg.FindByCapability("storage.local")`.

### 9. Monitoring & Metrics

**Old:** No built-in metrics endpoint.

**New:** Prometheus-format metrics at `GET /metrics` when
`MUXCORE_METRICS_ENABLE=true` is set.

**Migration:**
```bash
export MUXCORE_METRICS_ENABLE=true
# Metrics available at http://<addr>/metrics
```

### 10. Audit Logging

**Old:** No structured audit logging.

**New:** JSONL audit log with SHA-256 hash chain for tamper detection.
Configured via the `audit` section in `muxcore.json`.

**Migration:**
```json
{
  "audit": {
    "path": "/var/log/muxcore/audit.jsonl",
    "max_size_mb": 100,
    "max_rotated": 5
  }
}
```

---

## Required Sidecar Modules

The following modules must be deployed for a fully functional v1.0 cluster:

| Module | Purpose | Required? |
|--------|---------|-----------|
| `auth-local` | Authentication, RBAC, API tokens | Yes (no HTTP/gRPC access without it) |
| `call-policy-default` | Inter-module call policy | Yes (all calls denied without it) |
| `publish-policy-default` | Event publish policy | Yes (all publishes denied without it) |
| `scheduler-cron` | Cron-based task scheduling | No (manual task submission works) |
| `worker-pool-memory` | Distributed task execution | No (core has built-in worker pool) |

---

## Step-by-Step Migration

1. **Install prerequisites:**
   ```bash
   go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
   go install honnef.co/go/tools/cmd/staticcheck@latest
   ```

2. **Build v1.0 binary:**
   ```bash
   cd core
   make build VERSION=1.0.0-rc.1
   ```

3. **Deploy sidecar modules:**
   Each module repo has its own Makefile. Build and deploy alongside `muxcored`:
   ```bash
   cd auth-local && make build
   cd call-policy-default && make build
   cd publish-policy-default && make build
   ```

4. **Create configuration:**
   ```bash
   cp docs/examples/muxcore.json /etc/muxcore/muxcore.json
   # Edit to match your environment
   ```

5. **Create admin user:**
   ```bash
   authctl adduser admin
   authctl addrole admin admin
   ```

6. **Start the cluster:**
   ```bash
   muxcored --config /etc/muxcore/muxcore.json
   ```

7. **Verify:**
   ```bash
   curl http://localhost:8080/health
   curl http://localhost:8080/version
   ```

---

## Rollback

To roll back to v0.x:
1. Stop `muxcored` and all sidecar modules.
2. Replace the binary with the v0.x version.
3. Restore the old configuration file format.
4. Start `muxcored` without sidecar modules.

Note: The WAL event format changed between v0.x and v1.0. If you were using
WAL in v0.x, the existing journal files will not be readable by v1.0 and
vice versa.

---

## See Also

- [CHANGELOG.md](../CHANGELOG.md) — Full release history
- [docs/operations.md](operations.md) — Operational guide
- [docs/grpc-services.md](grpc-services.md) — gRPC service reference
- [docs/openapi.yaml](openapi.yaml) — HTTP API specification
- GitHub Wiki: [Writing Modules](https://github.com/Muxcore-Media/core/wiki/Writing-Modules.md)
