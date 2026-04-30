#!/bin/bash -e
# Copy bundled plugins into /usr/lib/knot/plugins/ on the rootfs.
# image/build.sh copies the repo's plugins/ tree into
# stage-knot/01-install-plugins/files/ before pi-gen runs, so
# everything we need is already on disk.

SRC="${BASE_DIR}/stage-knot/01-install-plugins/files"
DST="${ROOTFS_DIR}/usr/lib/knot/plugins"

if [[ ! -d "$SRC" ]] || [[ -z "$(ls -A "$SRC" 2>/dev/null)" ]]; then
    echo "no bundled plugins to install"
    exit 0
fi

mkdir -p "$DST"
cp -r "$SRC/." "$DST/"
chmod -R u=rwX,go=rX "$DST"

echo "installed plugins:"
ls -1 "$DST"
