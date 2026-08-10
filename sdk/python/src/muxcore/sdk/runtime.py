"""Module lifecycle: connect, register, init/start, signal stop, unregister."""

from __future__ import annotations

import argparse
import logging
import os
import signal
import threading
from dataclasses import dataclass, field
from typing import Protocol, runtime_checkable

from muxcore.module.v1 import registration_pb2, registration_pb2_grpc
from muxcore.sdk.channel import dial

logger = logging.getLogger("muxcore.sdk")

_ENV_GRPC = "MUXCORE_GRPC_ADDR"
_ENV_MESH = "MUXCORE_MESH_ADDR"
_ENV_MODULE_ID = "MUXCORE_MODULE_ID"


@dataclass
class ModuleInfo:
    """Identity advertised to core during Register."""

    id: str = ""
    name: str = ""
    version: str = "0.0.0"
    roles: list[str] = field(default_factory=list)
    description: str = ""
    author: str = ""
    capabilities: list[str] = field(default_factory=list)
    depends_on: list[str] = field(default_factory=list)
    min_core_version: str = ""
    http_addr: str = ""


@runtime_checkable
class Module(Protocol):
    """Sidecar module contract (mirrors Go contracts.Module)."""

    def info(self) -> ModuleInfo: ...
    def init(self) -> None: ...
    def start(self) -> None: ...
    def stop(self) -> None: ...


@dataclass
class RunConfig:
    module: Module
    grpc_addr: str = ""
    module_id: str = ""
    insecure: bool = False
    tls_cert_file: str = ""
    tls_key_file: str = ""
    tls_ca_file: str = ""
    shutdown_timeout_sec: float = 30.0


def _resolve_addr(explicit: str) -> str:
    return (
        explicit
        or os.environ.get(_ENV_GRPC, "")
        or os.environ.get(_ENV_MESH, "")
        or ""
    )


def _parse_cli_overrides() -> tuple[str, str]:
    """Parse --muxcore-* flags without claiming the whole argv for the module."""
    parser = argparse.ArgumentParser(add_help=False)
    parser.add_argument("--muxcore-mesh-addr", default="")
    parser.add_argument("--muxcore-module-id", default="")
    parser.add_argument("--muxcore-tls-cert", default="")
    parser.add_argument("--muxcore-tls-key", default="")
    parser.add_argument("--muxcore-tls-ca", default="")
    args, _ = parser.parse_known_args()
    return args.muxcore_mesh_addr, args.muxcore_module_id


def run(cfg: RunConfig) -> None:
    """Register with muxcored, run the module until SIGINT/SIGTERM, then unregister."""
    if cfg.module is None:
        raise ValueError("module is required")

    flag_addr, flag_id = _parse_cli_overrides()
    grpc_addr = _resolve_addr(cfg.grpc_addr or flag_addr)
    info = cfg.module.info()
    module_id = cfg.module_id or os.environ.get(_ENV_MODULE_ID, "") or flag_id or info.id

    if not grpc_addr:
        raise ValueError(
            f"core gRPC address required — set {_ENV_GRPC}/{_ENV_MESH} or --muxcore-mesh-addr"
        )
    if not module_id:
        raise ValueError(f"module id required — set {_ENV_MODULE_ID} or --muxcore-module-id")

    channel = dial(
        grpc_addr,
        insecure=cfg.insecure,
        cert_file=cfg.tls_cert_file or None,
        key_file=cfg.tls_key_file or None,
        ca_file=cfg.tls_ca_file or None,
    )
    try:
        stub = registration_pb2_grpc.ModuleRegistrationStub(channel)
        info.id = module_id
        resp = stub.Register(
            registration_pb2.RegisterRequest(
                module_id=module_id,
                min_core_version=info.min_core_version,
                module_info=registration_pb2.ModuleInfo(
                    id=info.id,
                    name=info.name or module_id,
                    version=info.version,
                    roles=list(info.roles),
                    description=info.description,
                    author=info.author,
                    capabilities=list(info.capabilities),
                    depends_on=list(info.depends_on),
                    min_core_version=info.min_core_version,
                    http_addr=info.http_addr,
                ),
            )
        )
        if not resp.accepted:
            raise RuntimeError(f"core rejected registration: {resp.error}")

        logger.info(
            "registered id=%s version=%s mesh=%s node=%s",
            module_id,
            info.version,
            resp.mesh_addr,
            resp.node_id,
        )

        cfg.module.init()
        logger.info("initialized id=%s", module_id)
        cfg.module.start()
        logger.info("started id=%s", module_id)

        stop = threading.Event()

        def _handle(signum: int, _frame: object) -> None:
            logger.info("signal %s — shutting down id=%s", signum, module_id)
            stop.set()

        signal.signal(signal.SIGINT, _handle)
        signal.signal(signal.SIGTERM, _handle)
        stop.wait()

        try:
            cfg.module.stop()
        except Exception:
            logger.exception("stop failed id=%s", module_id)

        try:
            stub.Unregister(registration_pb2.UnregisterRequest(module_id=module_id))
        except Exception:
            logger.exception("unregister failed id=%s", module_id)
        logger.info("stopped id=%s", module_id)
    finally:
        channel.close()
