#!/usr/bin/env bash
# Run knotd in dev mode with mocked network backend.
# Intended for local development on Linux/macOS/WSL2.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

mkdir -p ./tmp/etc-knot ./tmp/var-knot

go run ./core/cmd/knotd \
    -dev \
    -config ./tmp/etc-knot/config.yaml \
    -listen :8080
