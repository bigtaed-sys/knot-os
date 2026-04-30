#!/usr/bin/env bash
# Cross-compile knotd and knotctl for Raspberry Pi Zero 2W (linux/arm64).

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

VERSION="${VERSION:-0.0.0-dev}"
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
