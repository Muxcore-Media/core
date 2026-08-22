#!/usr/bin/env bash
# Build muxcored on vault (Forgejo module fetch) and optionally push to GHCR.
# Use when desk lacks podman or gh write:packages; run from desk via SSH or on vault.
#
# Usage (from desk):
#   GHCR_TOKEN="$(gh auth token)" ./scripts/publish-muxcored-vault-ghcr.sh v0.5.8
#
# BUILD_ONLY (no push):
#   BUILD_ONLY=1 ./scripts/publish-muxcored-vault-ghcr.sh v0.5.8
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

TAG="${1:-v0.5.8}"
VAULT="${MUXCORE_VAULT_SSH:-ender@fd2c:a2fd:5d9e:ab72:9d99:930d:f160:3e95}"
BUILD_ONLY="${BUILD_ONLY:-0}"
GHCR_TOKEN="${GHCR_TOKEN:-}"

[[ "$TAG" == v* ]] || TAG="v${TAG}"

if [[ -f Dockerfile.goreleaser && -f cmd/muxcored/main.go && -x "$(command -v podman 2>/dev/null || true)" ]]; then
  ver="${TAG#v}"
  export GOPRIVATE='github.com/Muxcore-Media/*' GOPROXY='https://proxy.golang.org,direct'
  export PATH="${HOME}/.local/go/bin:${PATH}"
  git config --global url."https://git.zem.systems/muxcore/".insteadOf "https://github.com/Muxcore-Media/" 2>/dev/null || true
  command -v go >/dev/null || { echo "FAIL: go not on PATH" >&2; exit 1; }
  command -v podman >/dev/null || { echo "FAIL: podman not found" >&2; exit 1; }
  CGO_ENABLED=0 go build \
    -ldflags="-s -w -X github.com/Muxcore-Media/core/internal/version.Version=${ver}" \
    -o muxcored ./cmd/muxcored
  podman build -t "localhost/muxcored:${TAG}" -f Dockerfile.goreleaser .
  if [[ "$BUILD_ONLY" == "1" || -z "$GHCR_TOKEN" ]]; then
    echo "BUILD_ONLY — image ready: localhost/muxcored:${TAG}"
    exit 0
  fi
  echo "$GHCR_TOKEN" | podman login ghcr.io -u muxcore-media --password-stdin
  podman tag "localhost/muxcored:${TAG}" "ghcr.io/muxcore-media/muxcored:${TAG}"
  podman tag "localhost/muxcored:${TAG}" "ghcr.io/muxcore-media/core:${TAG}"
  podman push "ghcr.io/muxcore-media/muxcored:${TAG}"
  podman push "ghcr.io/muxcore-media/core:${TAG}"
  echo "published ghcr.io/muxcore-media/muxcored:${TAG}"
  exit 0
fi

echo "== remote build on vault =="
ssh -6 "$VAULT" bash -s <<REMOTE
set -euo pipefail
TAG='${TAG}'
BUILD_ONLY='${BUILD_ONLY}'
GHCR_TOKEN='${GHCR_TOKEN}'
TMP=/tmp/muxcore-ghcr-publish-\$\$
rm -rf "\$TMP"
git clone --depth 1 --branch "\$TAG" ssh://forgejo@127.0.0.1:2222/muxcore/core.git "\$TMP"
cd "\$TMP"
export GOPRIVATE='github.com/Muxcore-Media/*' GOPROXY='https://proxy.golang.org,direct'
export PATH="\$HOME/.local/go/bin:\$PATH"
git config --global url."https://git.zem.systems/muxcore/".insteadOf "https://github.com/Muxcore-Media/"
ver="\${TAG#v}"
CGO_ENABLED=0 go build \
  -ldflags="-s -w -X github.com/Muxcore-Media/core/internal/version.Version=\${ver}" \
  -o muxcored ./cmd/muxcored
podman build -t "localhost/muxcored:\${TAG}" -f Dockerfile.goreleaser .
if [[ "\$BUILD_ONLY" == "1" || -z "\${GHCR_TOKEN:-}" ]]; then
  echo "BUILD_ONLY — image ready on vault: localhost/muxcored:\${TAG}"
  exit 0
fi
echo "\$GHCR_TOKEN" | podman login ghcr.io -u muxcore-media --password-stdin
podman tag "localhost/muxcored:\${TAG}" "ghcr.io/muxcore-media/muxcored:\${TAG}"
podman tag "localhost/muxcored:\${TAG}" "ghcr.io/muxcore-media/core:\${TAG}"
podman push "ghcr.io/muxcore-media/muxcored:\${TAG}"
podman push "ghcr.io/muxcore-media/core:\${TAG}"
echo "published ghcr.io/muxcore-media/muxcored:\${TAG}"
REMOTE
