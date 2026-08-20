#!/usr/bin/env bash
# Build a minimal BusyBox-style sandbox rootfs (Mode A stub or Mode B real busybox).
#
# Usage:
#   ./scripts/make-sandbox-rootfs.sh <output-dir>
#
# Operator (Mode B) — set one of:
#   MUXCORE_SANDBOX_ROOTFS_BUSYBOX=/path/to/busybox-static
#   MUXCORE_SANDBOX_ROOTFS_BUSYBOX_URL=https://…/busybox
#
# This is not a full distro image. See core/SECURITY.md and internal/sandbox/rootfs.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${1:-}"
if [[ -z "$OUT" ]]; then
  echo "usage: $0 <output-dir>" >&2
  exit 2
fi
mkdir -p "$OUT"
export PATH="${PATH:-}"
cd "$ROOT"
go run ./internal/sandbox/rootfs/cmd/make-rootfs "$OUT"
