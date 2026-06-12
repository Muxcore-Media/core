# MuxCore Module Deployment Templates

Copy the relevant file(s) from this directory into your module repository
to get a working deployment for every supported method.

## Quick Start

```bash
# Copy everything
cp -r deploy/module-templates/* my-module/

# Or just what you need
cp deploy/module-templates/Dockerfile my-module/
cp -r deploy/module-templates/systemd my-module/deploy/
cp -r deploy/module-templates/helm my-module/deploy/
```

## What to Customize

Each template contains `TODO` markers. Search for `TODO` and fill in:

| Template | TODOs |
|----------|-------|
| `Dockerfile` | Module binary name, build path |
| `systemd/` | Module binary path, user, description |
| `helm/` | Image repo, module ID, config values |
| `kustomize/` | Image, module ID, namespace |
| `ansible/` | Binary source, systemd unit name |

## How It Works

All deployment methods converge on the same environment variables
documented in `CONTRACT.md`. The module binary reads these vars
(via the Go SDK or directly) and connects to core's gRPC endpoint.
