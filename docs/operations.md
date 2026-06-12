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

## Backup & Recovery

### Audit Log Backup

The audit log is a JSONL file with a SHA-256 hash chain for tamper detection.
Each entry carries `prev_entry_hash` linking it to the previous entry, forming
an immutable chain.

**Backup strategy:**

- **Ship logs off-box** — use a log shipper (Filebeat, Fluent Bit, Vector) to
  forward audit JSONL to a central logging system. Core appends to a single
  file; the shipper tails it.
- **Rotate with external tooling** — core has optional built-in rotation
  (`MaxSizeMB`, `MaxRotatedFiles`), but for production prefer `logrotate`:
  ```
  /var/log/muxcore/audit.jsonl {
    daily
    rotate 90
    compress
    delaycompress
    postrotate
      systemctl reload muxcored 2>/dev/null || true
    endscript
  }
  ```
- **Verify integrity** — use `AuditLogger.VerifyChainIntegrity()` to validate
  the hash chain against tampering. Run this periodically on backups.
- **Retention** — determine retention by compliance requirements. Each JSONL
  entry is ~200-500 bytes. A busy server generates ~1 GB/month.

### WAL Backup and Replay

The event bus WAL (`MUXCORE_EVENT_JOURNAL_PATH`) persists every published event
to disk before dispatching to subscribers. WAL segments are named
`events-{seq}.wal` and are stored in the configured journal directory.

**Backup strategy:**

- The WAL is **ephemeral by design** — once all subscribers have acknowledged
  an event, the segment is pruned. Long-term event storage is the
  responsibility of subscriber modules.
- To preserve WAL data for replay, **back up the journal directory** before
  restarting core. Segments are only pruned when all subscribers have advanced
  past them (see `SetMinSubscriberSeq`).
- **Replay procedure** — on startup, `EnableWAL()` replays events from the
  last committed sequence to catch subscribers up. No manual intervention needed.
- **Disaster recovery** — if the WAL directory is lost, core starts with an
  empty event bus. Modules that missed events must re-fetch state from their
  own data stores.

### Module Cache

Module binaries are cached at `~/.muxcore/modules/<id>/<version>/muxcore-module`.

- **Rebuildable from spool** — the cache is a pure derived artifact. If
  deleted, modules are rebuilt from source on next spawn. No data loss.
- **Pruning** — `PruneCache(3)` keeps the 3 most recent versions per module.
- **No backup needed** — the cache is not a backup concern. Focus backup
  efforts on audit logs and subscriber data stores.

---

## Prometheus Metrics

Enable the `/metrics` endpoint with `MUXCORE_METRICS_ENABLE=true`.

| Metric | Type | Description |
|--------|------|-------------|
| `muxcore_event_bus_publishes_total` | Counter | Total events published on the event bus |
| `muxcore_event_bus_dropped_events_total` | Counter | Events dropped due to full subscriber channels |
| `muxcore_event_bus_active_subscribers` | Gauge | Active event subscriptions |
| `muxcore_grpc_conn_pool_size` | Gauge | gRPC connections in the pool |
| `muxcore_registry_module_count` | Gauge | Registered modules |
| `muxcore_cluster_leader_term` | Gauge | Current election term |
| `muxcore_cluster_is_leader` | Gauge | 1 if this node is leader, 0 otherwise |
| `muxcore_storage_provider_count` | Gauge | Registered storage providers |
| `muxcore_module_degraded_count` | Gauge | Modules in degraded state |
| `muxcore_api_auth_failures_total` | Counter | Total authentication failures across all IPs |
| `muxcore_api_auth_backoffs_active` | Gauge | IPs currently in auth backoff |
| `muxcore_grpc_mesh_calls_total` | Counter | Total gRPC mesh calls made through the client |
| `muxcore_api_requests_total` | Counter | Total HTTP requests processed by the API server |
| `muxcore_api_requests_2xx_total` | Counter | HTTP 2xx responses |
| `muxcore_api_requests_4xx_total` | Counter | HTTP 4xx responses |
| `muxcore_api_requests_5xx_total` | Counter | HTTP 5xx responses |
| `muxcore_storage_put_operations_total` | Counter | Storage Put operations |
| `muxcore_storage_get_operations_total` | Counter | Storage Get operations |
| `muxcore_storage_delete_operations_total` | Counter | Storage Delete operations |
| `muxcore_module_spawns_total` | Counter | Module spawns |
| `muxcore_module_restarts_total` | Counter | Module restarts after crash |
| `muxcore_module_resolves_total` | Counter | Module resolves (cache + build) |
| `muxcore_goroutine_count` | Gauge | Current Go runtime goroutines |
| `muxcore_memory_alloc_bytes` | Gauge | Current heap memory allocation in bytes |

**Alert triggers to consider:**
- `muxcore_event_bus_dropped_events_total` increasing → subscriber too slow
- `muxcore_cluster_is_leader` unstable (flipping) → network instability
- `muxcore_registry_module_count` drops unexpectedly → module crashed
- `muxcore_module_degraded_count` > 0 → module in degraded state
- `muxcore_api_auth_backoffs_active` > threshold → possible brute-force attack
- `muxcore_memory_alloc_bytes` approaching limit → memory leak or scale pressure

---

---

## Performance & Resource Requirements

### Baseline Resource Usage

A bare MuxCore instance (no modules, idle) consumes approximately:

| Resource | Usage |
|----------|-------|
| **Memory** | ~25 MB RSS (idle), ~40 MB with WAL enabled |
| **CPU** | < 0.1 core idle; ~0.5 core under 1,000 req/s |
| **Disk** | ~10 MB for binary; WAL grows with event throughput |
| **File descriptors** | ~20 (idle), ~50 with gRPC peers connected |

### Benchmarks

Built-in benchmarks are available for key subsystems:

```bash
# Run all benchmarks
go test -bench=. -benchtime=1x ./internal/events/... ./internal/storage/... ./internal/grpcmesh/... ./internal/api/...
```

Typical results (Go 1.26, 4-core AMD64):

| Benchmark | Ops/s | Notes |
|-----------|-------|-------|
| Event bus publish (no subscribers) | ~5,000,000 | Pure channel send |
| Event bus publish (10 subscribers) | ~500,000 | Fan-out overhead |
| gRPC mesh call (64B payload) | ~200,000 | In-process routing |
| gRPC mesh call (1MB payload) | ~5,000 | Bandwidth-bound |
| WAL write (64B payload) | ~100,000 | With fsync |
| WAL write (batched, 100 writes) | ~2,000,000 | Batch amortizes fsync |
| API health endpoint | ~50,000 | No auth middleware |
| API authenticated request | ~25,000 | Full middleware chain |

### Scaling Guidelines

- **gRPC mesh** is the primary bottleneck for inter-module communication.
  Each `Call()` involves a capability policy check. For high-throughput
  workloads, prefer event bus publish/subscribe (which has no per-call
  policy check).
- **Storage throughput** depends on the provider backend. The local
  filesystem provider saturates disk I/O before CPU.
- **WAL throughput** is limited by disk fsync. Increase batch sizes or
  set `SyncFlushInterval` to trade durability for throughput.
- **Module spawn latency** is dominated by Go build time from source
  (~10-30s). The module cache eliminates this for subsequent spawns.
- **Cross-node routing** adds ~1ms of gRPC dial and serialization
  overhead per call. For latency-sensitive workloads, co-locate modules
  on the same node.

### Load Testing

Quick smoke test with built-in tools:

```bash
# HTTP health endpoint
for i in $(seq 1 100); do
  curl -so /dev/null -w "%{http_code}\n" http://localhost:8080/health
done | sort | uniq -c

# gRPC health check (requires grpcurl)
grpcurl -plaintext localhost:9090 grpc.health.v1.Health/Check
```

For production load testing, use dedicated tools:

- **HTTP**: [vegeta](https://github.com/tsenart/vegeta) — `echo "GET http://localhost:8080/health" | vegeta attack -rate=1000 -duration=30s | vegeta report`
- **gRPC**: [ghz](https://ghz.sh/) — `ghz --insecure --proto proto/muxcore/health/v1/health.proto --call grpc.health.v1.Health/Check localhost:9090`

## Disaster Recovery Runbook

### Scenario: Node Failure

1. **Detect** — Monitoring alerts on `muxcore_cluster_is_leader` dropping to 0
   or `muxcore_registry_module_count` decreasing.
2. **Verify** — Check `/health` endpoint on remaining nodes. Degraded status
   is expected while modules re-register.
3. **Replace** — Provision a replacement node, start `muxcored` with the same
   `--tag` and config. The new node joins the cluster via seed nodes.
4. **Recover** — Modules rebuild from spool cache (~10–30s per module).
   Storage providers re-discover their backends.

### Scenario: Config File Corruption

1. **Detect** — SIGHUP reload logs `config reload failed` at error level.
2. **Verify** — Check `/health` — core continues running with the last valid
   config. No loss of service.
3. **Fix** — Restore config file from backup or version control.
4. **Reload** — Send `SIGHUP` to the process. Core applies the fixed config
   and logs success.

### Scenario: Disk Full

1. **Detect** — OS disk monitoring alerts. Audit log writes fail silently
   (logged at error level, core continues).
2. **Mitigate** — Free disk space (remove old rotated audit files, rotate
   WAL segments, prune module cache).
3. **Verify** — Audit writes resume automatically once space is available.
   No restart needed.

### Scenario: Audit Log Tampering

1. **Detect** — Periodic `VerifyChainIntegrity()` check returns hash mismatch.
2. **Investigate** — Identify which entry was tampered using the hash chain.
   Each entry's `prev_entry_hash` links to the previous, making selective
   tampering detectable.
3. **Recover** — Restore audit log from off-box backup. Replay events from
   WAL if available to fill gaps between backup and incident.

### Scenario: WAL Loss

1. **Impact** — Events published between the last backup and the crash are
   lost. Subscribers that missed events must re-fetch state from their data
   stores.
2. **Recover** — Start core without WAL. Event bus resumes with empty state.
   Subscriber modules that need historical data should implement their own
   replay mechanism from their data stores.

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

---

## Module Deployment Requirements

### Infrastructure Modules (Required)

Before any functional module can communicate, these infrastructure modules must
be deployed:

```bash
# 1. Call policy — without this, all inter-module gRPC calls are denied
muxcored --tag default  # includes call-policy-default

# 2. Publish policy — without this, all event publishes are denied
# (also included in the default tag)

# 3. Auth — without this, all HTTP and gRPC requests are rejected
# (also included in the default tag)
```

### Health Endpoints

All infrastructure modules expose a `/health` HTTP endpoint returning
`{"status":"ok"}`:

| Module | Default Health Port |
|--------|-------------------|
| `call-policy-default` | `:9102` |
| `publish-policy-default` | `:9103` |
| `scheduler-cron` | Same as HTTP API (no separate health port) |
| `worker-pool-memory` | Same as HTTP API (no separate health port) |
| `auth-local` | Same as WebAuthn HTTP port (no separate health port) |

### Module Restart

When core restarts, it re-spawns all modules listed in the spool tag. Modules
that were registered manually must be re-started externally. If a module
registers before its policy dependencies are available (e.g., auth-local before
call-policy-default), core runs standalone until the dependencies arrive.
