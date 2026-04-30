#!/usr/bin/env bash
# Build the SvelteKit UI and copy the artifacts into core/internal/web/dist
# so the next `go build` embeds them via //go:embed.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT/ui"

echo "==> Installing UI dependencies (npm ci)"
npm ci --no-audit --no-fund --loglevel=error

echo "==> Building UI"
npm run build

DEST="$ROOT/core/internal/web/dist"
echo "==> Copying build to $DEST"
rm -rf "$DEST"
mkdir -p "$DEST"
cp -r "$ROOT/ui/build/." "$DEST/"

# Restore the placeholder so the directory is never empty for embed.
cat > "$DEST/.gitkeep" <<'EOF'
This directory is populated by scripts/build-ui.sh before `go build`.
The .gitkeep ensures `//go:embed all:dist` always has at least one file.
EOF

echo "==> Done"
ls -la "$DEST" | head -10
