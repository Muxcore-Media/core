# Operations Guide

Operational reference for running MuxCore in production: startup checks,
health monitoring, version compatibility, connection pooling, and rate limiting.

---

## Startup Invariant Checks

Before any subsystem initialises, core runs a set of invariant checks via
`internal/startup/checks.go`. Fatal failures abort startup with a clear error;
warnings are logged and startup continues.

| Check | Fatal? | What it verifies |
|-------|--------|-----------------|
| `module_cache_writable` | Yes | `~/.muxcore/modules/` exists and is writable |
| `audit_log_dir_writable` | Yes | Parent directory of `audit.path` is writable (when configured) |
| `cosign_pub_readable` | No (warn) | `cosign.pub` exists and is non-empty for spool verification |
| `go_version_match` | No (warn) | Go runtime major.minor matches expected go1.26 |

**Validate before deploying a config change:**

```bash
muxcored --dry-run --tag default
```

`--dry-run` runs startup checks, validates TLS certs, and (if `--tag` is given)
fetches the spool tag to verify connectivity. Exits 0 on success, 1 on failure.

---

## Core Self-Health Probes

Core probes its own subsystems on every `/health` request. Results are cached
for 5 seconds to avoid thundering-herd on monitoring polls.

| Probe | What it checks |
|-------|---------------|
| `core.event_bus` | Event bus is initialised and accepting subscriptions |
| `core.discovery` | Discovery subsystem is reachable |
| `core.storage` | Storage orchestrator is alive |
| `core.audit` | Audit log directory is writable (when configured) |

Failed core probes appear in the `/health` response:

```json
{
  "status": "degraded",
  "modules": {
    "core.audit": "audit log directory /var/log/muxcore not writable",
    "downloader-qbittorrent": "ok"
  }
}
```

---

## Version Compatibility

Core enforces SemVer compatibility when modules register. The check is in
`internal/version/version.go:CheckModule`.

Rules:
- **Empty `MinCoreVersion`** — always accepted (backward compat for old modules)
- **Different major version** — rejected (breaking change boundary)
- **Higher minor version required** — rejected (module needs features this core doesn't have)
- **Dev build (`0.0.0-dev`)** — accepts everything (no enforcement in dev)

When a module is rejected at registration, the error is logged:

```
module "auth-oidc" requires core >= 1.2.0 (running 1.0.0): core minor version too old
```

---

## gRPC Connection Pool

The heartbeat loop and cross-node mesh calls share a connection pool
(`internal/grpcmesh/connpool.go`).

- **Keepalive pings** are sent every 20s to detect dead peers within ~30s.
- **Idle eviction** removes connections unused for 5 minutes.
- **Unhealthy eviction** removes connections in `TransientFailure` or `Shutdown`
  state on the next cleanup tick (1 minute interval).

Without the pool, each heartbeat would dial a new TLS connection — 10s overhead
per peer per heartbeat cycle. The pool eliminates this.

**Configuration:** pool is always on; no config required. The idle timeout and
cleanup interval are constants (`connPoolIdleTimeout = 5m`,
`connPoolCleanupInterval = 1m`).

---

## gRPC Rate Limiting

The built-in gRPC per-IP rate limiter was removed in the module-extraction
refactor. The gRPC port is intended for internal sidecar use only — module
connections are authenticated via TLS and a join token.

**Protect the gRPC port at the network level** (firewall, load balancer ACL)
rather than relying on application-layer rate limiting.

HTTP API rate limiting is still available via a `RateLimiterProvider` module
(see `pkg/contracts/ratelimit.go`). Core discovers a registered provider at
startup and wires it into the HTTP middleware chain.

---

## Module Cache Pruning

Module binaries are cached at `~/.muxcore/modules/<id>/<version>/muxcore-module`.
On startup, `PruneCache(3)` is called automatically, keeping the 3 most recent
versions per module and deleting older ones.

To prune more aggressively or disable pruning, modify the call in `main.go`:

```go
modMgr.PruneCache(1) // keep only 1 version per module
modMgr.PruneCache(0) // remove all cached versions (always build from source)
```

---

## Audit Log

The built-in audit logger writes JSONL to a configured file path with
a SHA-256 hash chain for tamper detection.

- **In-memory buffer** is capped at 50,000 entries. Older entries are only on disk.
- **No rotation** is built in. Use `logrotate` or set `audit.path` to a path
  managed by your log rotation tooling.
- **Disk full** — if disk fills, write errors are logged but don't crash core.
  Monitor disk space externally.
- **Verify chain integrity** — use `AuditLogger.VerifyChainIntegrity()` to
  validate the hash chain has not been tampered with.

---

## Prometheus Metrics

Enable the `/metrics` endpoint with `MUXCORE_METRICS_ENABLE=true`.

| Metric | Type | Description |
|--------|------|-------------|
| `muxcore_event_bus_dropped_events_total` | Counter | Events dropped due to full subscriber channels |
| `muxcore_event_bus_active_subscribers` | Gauge | Active event subscriptions |
| `muxcore_grpc_conn_pool_size` | Gauge | gRPC connections in the pool |
| `muxcore_registry_module_count` | Gauge | Registered modules |
| `muxcore_cluster_leader_term` | Gauge | Current election term |
| `muxcore_cluster_is_leader` | Gauge | 1 if this node is leader, 0 otherwise |

**Alert triggers to consider:**
- `muxcore_event_bus_dropped_events_total` increasing → subscriber too slow
- `muxcore_cluster_is_leader` unstable (flipping) → network instability
- `muxcore_registry_module_count` drops unexpectedly → module crashed

---

## pprof Profiling

Enable Go pprof endpoints with `MUXCORE_DEBUG_ENABLE=true`. These are served
on the HTTP server under `/debug/pprof/` and are protected by the auth middleware.

**Capture a 30-second CPU profile:**

```bash
curl -o cpu.prof https://muxcore.host:8080/debug/pprof/profile?seconds=30
go tool pprof cpu.prof
```

**Capture a goroutine dump:**

```bash
curl https://muxcore.host:8080/debug/pprof/goroutine?debug=1
```

Only enable in trusted environments. pprof exposes internal memory state.
