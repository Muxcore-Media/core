# MuxCore gRPC Service Reference

This document describes all gRPC services defined by MuxCore. Sidecar modules
implement or consume these services to communicate with core and with each
other.

Source: `proto/muxcore/*/v1/*.proto`

---

## 1. DiscoveryService

**Package:** `muxcore.discovery.v1`
**File:** `proto/muxcore/discovery/v1/discovery.proto`

Handles cluster membership, service discovery, and module registry queries.
Each node runs a DiscoveryServer; modules query it to find peer modules and
services across the cluster.

| RPC | Request | Response | Description |
|-----|---------|----------|-------------|
| `Join` | `JoinRequest` | `JoinResponse` | Request cluster membership. The first node to join becomes leader. |
| `Leave` | `LeaveRequest` | `LeaveResponse` | Gracefully remove a node from the cluster. Triggers re-election if the leaving node was leader. |
| `Heartbeat` | `HeartbeatRequest` | `HeartbeatResponse` | Signal that a node is still alive. Carries module list and health for cross-node routing. |
| `Members` | `MembersRequest` | `MembersResponse` | Returns the current cluster member list with module assignments. |
| `Watch` | `MembersRequest` | `stream ClusterEvent` | Streams cluster membership changes (Join, Leave, LeaderChanged). |
| `FindByCapability` | `FindByCapabilityRequest` | `FindByCapabilityResponse` | Returns all registered modules advertising a given capability string. Fans out to peer nodes for cluster-wide results. |
| `FindByRole` | `FindByRoleRequest` | `FindByRoleResponse` | Returns all registered modules with a given role string. |
| `Resolve` | `ResolveRequest` | `ResolveResponse` | Returns a single module entry by ID. |

**Key behaviors:**
- Leader election is deterministic: lowest node ID wins within each term.
- Terms are monotonically increasing counters propagated via gRPC metadata.
- Nodes are evicted after 30s of missed heartbeats (configurable).
- `FindByCapability`/`FindByRole` fan out to all known peers and merge results.

---

## 2. AuthService

**Package:** `muxcore.auth.v1`
**File:** `proto/muxcore/auth/v1/auth.proto`

Implemented by sidecar auth modules (e.g. `auth-local`). Core delegates all
authentication, authorization, and identity extraction to this service.

### Core Auth

| RPC | Request | Response | Description |
|-----|---------|----------|-------------|
| `Authenticate` | `AuthenticateRequest` | `AuthenticateResponse` | Authenticate with credentials (password, TOTP, passkey, API key). Returns session token or partial token for 2FA step-up. |
| `Validate` | `ValidateRequest` | `ValidateResponse` | Validate a session token and return user identity and roles. |
| `Revoke` | `RevokeRequest` | `RevokeResponse` | Revoke a session or token. |
| `Can` | `CanRequest` | `CanResponse` | Check if a user has permission for an action on a resource (RBAC). |
| `ExtractIdentity` | `ExtractIdentityRequest` | `ExtractIdentityResponse` | Extract caller identity from a token or gRPC metadata. |

### TOTP Management

| RPC | Request | Response | Description |
|-----|---------|----------|-------------|
| `EnableTOTP` | `EnableTOTPRequest` | `EnableTOTPResponse` | Generate TOTP secret and QR code URL for a user. |
| `DisableTOTP` | `DisableTOTPRequest` | `DisableTOTPResponse` | Disable TOTP for a user. |
| `TOTPStatus` | `TOTPStatusRequest` | `TOTPStatusResponse` | Check whether TOTP is enabled for a user. |
| `VerifyTOTPSetup` | `VerifyTOTPSetupRequest` | `VerifyTOTPSetupResponse` | Verify a TOTP code during setup to confirm the user scanned the QR code. |

### User Management (admin only)

| RPC | Request | Response | Description |
|-----|---------|----------|-------------|
| `CreateUser` | `CreateUserRequest` | `CreateUserResponse` | Create a new user account. |
| `DeleteUser` | `DeleteUserRequest` | `DeleteUserResponse` | Erase a user account (ADR-0035 §1): current admin bearer in `x-auth-token` required (no mesh-peer bypass), target in the caller's tenant, never self or the last admin. Returns the tombstone's `erasure_id`; repeating it for a tombstoned id returns the same `erasure_id`. |
| `ListUsers` | `ListUsersRequest` | `ListUsersResponse` | List all users. |
| `SetPassword` | `SetPasswordRequest` | `SetPasswordResponse` | Set or reset a user's password. |
| `SetRoles` | `SetRolesRequest` | `SetRolesResponse` | Update a user's role assignments. |

### API Token Management

| RPC | Request | Response | Description |
|-----|---------|----------|-------------|
| `CreateAPIToken` | `CreateAPITokenRequest` | `CreateAPITokenResponse` | Create a scoped API token for automation. |
| `ListAPITokens` | `ListAPITokensRequest` | `ListAPITokensResponse` | List all API tokens for a user. |
| `DeleteAPIToken` | `DeleteAPITokenRequest` | `DeleteAPITokenResponse` | Revoke an API token. |

### User Erasure Ledger (ADR-0035, NFR-DATA-003)

The provider's ledger of erasure tombstones is the only authority for erasing a
deleted user's data in other modules; it is never carried on the event bus.
Every personal-data owner reconciles against it with the SDK helper
`sdk/go/module/erasure`. Providers without a ledger (e.g. auth-oidc) return
`Unimplemented` for all three RPCs, which consumers report as `unsupported`.

| RPC | Request | Response | Description |
|-----|---------|----------|-------------|
| `ListUserErasures` | `ListUserErasuresRequest` | `ListUserErasuresResponse` | Page through every tombstone (`erasure_id`, `user_id`, `tenant_id`, `deleted_at`, `acknowledged_by_caller`). Verified mesh client certificate whose CN is in `AUTH_ERASURE_CONSUMERS` only; a user bearer is rejected. |
| `AckUserErasure` | `AckUserErasureRequest` | `AckUserErasureResponse` | Record the caller's outcome (`OK`, `FAILED`, `UNSUPPORTED`), a short PII-free `detail_code` and per-store `counts`. Same caller rule; the acknowledging module is the verified CN, never a request field. Idempotent. |
| `GetUserErasureStatus` | `GetUserErasureStatusRequest` | `GetUserErasureStatusResponse` | Per-module completion for one erasure or all/pending ones; complete when every `AUTH_ERASURE_REQUIRED` module acknowledged `OK`. Admin bearer required (ADR-0026). |

---

## 3. PolicyService

**Package:** `muxcore.policy.v1`
**File:** `proto/muxcore/policy/v1/policy.proto`

Implemented by sidecar policy modules (`call-policy-default`, `publish-policy-default`).
Core queries these before dispatching inter-module calls or event publishes.

| RPC | Request | Response | Description |
|-----|---------|----------|-------------|
| `AllowCall` | `AllowCallRequest` | `AllowCallResponse` | Check whether caller module may invoke a method on target module. Called by mesh client before every inter-module dispatch. |
| `AllowPublish` | `AllowPublishRequest` | `AllowPublishResponse` | Check whether caller module may publish events of a given type. Called by event bus before every publish. |

**Key behaviors:**
- Deny-by-default: all calls/publishes are denied when no policy module is registered.
- Policies are hot-reloadable via SIGHUP.
- Supports `--allow-all` for development mode.

---

## 4. ModuleRegistration

**Package:** `muxcore.module.v1`
**File:** `proto/muxcore/module/v1/registration.proto`

Called by sidecar module binaries at startup and shutdown to register with core.

| RPC | Request | Response | Description |
|-----|---------|----------|-------------|
| `Register` | `RegisterRequest` | `RegisterResponse` | Register a module with core. Provides module identity, capabilities, and endpoint addresses. |
| `Unregister` | `UnregisterRequest` | `UnregisterResponse` | Gracefully unregister a module at shutdown. |

---

## 5. StorageService

**Package:** `muxcore.storage.v1`
**File:** `proto/muxcore/storage/v1/storage.proto`

Provides storage access to modules. Modules call this to store/retrieve files,
query metadata, and negotiate backend capabilities.

| RPC | Request | Response | Description |
|-----|---------|----------|-------------|
| `Put` | `stream PutRequest` | `PutResponse` | Store an object. First message carries key + total size; subsequent messages carry chunks. |
| `Get` | `GetRequest` | `stream GetResponse` | Retrieve an object as a chunk stream. Supports optional byte range via offset/length. |
| `Delete` | `DeleteRequest` | `DeleteResponse` | Remove an object from storage. |
| `Stat` | `StatRequest` | `StatResponse` | Return object metadata (size, type, modification time). |
| `List` | `ListRequest` | `ListResponse` | List objects matching a key prefix. |
| `Capabilities` | `CapabilitiesRequest` | `CapabilitiesResponse` | Return supported optional features (hardlink, stream, atomic move, etc.). |

---

## 6. HealthService

**Package:** `muxcore.health.v1`
**File:** `proto/muxcore/health/v1/health.proto`

Standard gRPC health checking for Kubernetes and monitoring tool compatibility.

| RPC | Request | Response | Description |
|-----|---------|----------|-------------|
| `Check` | `HealthCheckRequest` | `HealthCheckResponse` | Return health status of a node or specific module. |
| `Watch` | `HealthWatchRequest` | `stream HealthCheckResponse` | Stream health status changes for monitored modules. |

---

## 7. EventService

**Package:** `muxcore.events.v1`
**File:** `proto/muxcore/events/v1/events.proto`

Distributed publish/subscribe over gRPC. Nodes use this to relay events
across the cluster.

| RPC | Request | Response | Description |
|-----|---------|----------|-------------|
| `Publish` | `PublishRequest` | `PublishResponse` | Send an event to all subscribers on the target node. |
| `Subscribe` | `SubscribeRequest` | `stream Event` | Stream events matching the given types from the remote node. |
| `Request` | `RequestEvent` | `Event` | Send a request event and wait for a single reply (request/reply pattern). |
| `Replay` | `ReplayRequest` | `stream Event` | Stream historical events from the WAL starting at a given sequence number. Requires WAL to be configured. |

---

## 8. ModuleMesh

**Package:** `muxcore.mesh.v1`
**File:** `proto/muxcore/mesh/v1/module.proto`

The inter-module communication mesh. Modules expose this to receive calls from
other modules across nodes.

| RPC | Request | Response | Description |
|-----|---------|----------|-------------|
| `Call` | `CallRequest` | `CallResponse` | Invoke a method on a remote module. Routed through discovery + load balancer. |
| `StreamCall` | `stream CallRequest` | `stream CallResponse` | Bidirectional streaming for long-running interactions. |

**Key behaviors:**
- Calls are gated by `PolicyService.AllowCall` (deny-by-default).
- Load balancing: round-robin, random, or least-loaded strategy.
- Circuit breaker: 5 consecutive failures opens circuit for 30s cooldown.
- Health-aware: unhealthy modules are excluded from routing candidates.

---

## Service Summary

| Service | File | RPCs | Implemented By | Consumed By |
|---------|------|------|----------------|-------------|
| DiscoveryService | `discovery/v1/discovery.proto` | 8 | Core | All nodes, modules |
| AuthService | `auth/v1/auth.proto` | 30 | `auth-local` module | Core (HTTP/gRPC middleware) |
| PolicyService | `policy/v1/policy.proto` | 2 | `call-policy-default`, `publish-policy-default` | Core (mesh client, event bus) |
| ModuleRegistration | `module/v1/registration.proto` | 2 | Core | Sidecar modules |
| StorageService | `storage/v1/storage.proto` | 6 | Core | Sidecar modules |
| HealthService | `health/v1/health.proto` | 2 | Core | K8s, monitoring |
| EventService | `events/v1/events.proto` | 4 | Core | Cross-node event relay |
| ModuleMesh | `mesh/v1/module.proto` | 2 | Sidecar modules | Sidecar modules |

**Total: 8 services, 43 RPC methods.**
