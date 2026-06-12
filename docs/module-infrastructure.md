# Module Infrastructure Contracts

Sidecar modules can implement infrastructure contracts — not just domain logic.
Core discovers these modules by capability string and connects to them via gRPC
to enforce policies, authenticate callers, and authorize actions.

## Protocol

Infrastructure modules serve a gRPC service that core queries. The service is
hosted on a port specified by the module's `HttpAddr` field during registration.

```
Sidecar Module                  Core
    │                            │
    │── Register(ModuleInfo) ───►│  registers with capability + HttpAddr
    │                            │
    │   (gRPC server on HttpAddr)│
    │◄── AllowCall(req) ─────────│  core queries the module
    │── AllowCall(resp) ────────►│
```

## PolicyService (`proto/muxcore/policy/v1/policy.proto`)

Implemented by modules that control inter-module communication.

| RPC | Purpose | Used by | Capability |
|-----|---------|---------|------------|
| `AllowCall` | Check if module A may call module B | Mesh client (`grpcmesh.Client.Call`) | `call.policy` |
| `AllowPublish` | Check if a module may publish an event type | Event bus (`MemoryBus.Publish`) | `publish.policy` |

### Sidecar Adapters

Core wraps the gRPC client into Go interfaces using adapters in
`internal/grpcmesh/`:

| Adapter | Go Interface | Proto |
|---------|-------------|-------|
| `SidecarCallPolicy` | `contracts.CallPolicyProvider` | `PolicyService.AllowCall` |
| `SidecarPublishPolicy` | `contracts.PublishPolicyProvider` | `PolicyService.AllowPublish` |

### Modules

| Module | Capability | Policy File | Port |
|--------|-----------|-------------|------|
| `call-policy-default` | `call.policy` | `policies.yaml` (YAML allow-list) | `:9101` gRPC, `:9102` health |
| `publish-policy-default` | `publish.policy` | `policies.yaml` (YAML glob matching) | `:9102` gRPC, `:9103` health |

### Bootstrap Flow

1. **Bootstrap wiring**: `wireCallPolicy()` and `wirePublishPolicy()` run before
   sidecar modules are spawned. If an in-process module is registered (legacy
   path), it is wired immediately.
2. **Sidecar spawn**: Modules are spawned by `loadAndSpawnModules()`.
   Registration is asynchronous — the process starts, connects to core, and
   calls `Register()` via gRPC.
3. **Re-discovery**: After spawning, a polling loop waits up to 5 seconds for
   policy modules to appear in the registry. If found, core connects to their
   gRPC server and wires the adapter.

## AuthService (`proto/muxcore/auth/v1/auth.proto`)

Implemented by modules that provide authentication, authorization, and identity
extraction.

| RPC | Purpose | Used by | Capability |
|-----|---------|---------|------------|
| `Authenticate` | Password or API key login → session token | HTTP API | `auth` |
| `Validate` | Token → session | HTTP middleware, gRPC interceptor | `auth` |
| `Revoke` | Invalidate token | HTTP API | `auth` |
| `Can` | Check if user has permission | HTTP authz middleware, gRPC auth interceptor | `authorizer` |
| `ExtractIdentity` | Extract caller identity from context | gRPC auth interceptor | `identity` |
| `EnableTOTP` / `DisableTOTP` / `TOTPStatus` / `VerifyTOTPSetup` | TOTP 2FA management | Admin API | `auth` |
| `CreateUser` / `DeleteUser` / `ListUsers` / `SetPassword` / `SetRoles` | User management | `authctl` CLI | `auth` |
| `CreateAPIToken` / `ListAPITokens` / `DeleteAPIToken` | API token management | `authctl` CLI | `auth` |

### Sidecar Adapters

| Adapter | Go Interface | Proto |
|---------|-------------|-------|
| `SidecarAuthProvider` | `contracts.AuthProvider` | `AuthService.Authenticate` / `Validate` / `Revoke` |
| `SidecarAuthorizer` | `contracts.Authorizer` | `AuthService.Can` |
| `SidecarIdentityProvider` | `contracts.IdentityProvider` | `AuthService.ExtractIdentity` |

### Module

| Module | Capabilities | Ports |
|--------|-------------|-------|
| `auth-local` | `auth`, `authorizer`, `identity` | `:9400` gRPC service, `:9401` HTTP (WebAuthn) |

### Authentication Flows

**Password + optional TOTP:**
```
POST /api/login {username, password}
  → if TOTP enabled: {requires_2fa: true, partial_token, methods: ["totp"]}
  → POST /api/login/totp {partial_token, totp_code}
  → {session_token}

  → if no TOTP: {session_token}
```

**Passkey (WebAuthn):**
```
GET  /api/webauthn/login/begin?username=alice  → credentialRequestOptions
POST /api/webauthn/login/complete               → {session_token}
```

All subsequent requests include `Authorization: Bearer <session_token>`.

## Adding a New Infrastructure Module

1. Add the gRPC service to `proto/muxcore/<service>/v1/<service>.proto`
2. Regenerate Go code with `make proto`
3. Add adapter struct(s) in `internal/grpcmesh/`
4. Wire discovery in `cmd/muxcored/main.go` with a `wire<Service>()` function
5. Add capability string to `re-discovery` polling loop after spawn
6. Build the sidecar module in its own repo, implementing the gRPC server
