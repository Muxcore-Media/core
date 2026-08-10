#!/usr/bin/env bash
# Build muxcored from this repo's Dockerfile and push to a local registry.
# Delegates to the workspace laptop helper when present; otherwise documents
# the docker/podman recipe against ./Dockerfile.
set -euo pipefail

CORE_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
HELPER="${MUXCORE_LOCAL_REGISTRY_SCRIPT:-$CORE_ROOT/../_mvp/local-registry.sh}"
TAG="${1:-${MUXCORE_LOCAL_IMAGE_TAG:-v0.5.0}}"
REGISTRY_HOST="${MUXCORE_LOCAL_REGISTRY:-localhost:5000}"
IMAGE_REPO="${MUXCORE_LOCAL_IMAGE_REPO:-muxcore/muxcored}"

if [[ -x "$HELPER" ]]; then
  export MUXCORE_CORE_DIR="$CORE_ROOT"
  export MUXCORE_LOCAL_IMAGE_TAG="$TAG"
  exec "$HELPER" push "$TAG"
fi

RUNTIME="${CONTAINER_RUNTIME:-}"
if [[ -z "$RUNTIME" ]]; then
  if command -v docker >/dev/null 2>&1; then RUNTIME=docker
  elif command -v podman >/dev/null 2>&1; then RUNTIME=podman
  else
    cat >&2 <<EOF
FAIL: no container runtime and helper missing ($HELPER).

Manual recipe (with Docker/Podman + local registry on ${REGISTRY_HOST}):
  cd $CORE_ROOT
  \$RUNTIME build --build-arg VERSION=${TAG} -t ${REGISTRY_HOST}/${IMAGE_REPO}:${TAG} .
  \$RUNTIME push ${REGISTRY_HOST}/${IMAGE_REPO}:${TAG}
EOF
    exit 1
  fi
fi

cd "$CORE_ROOT"
"$RUNTIME" build --build-arg "VERSION=${TAG}" -t "${REGISTRY_HOST}/${IMAGE_REPO}:${TAG}" .
"$RUNTIME" push "${REGISTRY_HOST}/${IMAGE_REPO}:${TAG}"
echo "OK: pushed ${REGISTRY_HOST}/${IMAGE_REPO}:${TAG}"
