# MuxCore Audit System

## Overview

MuxCore's audit system records security-relevant and operational events throughout the infrastructure. Every audit entry flows through an optional `AuditLogger` implementation (registered as a module) that persists and exposes the trail. If no `AuditLogger` module is loaded, audit calls are silently dropped — the system operates without an audit trail but incurs no performance penalty.

## Architecture

```
┌──────────────────────────────────────────────────┐
│                   bootstrap                       │
│  nodeID = "muxcore-" + gRPC addr                 │
│  bus.SetNodeID / SetAuditLogger                  │
│  meshSrv.SetNodeID / SetAuditLogger              │
│  meshClient.SetNodeID / SetAuditLogger           │
│  srv.SetNodeID / SetAuditLogger                  │
│  mgr.SetAuditLogger                              │
│  store.SetAuditLogger                            │
└──────────────┬───────────────────────────────────┘
               │
    ┌──────────┼──────────────────────────┐
    ▼          ▼                          ▼
┌───────┐  ┌───────┐                ┌──────────┐
│ API   │  │ Mesh  │                │ Event Bus │
│Server │  │Server │                │(MemoryBus)│
│       │  │       │                │           │
│ http. │  │ grpc. │                │ event.    │
│request│  │ call  │                │ publish   │
│ auth. │  │stream_│                │ subscribe │
│failure│  │ call  │                │ unsubscribe│
│ authz.│  │ mesh. │                └───────────┘
│denied │  │client_│
└───────┘  │ call  │     ┌──────────┐  ┌──────────┐
           └───────┘     │  Module  │  │ Storage  │
                         │  Manager │  │Orch'ator │
                         │          │  │          │
                         │ module.  │  │ storage. │
                         │ register │  │ put      │
                         │ init     │  │ delete   │
                         │ start    │  │ move     │
                         │ stop     │  └──────────┘
                         │ degraded │
                         └──────────┘
               │                          │
               └──────────┬───────────────┘
                          ▼
                  ┌──────────────┐
                  │ AuditLogger  │
                  │  (module)    │
                  │              │
                  │ Log()        │
                  │ Query()      │
                  │ Export()     │
                  └──────────────┘
```

> **Note on core's built-in FileLogger:** The default `FileLogger` shipped with
> core keeps an in-memory ring buffer of the last 50,000 entries for `Query()`
> and `Export()`. The full history on disk (JSONL file) is not indexed for
> search. For compliance and forensics use cases requiring disk-backed query,
> deploy a module that implements `AuditLogger` with its own query engine
> (e.g., SQLite, PostgreSQL). See "Writing an AuditLogger Module" below.

## Audit Entry Schema

All audit entries use `contracts.AuditEntry`:

| Field | Type | Description |
|---|---|---|
| `ID` | string | Unique entry UUID |
| `Timestamp` | time | When the event occurred |
| `Actor` | string | User ID, module ID, `"system"`, `"anonymous"`, or peer address |
| `Action` | string | Dotted action identifier (see table below) |
| `Resource` | string | What was acted upon (path, module ID, storage key) |
| `ResourceID` | string | UUID of the affected resource, if applicable |
| `Details` | map[string]string | Operation-specific context |
| `TraceID` | string | Distributed trace ID for correlation |
| `NodeID` | string | Which node produced this entry (set at bootstrap) |

## Audit Actions

### HTTP API (`internal/api/middleware.go`)

| Action | Actor | Resource | When |
|---|---|---|---|
| `http.request` | Session UserID or `"anonymous"` | URL path | Every authenticated request completes |
| `auth.failure` | `"anonymous"` | URL path | Authentication fails (bad token, wrong password) |
| `authz.denied` | Session UserID | Required resource | Authorization check denies access |

Details for `http.request`: `method`, `status_code`, `ip`, `duration_ms`, `username`.
Details for `auth.failure`: `method`, `ip`, `error`.
Details for `authz.denied`: `action`, `username`, `path`, `method`, `error`.

### gRPC Mesh (`internal/grpcmesh/server.go`)

| Action | Actor | Resource | When |
|---|---|---|---|
| `grpc.call` | Peer IP address | Target module ID | Inbound unary gRPC call |
| `grpc.stream_call` | Peer IP address | Target module ID | Inbound streaming gRPC call |
| `mesh.client_call` | Local nodeID | Target module ID | Outbound call to remote node |

Details: `method` (for all three), `remote_node` and `remote_addr` (for client_call).

### Event Bus (`internal/events/memory.go`)

| Action | Actor | Resource | When |
|---|---|---|---|
| `event.publish` | Event source | Event type | Event published to bus |
| `event.subscribe` | `"system"` | Event type | Handler subscribes to event type |
| `event.unsubscribe` | `"system"` | Event type | Handler unsubscribes from event type |

Details for `event.publish`: `subscriber_count`.
Details for subscribe/unsubscribe: `handler` (function pointer).

### Module Lifecycle (`internal/module/manager.go`)

| Action | Actor | Resource | When |
|---|---|---|---|
| `module.register` | `"system"` | Module ID | Module registered |
| `module.unregister` | `"system"` | Module ID | Module unregistered |
| `module.init` | `"system"` | Module ID | Module initialization completes |
| `module.start` | `"system"` | Module ID | Module started |
| `module.stop` | `"system"` | Module ID | Module stopped |
| `module.degraded` | `"system"` | Module ID | Health check returns error |

Details for `module.register` and `module.init`: `version`.
Details for `module.degraded`: `error`.

### Storage (`internal/storage/orchestrator.go`)

| Action | Actor | Resource | When |
|---|---|---|---|
| `storage.put` | `"system"` | Storage key | Object written successfully |
| `storage.delete` | `"system"` | Storage key | Object deleted successfully |
| `storage.move` | `"system"` | Destination key | Object moved successfully |

Details for `storage.put`: `key`, `size_bytes`.

> **Note:** Get, List, Exists, and Stat are intentionally not audited at the core level. These are hot paths for a media platform — auditing every read would overwhelm the audit trail. Modules that need read-auditing should implement it themselves.

## Design Principles

1. **Fire-and-forget.** All audit calls use goroutines. No audit path blocks the system. If the audit logger is slow or unavailable, regular operations continue unimpeded.

2. **Nil-safe.** Every audit hook checks `if auditLogger != nil` before calling. When no `AuditLogger` module is loaded, the audit system compiles to a few no-op branches.

3. **Node identity is explicit.** Every infrastructure component receives its node ID at bootstrap. Audit entries from a multi-node cluster are traceable to their origin node.

4. **Defense in depth.** Events published to the bus are audited at the bus level (`event.publish`). Module lifecycle transitions are also audited at the manager level (`module.register`). These serve different purposes: bus-level is transport auditing, manager-level is intent auditing.

## Adding a New Audit Hook

To add audit logging to a new core component:

1. Add `audit contracts.AuditLogger` and `nodeID string` fields to the struct.
2. Add `SetAuditLogger(a contracts.AuditLogger)` and `SetNodeID(id string)` setters.
3. Where the auditable event occurs, read the audit logger under the appropriate lock, then:
   ```go
   if auditLogger != nil {
       go func() {
           entry := contracts.AuditEntry{
               ID:        uuid.New().String(),
               Timestamp: time.Now(),
               Actor:     "...",
               Action:    "your.action",
               Resource:  "...",
               Details:   map[string]string{"key": "value"},
               TraceID:   traceID,
               NodeID:    nodeID,
           }
           _ = auditLogger.Log(ctx, entry)
       }()
   }
   ```
4. Wire in `main.go` alongside existing `SetAuditLogger` / `SetNodeID` calls.

## Building an AuditLogger Module

### Contract

The `AuditLogger` contract is defined in `pkg/contracts/audit.go`:

```go
type AuditEntry struct {
    ID         string
    Timestamp  time.Time
    Actor      string            // user ID, "system", or module ID
    Action     string            // e.g., "http.request", "module.registered", "media.requested"
    Resource   string            // e.g., "/api/movies", "admin-ui", "Inception"
    ResourceID string            // UUID of the affected resource, if applicable
    Details    map[string]string // arbitrary context
    TraceID    string
    NodeID     string
}

type AuditFilter struct {
    Actor    string
    Action   string
    Resource string
    From     time.Time
    To       time.Time
    TraceID  string
}

type AuditLogger interface {
    Log(ctx context.Context, entry AuditEntry) error
    Query(ctx context.Context, filter AuditFilter) ([]AuditEntry, error)
    Export(ctx context.Context, format string) (io.ReadCloser, error)
}
```

### Module Declaration

An AuditLogger module must register itself so core discovers it at bootstrap. The module must satisfy both `contracts.Module` and `contracts.AuditLogger`:

```go
package auditsqlite

import (
    "github.com/Muxcore-Media/core/pkg/contracts"
)

type Module struct { /* ... */ }

func (m *Module) Info() contracts.ModuleInfo {
    return contracts.ModuleInfo{
        ID:      "audit-sqlite",
        Name:    "Audit (SQLite)",
        Version: "1.0.0",
        Roles:   []string{"audit"},
        Capabilities: []string{"audit.local"},
        Contracts: []contracts.ContractDeclaration{
            {
                Repo:      "github.com/Muxcore-Media/core",
                Version:   "v0.1.0",
                Interface: "AuditLogger",
            },
        },
    }
}
```

Key points for `ModuleInfo`:
- **`Roles`**: Must include `"audit"`. Core uses `reg.FindByRole("audit")` — not type assertion — to determine whether to enable the audit logger banner log line. However, discovery of the `AuditLogger` interface itself uses a type assertion (`mod.(contracts.AuditLogger)`), so the module must implement every method of the interface.
- **`Capabilities`**: `"audit.local"` is the convention for a local (single-node) audit backend. Future clustered backends could use `"audit.distributed"`.
- **`Contracts`**: Must declare `AuditLogger` with the core repo path and version. The marketplace compatibility checker uses this to verify that modules claiming to provide audit logging actually implement the correct interface version.

### Discovery and Wiring

At bootstrap, `main.go` iterates all loaded modules and checks each one with a type assertion:

```go
for _, mod := range modules {
    if a, ok := mod.(contracts.AuditLogger); ok {
        al = a
    }
}
```

If found, the audit logger is wired into every infrastructure component:
- `bus.SetAuditLogger(al)` — event bus hooks
- `meshSrv.SetAuditLogger(al)` / `meshClient.SetAuditLogger(al)` — gRPC call auditing
- `srv.SetAuditLogger(al)` — HTTP request auditing
- `mgr.SetAuditLogger(al)` — module lifecycle auditing
- `store.SetAuditLogger(al)` — storage operation auditing

Node identity is wired separately via `SetNodeID()` calls on each component (see the Architecture diagram above). The module itself can receive the node ID via `InfrastructureAware`:

```go
func (m *Module) SetInfrastructure(cluster contracts.Cluster, wp contracts.WorkerPool, audit contracts.AuditLogger) {
    // A module that IS the audit logger doesn't need the audit logger parameter,
    // but it may want the cluster reference for NodeID population:
    if cluster != nil {
        m.nodeID = cluster.LocalNode().ID
    }
}
```

Alternatively, the module can read its NodeID from the `HERMES_NODE_ID` environment variable or construct it from the gRPC listen address. Core passes no node ID to the module directly — the module should self-identify.

### Module Lifecycle

The module must implement the standard `contracts.Module` interface:

- **`Init(ctx)`**: Create/open the database. Run migrations. Create indexes on `(timestamp)`, `(actor, timestamp)`, `(action, timestamp)`, `(resource)`, `(trace_id)`.
- **`Start(ctx)`**: Begin accepting writes. If using a write buffer, start the flush goroutine.
- **`Stop(ctx)`**: Flush all pending writes. Close the database cleanly. Drain the write buffer.
- **`Health(ctx)`**: Return an error if the database is unreachable or disk space is below a threshold.

### Implementation Guidance

A reference implementation should:
- **Buffer writes.** Don't fsync per entry. Use a ring buffer + periodic flush (e.g., every 2 seconds or 1000 entries, whichever comes first). This keeps audit overhead near zero for bursty workloads.
- **Index by actor, action, resource, trace_id, and timestamp.** The `Query` method must handle all `AuditFilter` fields efficiently.
- **Auto-populate NodeID.** If an `AuditEntry` arrives with an empty `NodeID`, populate it from the module's own node identity. This catches entries from modules that don't set NodeID.
- **Support `"json"` and `"csv"` export formats.** `Export` returns an `io.ReadCloser` — stream the data, don't buffer the entire dataset in memory.
- **Prune old entries.** Configurable retention (default: 30 days). Run a periodic cleanup goroutine from `Start()`.
- **The `Log` method must never return an error that blocks the caller.** If the database is unreachable, drop the entry on the floor and log a warning via `slog`. The audit system is best-effort — it must not become a failure domain for the rest of the platform.

### Audit Log Table Schema (SQLite reference)

```sql
CREATE TABLE IF NOT EXISTS audit_entries (
    id          TEXT PRIMARY KEY,
    timestamp   INTEGER NOT NULL,  -- Unix nano
    actor       TEXT NOT NULL DEFAULT '',
    action      TEXT NOT NULL DEFAULT '',
    resource    TEXT NOT NULL DEFAULT '',
    resource_id TEXT NOT NULL DEFAULT '',
    details     TEXT NOT NULL DEFAULT '{}',  -- JSON object
    trace_id    TEXT NOT NULL DEFAULT '',
    node_id     TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_audit_timestamp   ON audit_entries(timestamp);
CREATE INDEX IF NOT EXISTS idx_audit_actor_ts    ON audit_entries(actor, timestamp);
CREATE INDEX IF NOT EXISTS idx_audit_action_ts   ON audit_entries(action, timestamp);
CREATE INDEX IF NOT EXISTS idx_audit_resource    ON audit_entries(resource);
CREATE INDEX IF NOT EXISTS idx_audit_trace_id    ON audit_entries(trace_id);
CREATE INDEX IF NOT EXISTS idx_audit_node_id     ON audit_entries(node_id);
```
