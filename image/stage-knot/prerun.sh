#!/bin/bash -e
# Inherit the rootfs from the previous stage. pi-gen runs prerun.sh
# at the start of each stage; we copy the previous stage's work
# (Raspberry Pi OS Lite) into ours and layer KnotOS on top.

if [ ! -d "${ROOTFS_DIR}" ]; then
    copy_previous
fi
