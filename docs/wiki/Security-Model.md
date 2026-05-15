# Security Model

## Philosophy

Most self-hosted media software has **terrible security**. MuxCore differentiates by building security into the core from day one.

## Features

### Authentication

Authentication is **module-driven** — the core does not authenticate users directly. Auth modules implement the `AuthProvider` contract and tie into existing identity infrastructure. Multiple auth modules can be active simultaneously.

- **Auth modules** — Plex auth, OAuth/OIDC (Authentik, Authelia, Keycloak, Google, GitHub), LDAP/AD, local accounts with TOTP 2FA
- **API tokens** — For programmatic access, scoped and revocable
- **Module tokens** — Modules authenticate to the core, not to each other

See [Module Types](Module-Types) — Authentication Modules for the full contract.

### Authorization (RBAC)

```go
type Authorizer interface {
    Can(ctx, session, action, resource) (bool, error)
}
```

#### Example Roles

| Role | Permissions |
|------|-------------|
| **Admin** | Full system access |
| **Manager** | Manage media, approve requests |
| **User** | Request media, view library |
| **Viewer** | View library only |
| **Module** | Scoped to module's declared needs |

#### Example Policies
```yaml
policies:
  - role: user
    can: [media.request, media.view]
    on: [movies, tv, music]

  - role: manager
    can: [media.approve, media.delete, media.edit]
    on: [movies, tv, music, books]

  - role: admin
    can: ["*"]
    on: ["*"]
```

### Module Permissions

Each module declares required permissions in its manifest:

```yaml
# module.yaml
name: downloader-qbittorrent
kind: downloader
permissions:
  - storage.write:downloads/*
  - events.publish:download.*
  - events.subscribe:media.download.approved
  - registry.read:media-managers
```

The core enforces these — a module cannot access more than declared.

### Network Security
- **Reverse proxy aware** — Works behind Traefik, Caddy, Nginx
- **mTLS** — Between core and external modules
- **Network policies** — Module-to-module communication is restricted by policy
- **API rate limiting** — Per-user, per-token

### Data Security
- **Audit logs** — Every action is recorded: who did what, when, from where
- **Encryption at rest** — Via storage overlay providers
- **Encryption in transit** — gRPC uses TLS, NATS uses TLS
- **No secrets in logs** — Automatic redaction

### Sandboxing
- **External modules** — Run as separate processes with limited OS permissions
- **Docker/container isolation** — Recommended deployment pattern
- **gVisor/Firecracker** — Optional microVM isolation for untrusted modules

## API Token Scopes

```json
{
  "name": "readonly-monitor",
  "scopes": ["events:read", "status:read"],
  "expires": "2027-01-01T00:00:00Z"
}
```

## Security Roadmap

| Phase | Feature |
|-------|---------|
| MVP | Local accounts auth module, API tokens, basic RBAC |
| Phase 2 | OIDC/SSO auth module, LDAP auth module, audit logging, mTLS |
| Phase 3 | Sandbox policies, network segmentation, SIEM integration |
