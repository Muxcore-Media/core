"""Unit tests for the Python module SDK (in-process gRPC fake)."""

from __future__ import annotations

import threading
from concurrent import futures

import grpc
import pytest

from muxcore.module.v1 import registration_pb2, registration_pb2_grpc
from muxcore.sdk import ModuleInfo, RunConfig, run
from muxcore.sdk.channel import dial


class _Reg(registration_pb2_grpc.ModuleRegistrationServicer):
    def __init__(self) -> None:
        self.registered: list[str] = []
        self.unregistered: list[str] = []

    def Register(self, request, context):  # noqa: N802
        self.registered.append(request.module_id)
        return registration_pb2.RegisterResponse(
            accepted=True,
            mesh_addr="127.0.0.1:9090",
            node_id="node-1",
        )

    def Unregister(self, request, context):  # noqa: N802
        self.unregistered.append(request.module_id)
        return registration_pb2.UnregisterResponse(acknowledged=True)

    def BootstrapRegister(self, request, context):  # noqa: N802
        return registration_pb2.BootstrapRegisterResponse(accepted=False, error="unused")


@pytest.fixture()
def fake_core():
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=2))
    svc = _Reg()
    registration_pb2_grpc.add_ModuleRegistrationServicer_to_server(svc, server)
    port = server.add_insecure_port("127.0.0.1:0")
    server.start()
    yield f"127.0.0.1:{port}", svc
    server.stop(grace=None)


def test_dial_insecure(fake_core):
    addr, _ = fake_core
    ch = dial(addr, insecure=True)
    stub = registration_pb2_grpc.ModuleRegistrationStub(ch)
    resp = stub.Register(
        registration_pb2.RegisterRequest(
            module_id="t",
            module_info=registration_pb2.ModuleInfo(id="t", name="t", version="0.0.1"),
        )
    )
    assert resp.accepted
    ch.close()


def test_run_registers_and_unregisters(fake_core):
    addr, svc = fake_core
    started = threading.Event()

    class Demo:
        def info(self) -> ModuleInfo:
            return ModuleInfo(id="demo-py", name="demo-py", version="0.1.0", roles=["tool"])

        def init(self) -> None:
            return None

        def start(self) -> None:
            started.set()

        def stop(self) -> None:
            return None

    def _kill():
        started.wait(timeout=5)
        # interrupt run() via its stop event by sending SIGTERM to ourselves
        import os
        import signal

        os.kill(os.getpid(), signal.SIGTERM)

    threading.Thread(target=_kill, daemon=True).start()
    run(RunConfig(module=Demo(), grpc_addr=addr, insecure=True))
    assert svc.registered == ["demo-py"]
    assert svc.unregistered == ["demo-py"]
