#!/usr/bin/env bash
# Cross-compile knotd and knotctl for Raspberry Pi Zero 2W (linux/arm64).

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# CalVer dev default: <year>.<month>.0-dev-<short-sha>. Override by
# exporting VERSION (the release CI passes the v-prefixed tag).
VERSION="${VERSION:-$(date -u +%Y.%m).0-dev-$(git rev-parse --short HEAD 2>/dev/null || echo unknown)}"
LDFLAGS="-s -w -X main.Version=${VERSION}"
OUTDIR="./dist/arm64"

mkdir -p "$OUTDIR"

echo "==> Building knotd (linux/arm64)"
GOOS=linux GOARCH=arm64 go build \
    -trimpath \
    -ldflags "$LDFLAGS" \
    -o "$OUTDIR/knotd" \
    ./core/cmd/knotd

echo "==> Building knotctl (linux/arm64)"
GOOS=linux GOARCH=arm64 go build \
    -trimpath \
    -ldflags "$LDFLAGS" \
    -o "$OUTDIR/knotctl" \
    ./cli/cmd/knotctl

echo
echo "Built artifacts:"
ls -lh "$OUTDIR"
