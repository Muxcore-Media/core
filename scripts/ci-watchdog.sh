#!/bin/bash
# ci-watchdog.sh — runs CI pipeline on git changes and reports failures.
# Run via cron or in a loop: while true; do ./scripts/ci-watchdog.sh; sleep 300; done
set -e

export PATH="/usr/local/go/bin:$HOME/go/bin:$HOME/.local/bin:$PATH"
cd "$(dirname "$0")/.."

REPO_DIR="$(pwd)"
STATE_FILE="/tmp/muxcore-ci-state"
NOTIFY_URL="${MUXCORE_NOTIFY_URL:-}"

# Determine last checked commit
if [ -f "$STATE_FILE" ]; then
    LAST_CHECKED=$(cat "$STATE_FILE")
else
    LAST_CHECKED="HEAD"
fi

CURRENT=$(git rev-parse HEAD 2>/dev/null || echo "")
if [ -z "$CURRENT" ]; then
    echo "$(date): not a git repo, skipping"
    exit 0
fi

if [ "$LAST_CHECKED" = "$CURRENT" ]; then
    exit 0
fi

echo "$(date): changes detected, running CI..."

OUT=$(mktemp)
if ! go vet ./internal/... ./pkg/... ./cmd/... > "$OUT" 2>&1; then
    echo "$(date): CI FAILED (vet)"
    cat "$OUT"
    [ -n "$NOTIFY_URL" ] && curl -H "Title: CI Failed (vet)" -d "$(cat "$OUT" | head -c 500)" "$NOTIFY_URL"
    rm -f "$OUT"
    exit 1
fi

if ! go test -race -count=1 -timeout 60s ./internal/... ./pkg/... ./cmd/... > "$OUT" 2>&1; then
    echo "$(date): CI FAILED (test)"
    cat "$OUT"
    [ -n "$NOTIFY_URL" ] && curl -H "Title: CI Failed (test)" -d "$(cat "$OUT" | head -c 500)" "$NOTIFY_URL"
    rm -f "$OUT"
    exit 1
fi

if ! go build -tags default -o /dev/null ./cmd/muxcored > "$OUT" 2>&1; then
    echo "$(date): CI FAILED (build)"
    cat "$OUT"
    [ -n "$NOTIFY_URL" ] && curl -H "Title: CI Failed (build)" -d "$(cat "$OUT" | head -c 500)" "$NOTIFY_URL"
    rm -f "$OUT"
    exit 1
fi

rm -f "$OUT"
echo "$CURRENT" > "$STATE_FILE"
echo "$(date): CI PASSED (vet + test + build)"
