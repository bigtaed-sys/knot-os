#!/bin/bash -e
# Host-side substage. Runs before chroot setup. Copies the
# pre-built knotd and knotctl binaries plus seed files into the
# rootfs.
#
# image/build.sh stages the binaries into files/ before invoking
# pi-gen, so they are guaranteed to be present here.

install -m 755 -D "${BASE_DIR}/stage-knot/00-install-knotd/files/knotd" \
                  "${ROOTFS_DIR}/usr/local/bin/knotd"

install -m 755 -D "${BASE_DIR}/stage-knot/00-install-knotd/files/knotctl" \
                  "${ROOTFS_DIR}/usr/local/bin/knotctl"

install -m 644 -D "${BASE_DIR}/stage-knot/00-install-knotd/files/knotd.service" \
                  "${ROOTFS_DIR}/etc/systemd/system/knotd.service"

install -m 600 -D "${BASE_DIR}/stage-knot/00-install-knotd/files/default-config.yaml" \
                  "${ROOTFS_DIR}/etc/knot/config.yaml"

# /etc/knot is owned by root with 0700 — knotd reads/writes here, no
# one else needs to look.
chmod 700 "${ROOTFS_DIR}/etc/knot"
