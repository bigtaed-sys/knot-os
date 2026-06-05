#!/bin/bash -e
# Host-side substage. Installs the nfqws (zapret) binary into the rootfs.
# The binary is staged into files/ by image/build.sh (which fetches the
# pinned bol-van/zapret release tarball and verifies the linux-arm64
# nfqws SHA-256 against core/internal/zapret/zapret.go) — this script
# just moves it into place.
#
# nfqws is the DPI-circumvention engine knotd drives for the
# YouTube/Discord bypass. knotd also downloads + verifies this exact
# binary on demand when it's absent, so a device updated over OTA from
# an older image still gets the feature; staging here is the fast path.

ZBIN="${BASE_DIR}/stage-knot/05-zapret/files/nfqws"

if [[ ! -f "${ZBIN}" ]]; then
	echo "stage-knot/05-zapret: nfqws binary not staged at ${ZBIN}"
	echo "  → image/build.sh should download + verify it before pi-gen runs."
	exit 1
fi

install -m 755 -D "${ZBIN}" "${ROOTFS_DIR}/usr/local/bin/nfqws"

# The on-disk asset tree (/var/lib/knot/zapret/{lists,bin}) is seeded by
# knotd from its embedded defaults on first enable, and refreshed from
# upstream on demand — nothing to pre-create here.
