#!/bin/bash -e
# Host-side substage. Installs the Xray-core binary into the rootfs.
# The binary is staged into files/ by image/build.sh (which fetches
# it from the upstream GitHub release and verifies the SHA-256
# against a pinned checksum) — this script just moves it into place.
#
# We pin the version in xray.Version (core/internal/xray/xray.go) —
# the build.sh fetcher reads that constant via `go run` to keep the
# source-of-truth in one place.
#
# Xray runs alongside sing-box: sing-box owns the TUN + per-device
# routing, Xray hosts the servers sing-box can't speak (xhttp etc.)
# behind loopback SOCKS inbounds that sing-box dials.

XBIN="${BASE_DIR}/stage-knot/04-xray/files/xray"

if [[ ! -f "${XBIN}" ]]; then
	echo "stage-knot/04-xray: xray binary not staged at ${XBIN}"
	echo "  → image/build.sh should download + verify it before pi-gen runs."
	exit 1
fi

install -m 755 -D "${XBIN}" "${ROOTFS_DIR}/usr/local/bin/xray"

# /run/knot/ is created by knotd at startup (tmpfs); the xray config
# is rendered there at runtime, so nothing to pre-create here.
