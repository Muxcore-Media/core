# Sidecar Module Manager

The sidecar module manager (`internal/module/mgr/`) controls the full lifecycle
of external module processes — from fetching source code to spawning, monitoring,
and optionally restarting child processes.

---

## Sidecar Pipeline

When a tag is loaded via `--tag default`, each module listed in the tag goes
through this pipeline:

```
spool tag entry (repo URL, version, checksum)
          │
          ▼
    Resolve(repo, version)
    ┌─────────────────────────────────────────┐
    │  cache hit?                              │
    │  ~/.muxcore/modules/<id>/<version>/      │
    │  muxcore-module                          │
    │                                          │
    │  cache miss → clone + scan + build       │
    │  git clone --depth 1 --branch <version>  │
    │  scanModuleSource() ← reject unsafe/cgo  │
    │  reconcileContracts() ← go.mod replace   │
    │  go build -o muxcore-module              │
    │  cache binary                            │
    └─────────────────────────────────────────┘
          │
          ▼
    VerifyChecksum(bin, expected)
    SHA-256 of binary must match spool entry
    (skipped if checksum is empty — backward compat)
          │
          ▼
    Spawn(ctx, bin)
    exec.Command(bin.Path, --muxcore-mesh-addr, --muxcore-module-id)
    stdout/stderr → os.Stdout/os.Stderr
    watchProcess() goroutine monitors for exit
```

---

## Source Scanning

Before compiling a cloned module, `scanModuleSource()` walks all `.go` files
and enforces these rules:

| Pattern | Action | Why |
|---------|--------|-----|
| `import "unsafe"` | **Reject** (error) | Memory safety violation risk |
| `import "C"` | **Reject** (error) | Arbitrary C code runs at build time |
| `//go:generate` | **Warn** (logged) | Can execute arbitrary commands |

The scanner is intentionally conservative. Modules using `unsafe` or `cgo`
cannot be loaded from the spool — they must be pre-built and placed in the
cache manually.

---

## Contract Reconciliation

If the cloned module includes a `muxcore.json` with a `contracts` array,
`reconcileContracts()` checks each declared contract against the canonical
Muxcore-Media contract repos using `contracts-reconciler`.

When a module uses a non-canonical contract that is structurally compatible,
`go.mod` `replace` directives are applied automatically to normalize imports.
This allows third-party modules to ship their own copy of a contract
interface and still be compatible with core.

---

## Cache Layout

```
~/.muxcore/modules/
├── downloader-qbittorrent/
│   ├── v1.2.0/
│   │   └── muxcore-module
│   └── v1.3.0/
│       └── muxcore-module    ← most recent, kept
└── transcoder-ffmpeg/
    └── v2.0.1/
        └── muxcore-module
```

`PruneCache(keepVersions)` is called at startup with `keepVersions=3`.
It deletes all but the N most recent version directories per module.
Versions are sorted lexicographically (semver tags from `git describe` sort
correctly this way).

---

## Restart Policy

`ModuleBinary.RestartPolicy` controls what happens when a sidecar exits:

| Policy | Behaviour |
|--------|-----------|
| `never` (default) | Exit is logged; process is not restarted |
| `on-failure` | Restarts on non-zero exit code with exponential back-off |
| `always` | Restarts on any exit (including clean exit 0) |

Back-off: 1s → 2s → 4s → 8s → 16s → 30s (capped). After `maxRestartAttempts`
(5) the manager logs a permanent failure and stops trying.

---

## gRPC Registration

Once spawned, the module binary connects to core via the `--muxcore-mesh-addr`
flag and calls `ModuleRegistration.Register()`. The registration server:

1. Deserialises the structured `ModuleInfo` protobuf field into `contracts.ModuleInfo`.
2. Applies `min_core_version` from the proto field for version compatibility checks.
3. Creates a `SidecarProxy` (satisfies `contracts.Module` for the lifecycle manager).
4. Registers the proxy with the module lifecycle manager (publishes `module.registered`).
5. Returns `mesh_addr` so the module knows where to call storage, events, etc.

At shutdown, the module calls `ModuleRegistration.Unregister()` to deregister
cleanly before SIGTERM.

---

## Module Stdout/Stderr

Module stdout and stderr are currently directed to core's own stdout/stderr.
In `log.format=json` mode this means module text lines corrupt the JSON stream.
A fix (prefix or separate log files per module) is planned.

---

## Files

| File | Purpose |
|------|---------|
| `internal/module/mgr/manager.go` | Manager, Resolve, Spawn, PruneCache, StopAll, watchProcess |
| `internal/module/mgr/proxy.go` | SidecarProxy — wraps a module binary as contracts.Module |
