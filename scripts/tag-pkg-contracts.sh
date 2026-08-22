#!/usr/bin/env bash
# Cut nested module tag pkg/contracts/vX.Y.Z on the current commit (matches core release version).
# Requires: contracts-media v0.1.0+ resolvable via GOPRIVATE/Forgejo insteadOf; no replace in pkg/contracts/go.mod.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

ver="${1:-}"
if [[ -z "$ver" ]]; then
  ver="$(git describe --tags --match 'v*' --abbrev=0 2>/dev/null | sed 's/^v//')"
fi
if [[ -z "$ver" ]]; then
  echo "usage: $0 [X.Y.Z]" >&2
  exit 1
fi

tag="pkg/contracts/v${ver}"
if git rev-parse "$tag" >/dev/null 2>&1; then
  echo "tag $tag already exists"
  exit 0
fi

echo "Verifying pkg/contracts resolves published contracts-media..."
(
  cd pkg/contracts
  go mod download github.com/Muxcore-Media/contracts-media@v0.1.0
  go test -count=1 ./...
)

git tag -a "$tag" -m "core/pkg/contracts v${ver} (nested module; contracts-media v0.1.0)"
echo "Created $tag — push with: git push origin $tag"
