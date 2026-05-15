#!/bin/bash
# Push wiki content from docs/wiki/ to the GitHub wiki repo.
#
# Prerequisites:
#   1. Visit https://github.com/Muxcore-Media/core/wiki
#   2. Click "Create the first page" (just save with any text)
#   3. Run this script

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
WIKI_SRC="$SCRIPT_DIR/../docs/wiki"
WIKI_REPO="https://github.com/Muxcore-Media/core.wiki.git"
TMP_DIR=$(mktemp -d)

echo "Cloning wiki repo..."
git clone "$WIKI_REPO" "$TMP_DIR"

echo "Copying wiki pages..."
cp "$WIKI_SRC"/*.md "$TMP_DIR/"

cd "$TMP_DIR"
git add *.md
git commit -m "Populate wiki with architecture documentation" || echo "No changes to commit"
git push origin master

echo "Wiki pushed successfully."
rm -rf "$TMP_DIR"
