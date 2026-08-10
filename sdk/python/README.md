# MuxCore Python SDK

Scaffold for [MASTER-ROADMAP §4.1](../../../../MASTER-ROADMAP.md) language-agnostic module SDKs — **Python** first (`v0.1.0`). TypeScript / Rust follow later.

Mirrors the Go `sdk/go/module` lifecycle: dial muxcored → `ModuleRegistration.Register` → `init`/`start` → wait for SIGINT/SIGTERM → `stop` → `Unregister`.

## Install (editable)

```bash
cd core/sdk/python
pip install -e ".[dev]"
```

## Quick start

```python
import logging
from muxcore.sdk import ModuleInfo, RunConfig, run

logging.basicConfig(level=logging.INFO)

class Echo:
    def info(self) -> ModuleInfo:
        return ModuleInfo(
            id="echo-py",
            name="echo-py",
            version="0.1.0",
            roles=["tool"],
            capabilities=["demo"],
        )

    def init(self) -> None: ...
    def start(self) -> None: ...
    def stop(self) -> None: ...

if __name__ == "__main__":
    run(RunConfig(module=Echo(), insecure=True))
```

```bash
MUXCORE_GRPC_ADDR=127.0.0.1:9090 MUXCORE_INSECURE_DISABLE_TLS=true python echo.py
```

Config resolution (same idea as Go):

1. `RunConfig` fields  
2. `MUXCORE_GRPC_ADDR` / `MUXCORE_MESH_ADDR` / `MUXCORE_MODULE_ID` / `MUXCORE_TLS_*`  
3. `--muxcore-mesh-addr`, `--muxcore-module-id`, `--muxcore-tls-{cert,key,ca}`

## Regenerate protos

From `core/`:

```bash
python -m grpc_tools.protoc \
  -I proto \
  --python_out=sdk/python/src \
  --grpc_python_out=sdk/python/src \
  proto/muxcore/module/v1/registration.proto
```

## Tests

```bash
pytest
```

## Out of scope (follow-ups)

- Client wrappers for Discovery / Storage / Events (Go `sdk/go/client` parity)
- Settings mesh (`RegisterSettings`)
- TypeScript and Rust SDKs
