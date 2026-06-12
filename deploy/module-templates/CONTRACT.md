# Module Environment Contract

All MuxCore modules communicate with core via a fixed set of environment
variables. Every deployment method (Helm, Kustomize, Ansible, systemd, Docker)
must ensure these variables reach the module process.

## Required

| Variable | Purpose | Example |
|----------|---------|---------|
| `MUXCORE_GRPC_ADDR` | Core gRPC address (host:port) | `core:9090` or `10.0.0.1:9090` |

## Strongly Recommended

Set these so core can identify the module. If absent, the module binary
should read `--muxcore-module-id` or use a sensible default.

| Variable | Purpose | Example |
|----------|---------|---------|
| `MUXCORE_MODULE_ID` | Unique module identifier | `metadata-tmdb`, `downloader-qbittorrent` |

## Optional

| Variable | Purpose | Example |
|----------|---------|---------|
| `MUXCORE_AUTH_TOKEN` | Cluster authentication token | (long hex string) |
| `MUXCORE_CFG_*` | Per-module configuration keys | `MUXCORE_CFG_API_KEY=abc123` |

## How Core Passes These (child-process mode)

When core spawns a module as a child process via the spool/tag system:
- `--muxcore-mesh-addr` and `--muxcore-module-id` are passed as CLI flags
- `MUXCORE_CFG_*` keys from the tag definition are injected as env vars

## How to Pass These (independent mode)

Each deployment method uses its own mechanism. See the templates in this
directory for per-method examples.
