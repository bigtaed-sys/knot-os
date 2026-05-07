#!/bin/bash -e
# Host-side substage. Installs the sing-box binary into the
# rootfs. The binary is staged into files/ by image/build.sh
# (which fetches it from the upstream GitHub release and verifies
# the SHA-256 against a pinned checksum) — this script just
# moves it into place.
#
# We pin the version in singbox.Version (core/internal/singbox/
# singbox.go) — the build.sh fetcher reads that constant via
# `go run` to keep the source-of-truth in one place.

SBIN="${BASE_DIR}/stage-knot/03-singbox/files/sing-box"

if [[ ! -f "${SBIN}" ]]; then
	echo "stage-knot/03-singbox: sing-box binary not staged at ${SBIN}"
	echo "  → image/build.sh should download + verify it before pi-gen runs."
	exit 1
fi

install -m 755 -D "${SBIN}" "${ROOTFS_DIR}/usr/local/bin/sing-box"

# /run/knot/ is created by knotd at startup (tmpfs), so we don't
# pre-create the sing-box config dir here.
