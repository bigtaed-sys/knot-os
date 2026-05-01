#!/usr/bin/env bash
# Build a flashable KnotOS image by modifying the official Raspberry Pi
# OS Lite arm64 image. Much faster than running pi-gen from scratch
# (5-10 minutes vs 30-60), and skips the qemu-emulated debootstrap of a
# full base system - we only chroot to add 4-5 packages on top of the
# already-built Lite rootfs.
#
# Usage:  sudo bash image/build.sh
#
# Requirements:
#   - Linux host (WSL2 Ubuntu works on Windows; use image/build.ps1).
#   - Go 1.22+ and Node 18+ on PATH for the UI/binary build steps.
#   - apt packages: xz-utils qemu-user-static parted e2fsprogs curl
#                   util-linux mount fdisk
#   - Run as root (we do losetup, mount, chroot).
#
# Output:
#   image/deploy/<timestamp>-KnotOS-zero2w-<version>.img.xz

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

VERSION="${VERSION:-0.1.0-dev}"
LITE_URL="${LITE_URL:-https://downloads.raspberrypi.com/raspios_lite_arm64_latest}"
QEMU_BIN="${QEMU_BIN:-/usr/bin/qemu-aarch64-static}"

CACHE_DIR="$ROOT/image/cache"
LITE_IMG_XZ="$CACHE_DIR/raspios_lite_arm64.img.xz"
WORK_DIR="$ROOT/image/work"
WORK_IMG="$WORK_DIR/knotos.img"
MOUNT_DIR="$WORK_DIR/mnt"
DEPLOY_DIR="$ROOT/image/deploy"
STAGE_DIR="$ROOT/image/stage-knot"
FILES_DIR="$STAGE_DIR/00-install-knotd/files"
PLUGINS_FILES_DIR="$STAGE_DIR/01-install-plugins/files"

# ---- Pre-flight ------------------------------------------------------------

if [[ "$(uname -s)" != "Linux" ]]; then
    echo "fatal: this script only runs on Linux. Use WSL2 on Windows (image/build.ps1)." >&2
    exit 1
fi

if [[ "$EUID" -ne 0 ]]; then
    echo "fatal: must run as root (losetup/mount/chroot). Try: sudo bash $0" >&2
    exit 1
fi

if [[ ! -x "$QEMU_BIN" ]]; then
    echo "fatal: $QEMU_BIN not found. Install qemu-user-static." >&2
    exit 1
fi

# Run user-level steps (build UI, go build) as the original user so
# their caches go to /home/<user>, not /root.
RUN_AS_USER="${SUDO_USER:-root}"
run_user() {
    if [[ "$RUN_AS_USER" == "root" ]]; then
        "$@"
    else
        sudo -u "$RUN_AS_USER" -H -E "$@"
    fi
}

# ---- 1. Build UI ----------------------------------------------------------

echo "==> [1/7] Building UI"
run_user bash "$ROOT/scripts/build-ui.sh"

# ---- 2. Cross-compile knotd + knotctl ------------------------------------

echo "==> [2/7] Cross-compiling knotd + knotctl (linux/arm64)"
run_user env GOOS=linux GOARCH=arm64 \
    go build -trimpath -ldflags "-s -w -X main.Version=$VERSION" \
    -o "$FILES_DIR/knotd" "$ROOT/core/cmd/knotd"
run_user env GOOS=linux GOARCH=arm64 \
    go build -trimpath -ldflags "-s -w -X main.Version=$VERSION" \
    -o "$FILES_DIR/knotctl" "$ROOT/cli/cmd/knotctl"
chmod +x "$FILES_DIR/knotd" "$FILES_DIR/knotctl"
echo "    knotd:    $(stat -c '%s' "$FILES_DIR/knotd")    bytes"
echo "    knotctl:  $(stat -c '%s' "$FILES_DIR/knotctl")  bytes"

# ---- 3. Stage bundled plugins ---------------------------------------------

echo "==> [3/7] Staging bundled plugins"
mkdir -p "$PLUGINS_FILES_DIR"
rm -rf "${PLUGINS_FILES_DIR:?}"/*
shopt -s nullglob
for p in "$ROOT/plugins"/*/; do
    name="$(basename "$p")"
    [[ "$name" == "README.md" ]] && continue
    if [[ -f "$p/plugin.yaml" ]]; then
        cp -r "$p" "$PLUGINS_FILES_DIR/"
        echo "    + $name"
    fi
done
shopt -u nullglob

# ---- 4. Download Pi OS Lite (cached) --------------------------------------

mkdir -p "$CACHE_DIR" "$WORK_DIR" "$DEPLOY_DIR"
if [[ ! -f "$LITE_IMG_XZ" ]] || [[ ! -s "$LITE_IMG_XZ" ]]; then
    echo "==> [4/7] Downloading Raspberry Pi OS Lite arm64 (~500 MB)"
    # Download as root: build.sh creates image/cache/ as root, so a
    # user-level curl cannot write into it. Letting root own the
    # cache file is fine - everything in image/cache, image/work,
    # image/deploy is cleaned by `image/build.ps1 -Clean` which runs
    # as root, and humans rarely need to touch them by hand.
    curl -fL --progress-bar -o "$LITE_IMG_XZ" "$LITE_URL"
else
    echo "==> [4/7] Using cached $LITE_IMG_XZ ($(stat -c '%s' "$LITE_IMG_XZ") bytes)"
fi

# ---- 5. Decompress and loop-attach ----------------------------------------

echo "==> [5/7] Decompressing image"
xz -dc "$LITE_IMG_XZ" > "$WORK_IMG"
echo "    image size: $(stat -c '%s' "$WORK_IMG") bytes"

cleanup() {
    set +e
    if mountpoint -q "$MOUNT_DIR/proc"            2>/dev/null; then umount "$MOUNT_DIR/proc";            fi
    if mountpoint -q "$MOUNT_DIR/sys"             2>/dev/null; then umount "$MOUNT_DIR/sys";             fi
    if mountpoint -q "$MOUNT_DIR/dev/pts"         2>/dev/null; then umount "$MOUNT_DIR/dev/pts";         fi
    if mountpoint -q "$MOUNT_DIR/dev"             2>/dev/null; then umount "$MOUNT_DIR/dev";             fi
    if mountpoint -q "$MOUNT_DIR/boot/firmware"   2>/dev/null; then umount "$MOUNT_DIR/boot/firmware";   fi
    if mountpoint -q "$MOUNT_DIR"                 2>/dev/null; then umount "$MOUNT_DIR";                 fi
    if [[ -n "${LOOP:-}" ]]; then losetup -d "$LOOP" 2>/dev/null || true; fi
    set -e
}
trap cleanup EXIT

LOOP="$(losetup --find --show --partscan "$WORK_IMG")"
echo "    loop: $LOOP"
# Some kernels need a moment for partition nodes to appear.
udevadm settle 2>/dev/null || sleep 1
BOOT_PART="${LOOP}p1"
ROOT_PART="${LOOP}p2"

mkdir -p "$MOUNT_DIR"
mount "$ROOT_PART" "$MOUNT_DIR"
mkdir -p "$MOUNT_DIR/boot/firmware"
mount "$BOOT_PART" "$MOUNT_DIR/boot/firmware"

# Bind-mount kernel virtual filesystems so apt's post-install scripts work.
mount --bind /dev      "$MOUNT_DIR/dev"
mount --bind /dev/pts  "$MOUNT_DIR/dev/pts" 2>/dev/null || true
mount -t proc proc     "$MOUNT_DIR/proc"
mount -t sysfs sys     "$MOUNT_DIR/sys"

# qemu-aarch64-static for arm64 binary execution under chroot.
cp "$QEMU_BIN" "$MOUNT_DIR/usr/bin/qemu-aarch64-static"

# DNS for apt inside chroot.
cp /etc/resolv.conf "$MOUNT_DIR/etc/resolv.conf.knotos-bak" 2>/dev/null || true
cp /etc/resolv.conf "$MOUNT_DIR/etc/resolv.conf"

# Pi OS Lite ships with /etc/ld.so.preload pointing at libcofi which
# does not load under qemu emulation. Move it aside for the chroot
# session, restore on cleanup.
if [[ -f "$MOUNT_DIR/etc/ld.so.preload" ]]; then
    mv "$MOUNT_DIR/etc/ld.so.preload" "$MOUNT_DIR/etc/ld.so.preload.knotos-bak"
fi

run_in_chroot() {
    chroot "$MOUNT_DIR" /usr/bin/qemu-aarch64-static /bin/bash -lc "$1"
}

# ---- 6. Install our extras + inject knotd --------------------------------

echo "==> [6/7] Installing dependencies in chroot (this is the slow step, ~3-5 min)"
run_in_chroot "
    set -e
    export DEBIAN_FRONTEND=noninteractive
    apt-get update
    apt-get install -y --no-install-recommends \
        hostapd \
        dnsmasq \
        nftables \
        iw \
        wireless-regdb \
        isc-dhcp-client \
        avahi-daemon
    apt-get clean
    rm -rf /var/lib/apt/lists/*
"

echo "    copying KnotOS files"
install -m 755 -D "$FILES_DIR/knotd"               "$MOUNT_DIR/usr/local/bin/knotd"
install -m 755 -D "$FILES_DIR/knotctl"             "$MOUNT_DIR/usr/local/bin/knotctl"
install -m 644 -D "$FILES_DIR/knotd.service"       "$MOUNT_DIR/etc/systemd/system/knotd.service"
install -m 600 -D "$FILES_DIR/default-config.yaml" "$MOUNT_DIR/etc/knot/config.yaml"
chmod 700 "$MOUNT_DIR/etc/knot"

# Recovery: write diagnostics to /boot/firmware/knot-startup.log on
# every boot. The user can read this from Windows Explorer (FAT
# partition) when there is no working network/serial.
RECOVERY_DIR="$STAGE_DIR/02-recovery/files"
install -m 755 -D "$RECOVERY_DIR/knot-bootlog"         "$MOUNT_DIR/usr/local/bin/knot-bootlog"
install -m 644 -D "$RECOVERY_DIR/knot-bootlog.service" "$MOUNT_DIR/etc/systemd/system/knot-bootlog.service"

echo "    copying plugins"
mkdir -p "$MOUNT_DIR/usr/lib/knot/plugins"
shopt -s nullglob
for p in "$PLUGINS_FILES_DIR"/*/; do
    name="$(basename "$p")"
    cp -r "$p" "$MOUNT_DIR/usr/lib/knot/plugins/"
    echo "      + $name"
done
shopt -u nullglob
chmod -R u=rwX,go=rX "$MOUNT_DIR/usr/lib/knot/plugins" 2>/dev/null || true

# tmpfiles.d entry so /run/knot exists with the right mode after boot.
cat > "$MOUNT_DIR/etc/tmpfiles.d/knot.conf" <<'EOF'
d /run/knot 0755 root root -
EOF

# Hostname.
echo "knot" > "$MOUNT_DIR/etc/hostname"
sed -i 's/127\.0\.1\.1.*/127.0.1.1\tknot/' "$MOUNT_DIR/etc/hosts" 2>/dev/null || \
    echo "127.0.1.1	knot" >> "$MOUNT_DIR/etc/hosts"

# Hand the network stack to knotd. Mask everything that races for wlan0.
echo "    configuring services"
run_in_chroot "
    set -e
    systemctl mask NetworkManager.service           2>/dev/null || true
    systemctl disable NetworkManager.service        2>/dev/null || true
    systemctl mask NetworkManager-wait-online.service 2>/dev/null || true
    systemctl mask hostapd.service                  2>/dev/null || true
    systemctl mask dnsmasq.service                  2>/dev/null || true
    systemctl disable wpa_supplicant.service        2>/dev/null || true
    systemctl disable dhcpcd.service                2>/dev/null || true
    systemctl mask dhcpcd.service                   2>/dev/null || true
    systemctl enable knotd.service
    systemctl enable knot-bootlog.service
    systemctl enable ssh.service
"

# Skip Raspberry Pi OS first-boot user setup (we ship a working
# 'knot' user from the Lite image). userconf.txt sets the initial
# username:password so the wizard does not block on it.
echo 'knot:$(openssl passwd -6 knot)' > "$MOUNT_DIR/boot/firmware/userconf.txt"

# ---- 7. Tear down chroot, compress, deploy --------------------------------

echo "==> [7/7] Cleaning up and compressing image"

# Restore preload, drop the qemu binary so the rootfs is bootable arm64-only.
if [[ -f "$MOUNT_DIR/etc/ld.so.preload.knotos-bak" ]]; then
    mv "$MOUNT_DIR/etc/ld.so.preload.knotos-bak" "$MOUNT_DIR/etc/ld.so.preload"
fi
rm -f "$MOUNT_DIR/usr/bin/qemu-aarch64-static"
if [[ -f "$MOUNT_DIR/etc/resolv.conf.knotos-bak" ]]; then
    mv "$MOUNT_DIR/etc/resolv.conf.knotos-bak" "$MOUNT_DIR/etc/resolv.conf"
else
    rm -f "$MOUNT_DIR/etc/resolv.conf"
fi

cleanup
trap - EXIT

TS="$(date +%Y%m%d-%H%M)"
OUT="$DEPLOY_DIR/${TS}-KnotOS-zero2w-${VERSION}.img.xz"
echo "    compressing -> $OUT (this takes a few minutes)"
xz -T0 -c "$WORK_IMG" > "$OUT"

# Drop intermediate work to free disk; keep the cache.
rm -f "$WORK_IMG"

echo
echo "==> Done."
echo "    Image: $OUT"
echo "    Size:  $(stat -c '%s' "$OUT") bytes"
echo
echo "Flash with Raspberry Pi Imager: Choose Device -> Pi Zero 2 W,"
echo "                                Choose OS -> Use custom -> select the .img.xz."
