# Compatibility Policy

This document defines what MuxCore v1 guarantees to module authors and what can
change between minor and patch versions.

## The Loom Never Changes

The core platform (the "loom") provides a fixed set of infrastructure services.
Modules ("threads") plug into these services and are never required to change
when the core is updated within the v1 major version.

## Stable for v1 (never breaks)

The following interfaces are frozen for the v1 lifetime. Breaking changes —
removing or changing any of these — require a v2 major version.

| Category | What's covered |
|----------|---------------|
| **6 gRPC services** | `ModuleRegistration`, `DiscoveryService`, `StorageService`, `EventService`, `ModuleMesh`, `HealthService` — all RPC signatures, message types, and field numbers/names/types in the `*.v1` proto packages |
| **38 Go interfaces** | Every exported type in `pkg/contracts/` — all interface methods, struct fields, constants, sentinel errors, and function signatures |
| **Module lifecycle** | `Register → Init → Start → Stop → Health` — the five lifecycle states and their transitions |
| **Core event types** | `module.registered`, `module.unregistered`, `module.degraded` — event schema and payload structure |
| **Capability strings** | All 39 capability constants in `capabilities.go` (additive only — new ones may be added) |
| **SDK mock package** | `sdk/go/mock/` — exported types and their method signatures |
| **Sentinel errors** | `ErrNotFound`, `ErrCallDenied`, `ErrLockHeld`, `ErrDatabaseCredentialExposure`, `ErrRollbackNotSupported`, `ErrMigrationTargetNotFound`, `ErrCircuitOpen`, `ErrCallNoPolicy` |

## Can change in v1.x (no module impact)

These can be modified, added, or removed without a major version bump. Existing
modules will continue to work unchanged.

| Category | Examples |
|----------|----------|
| **Internal packages** | `internal/*` — bug fixes, performance improvements, refactoring, security hardening |
| **New proto fields** | Optional fields may be added to existing proto messages (standard protobuf backward compatibility — unknown fields are preserved) |
| **New RPCs** | New RPCs may be added to existing proto services |
| **New capability constants** | New capability strings may be added to `capabilities.go` |
| **New sentinel errors** | New error sentinels may be added |
| **New env vars** | Environment variables are additive only |
| **New config file keys** | Configuration keys are additive only |
| **New CLI flags** | CLI flags are additive only |
| **Observability endpoints** | New HTTP endpoints for health, metrics, or debugging |

## Extension mechanism

When core needs to support new behaviour without breaking existing modules, it
uses one or more of these patterns:

1. **Optional interface extension** — A new interface that existing modules can
   opt into via runtime type assertion. Example: `ResourcePublishPolicyProvider`
   extends `PublishPolicyProvider`. Modules that don't implement the new
   interface continue to work with the base behaviour.

2. **New capability string + new gRPC service/RPC** — A new capability constant
   in `capabilities.go` allows modules to advertise support for a new service.
   The new RPC is added to an existing proto service or a new service is created.

3. **New event type** — A new core lifecycle event type is published. Existing
   subscribers don't need to change; they simply ignore events they don't
   handle.

## Deprecation policy

1. A feature marked as **deprecated** in v1 documentation and code comments is
   still fully supported and will not be removed within the v1 lifetime.
2. Deprecated features are removed in v2 (the next major version).
3. A deprecation notice must remain in place for at least one full major version
   cycle before removal.

## What this means for module authors

- **Write modules against pkg/contracts/ and the *.v1 proto packages.** These
  are the stable API surface.
- **Do not import internal/ packages.** They provide no compatibility
  guarantees and can change at any time.
- **Implement the full interface.** When you implement a contract interface,
  implement all methods. Future optional interfaces will use type assertions,
  not additions to existing interfaces.
- **Declare MinCoreVersion in ModuleInfo.** This tells core what minimum
  version your module requires. Core checks this at registration and rejects
  incompatible modules with a clear error.

## Version numbering

MuxCore follows Semantic Versioning 2.0.0:

- **Major version** increments indicate breaking changes to the stable API
  surface (the items listed above under "Stable for v1").
- **Minor version** increments indicate new functionality that is backward
  compatible.
- **Patch version** increments indicate bug fixes that are backward compatible.

Development builds (non-release binaries) use the version string `0.0.0-dev`
and accept all modules regardless of MinCoreVersion.
