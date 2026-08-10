"""gRPC channel helpers for muxcored."""

from __future__ import annotations

import os
from pathlib import Path

import grpc


def dial(
    addr: str,
    *,
    insecure: bool = False,
    cert_file: str | None = None,
    key_file: str | None = None,
    ca_file: str | None = None,
) -> grpc.Channel:
    """Open a gRPC channel to core (plaintext or mTLS)."""
    if insecure or os.environ.get("MUXCORE_INSECURE_DISABLE_TLS", "").lower() in {
        "1",
        "true",
        "yes",
    }:
        return grpc.insecure_channel(addr)

    cert_file = cert_file or os.environ.get("MUXCORE_TLS_CERT", "")
    key_file = key_file or os.environ.get("MUXCORE_TLS_KEY", "")
    ca_file = ca_file or os.environ.get("MUXCORE_TLS_CA", "")
    if not (cert_file and key_file and ca_file):
        raise ValueError(
            "TLS required: set cert/key/ca files or MUXCORE_TLS_* / "
            "MUXCORE_INSECURE_DISABLE_TLS=true for development"
        )
    private_key = Path(key_file).read_bytes()
    certificate_chain = Path(cert_file).read_bytes()
    root_certificates = Path(ca_file).read_bytes()
    creds = grpc.ssl_channel_credentials(
        root_certificates=root_certificates,
        private_key=private_key,
        certificate_chain=certificate_chain,
    )
    return grpc.secure_channel(addr, creds)
