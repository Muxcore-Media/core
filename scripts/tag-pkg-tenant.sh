#!/usr/bin/env bash
# Cut nested module tag pkg/tenant/vX.Y.Z on the current commit (matches core release version).
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

tag="pkg/tenant/v${ver}"
if git rev-parse "$tag" >/dev/null 2>&1; then
  echo "tag $tag already exists"
  exit 0
fi

echo "Verifying pkg/tenant tests (monorepo; remove replace directives before tagging for publish)..."
(
  cd pkg/tenant
  go test -count=1 ./...
)

git tag -a "$tag" -m "core/pkg/tenant v${ver} (nested module)"
echo "Created $tag — push with: git push origin $tag"
