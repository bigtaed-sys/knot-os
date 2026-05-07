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
# BASE_IMG is the cached "Pi OS Lite + apt-installed deps + service
# masks + serial console + recovery scripts", produced once on first
# build and reused thereafter. The slow apt-install-via-qemu step
# only re-runs when this file is missing (e.g. after -Clean, or if
# the user manually deletes it to refresh package versions).
BASE_IMG="$CACHE_DIR/knotos-base.img"
WORK_DIR="$ROOT/image/work"
WORK_IMG="$WORK_DIR/knotos.img"
MOUNT_DIR="$WORK_DIR/mnt"
DEPLOY_DIR="$ROOT/image/deploy"
STAGE_DIR="$ROOT/image/stage-knot"
FILES_DIR="$STAGE_DIR/00-install-knotd/files"
PLUGINS_FILES_DIR="$STAGE_DIR/01-install-plugins/files"
SINGBOX_FILES_DIR="$STAGE_DIR/03-singbox/files"

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

# ---- 3b. Fetch + verify sing-box binary -----------------------------------
#
# Version is pinned in core/internal/singbox/singbox.go (const Version)
# to keep the source-of-truth in one place. We resolve it here via a
# tiny `go run` so the shell script doesn't need a regex parse of Go.
# The binary lands in stage-knot/03-singbox/files/sing-box; the substage
# script then `install`s it into the rootfs.

echo "==> [3b/7] Staging sing-box binary"
mkdir -p "$SINGBOX_FILES_DIR" "$CACHE_DIR"

SINGBOX_VERSION="$(run_user env GOFLAGS=-mod=mod \
    go run "$ROOT/core/cmd/print-singbox-version" 2>/dev/null || true)"
if [[ -z "$SINGBOX_VERSION" ]]; then
    # Fallback: grep the constant directly. Loose match — if the
    # printer cmd disappears we still produce a version.
    SINGBOX_VERSION="$(grep -oE 'Version = "[0-9]+\.[0-9]+\.[0-9]+[^"]*"' \
        "$ROOT/core/internal/singbox/singbox.go" | head -1 | \
        sed -E 's/.*"([^"]+)"/\1/')"
fi
if [[ -z "$SINGBOX_VERSION" ]]; then
    echo "fatal: could not determine sing-box version from singbox.go" >&2
    exit 1
fi

# We pin SHA-256 alongside the version. When bumping, update both.
# Source: https://github.com/SagerNet/sing-box/releases (linux-arm64).
SINGBOX_TGZ_NAME="sing-box-${SINGBOX_VERSION}-linux-arm64.tar.gz"
SINGBOX_URL="https://github.com/SagerNet/sing-box/releases/download/v${SINGBOX_VERSION}/${SINGBOX_TGZ_NAME}"
SINGBOX_CACHED="$CACHE_DIR/$SINGBOX_TGZ_NAME"
SINGBOX_BIN="$SINGBOX_FILES_DIR/sing-box"

# Pinned SHA-256 of the upstream tar.gz. Empty = first-build mode:
# we record the hash on stdout for the developer to paste back.
case "$SINGBOX_VERSION" in
    1.10.7)  SINGBOX_SHA256="" ;;  # TODO: pin once verified manually
    *)       SINGBOX_SHA256="" ;;
esac

if [[ ! -f "$SINGBOX_CACHED" ]]; then
    echo "    downloading $SINGBOX_URL"
    curl -fL --progress-bar -o "$SINGBOX_CACHED" "$SINGBOX_URL"
fi

ACTUAL_SHA="$(sha256sum "$SINGBOX_CACHED" | awk '{print $1}')"
if [[ -n "$SINGBOX_SHA256" ]]; then
    if [[ "$ACTUAL_SHA" != "$SINGBOX_SHA256" ]]; then
        echo "fatal: sing-box ${SINGBOX_VERSION} sha256 mismatch" >&2
        echo "       expected: $SINGBOX_SHA256" >&2
        echo "       actual:   $ACTUAL_SHA" >&2
        exit 1
    fi
else
    echo "    note: no pinned sha256 for ${SINGBOX_VERSION}; recording $ACTUAL_SHA"
fi

# Extract just the binary out of the tarball.
TMP_EXTRACT="$(mktemp -d)"
tar -xzf "$SINGBOX_CACHED" -C "$TMP_EXTRACT"
EXTRACTED_BIN="$(find "$TMP_EXTRACT" -type f -name 'sing-box' -perm -u+x | head -1)"
if [[ -z "$EXTRACTED_BIN" ]]; then
    echo "fatal: sing-box binary not found inside $SINGBOX_TGZ_NAME" >&2
    rm -rf "$TMP_EXTRACT"
    exit 1
fi
install -m 755 "$EXTRACTED_BIN" "$SINGBOX_BIN"
rm -rf "$TMP_EXTRACT"
echo "    sing-box: $(stat -c '%s' "$SINGBOX_BIN") bytes (v${SINGBOX_VERSION})"

# ---- 4. Download Pi OS Lite (cached) --------------------------------------

mkdir -p "$CACHE_DIR" "$WORK_DIR" "$DEPLOY_DIR"

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

# Helper: loop-mount $1 at $MOUNT_DIR with /proc /sys /dev bind mounts
# and qemu-aarch64-static dropped in for chroot-time arm64 binary
# execution. Sets $LOOP for the cleanup trap to tear down.
mount_image() {
    local img="$1"
    LOOP="$(losetup --find --show --partscan "$img")"
    udevadm settle 2>/dev/null || sleep 1
    BOOT_PART="${LOOP}p1"
    ROOT_PART="${LOOP}p2"
    mkdir -p "$MOUNT_DIR"
    mount "$ROOT_PART" "$MOUNT_DIR"
    mkdir -p "$MOUNT_DIR/boot/firmware"
    mount "$BOOT_PART" "$MOUNT_DIR/boot/firmware"
    mount --bind /dev      "$MOUNT_DIR/dev"
    mount --bind /dev/pts  "$MOUNT_DIR/dev/pts" 2>/dev/null || true
    mount -t proc proc     "$MOUNT_DIR/proc"
    mount -t sysfs sys     "$MOUNT_DIR/sys"
    cp "$QEMU_BIN" "$MOUNT_DIR/usr/bin/qemu-aarch64-static"
    cp /etc/resolv.conf "$MOUNT_DIR/etc/resolv.conf.knotos-bak" 2>/dev/null || true
    cp /etc/resolv.conf "$MOUNT_DIR/etc/resolv.conf"
    if [[ -f "$MOUNT_DIR/etc/ld.so.preload" ]]; then
        mv "$MOUNT_DIR/etc/ld.so.preload" "$MOUNT_DIR/etc/ld.so.preload.knotos-bak"
    fi
}

# Helper: tear down chroot artifacts so the image is bootable arm64-only.
unmount_image() {
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
    LOOP=""
}

run_in_chroot() {
    chroot "$MOUNT_DIR" /usr/bin/qemu-aarch64-static /bin/bash -lc "$1"
}

# ---- 5. Build the base image once (slow path) -----------------------------
#
# The base image is Pi OS Lite + our apt deps + service masks + serial
# console + recovery scripts. Everything that doesn't change between
# knotd builds. Cached at $BASE_IMG and reused on subsequent runs.

if [[ ! -f "$BASE_IMG" ]] || [[ ! -s "$BASE_IMG" ]]; then
    if [[ ! -f "$LITE_IMG_XZ" ]] || [[ ! -s "$LITE_IMG_XZ" ]]; then
        echo "==> [4/7] Downloading Raspberry Pi OS Lite arm64 (~500 MB)"
        curl -fL --progress-bar -o "$LITE_IMG_XZ" "$LITE_URL"
    else
        echo "==> [4/7] Using cached $LITE_IMG_XZ"
    fi

    echo "==> [5/7] Building base image (one-time, ~5 min via qemu chroot)"
    echo "    decompressing Lite -> $BASE_IMG"
    xz -dc "$LITE_IMG_XZ" > "$BASE_IMG"

    mount_image "$BASE_IMG"

    echo "    apt install (this is the slow step)"
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
            avahi-daemon \
            wireguard-tools
        apt-get clean
        rm -rf /var/lib/apt/lists/*
    "

    # Service masks (race-with-knotd avoidance). These are part of the
    # base because they don't change between knotd builds.
    echo "    masking competing services"
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
        systemctl enable ssh.service
        systemctl enable getty@ttyGS0.service           2>/dev/null || true
        systemctl enable serial-getty@ttyGS0.service    2>/dev/null || true
    "

    # Default hostname; overwritten if user picks a name in the wizard.
    echo "knot" > "$MOUNT_DIR/etc/hostname"
    sed -i 's/127\.0\.1\.1.*/127.0.1.1\tknot/' "$MOUNT_DIR/etc/hosts" 2>/dev/null || \
        echo "127.0.1.1	knot" >> "$MOUNT_DIR/etc/hosts"

    # First-boot user setup so Pi OS Lite's userconf-pi service creates
    # a 'knot' user with password 'knot' on first boot - otherwise the
    # device boots into "no user configured" state and there is no way
    # to log in at the console.
    #
    # userconf.txt format: username:hashed_password (one line).
    # Generate the hash inside the chroot so it uses the target's
    # libcrypt (mostly cosmetic - openssl passwd -6 produces SHA-512
    # crypt that any modern glibc accepts).
    KNOT_PW_HASH="$(openssl passwd -6 knot)"
    echo "knot:${KNOT_PW_HASH}" > "$MOUNT_DIR/boot/firmware/userconf.txt"

    # Boot config: serial console (UART0 + USB-OTG g_serial).
    CONFIG_TXT="$MOUNT_DIR/boot/firmware/config.txt"
    if ! grep -q '^enable_uart=1' "$CONFIG_TXT" 2>/dev/null; then
        {
            echo
            echo "# KnotOS: serial console - both UART0 GPIO and USB-OTG gadget"
            echo "enable_uart=1"
            echo "dtoverlay=disable-bt"
            echo "dtoverlay=dwc2"
        } >> "$CONFIG_TXT"
    fi

    CMDLINE_TXT="$MOUNT_DIR/boot/firmware/cmdline.txt"
    if ! grep -q 'console=serial0' "$CMDLINE_TXT" 2>/dev/null; then
        sed -i '1s/^/console=serial0,115200 /' "$CMDLINE_TXT"
    fi
    if ! grep -q 'modules-load=dwc2,g_serial' "$CMDLINE_TXT" 2>/dev/null; then
        sed -i 's/\brootwait\b/rootwait modules-load=dwc2,g_serial/' "$CMDLINE_TXT"
    fi

    unmount_image
    echo "    base image cached at $BASE_IMG ($(stat -c '%s' "$BASE_IMG") bytes)"
else
    echo "==> [4/7] Using cached base image $BASE_IMG"
    echo "==> [5/7] (skipping base build - delete $BASE_IMG to refresh)"
fi

# ---- 6. Fast path: copy base, inject knotd files --------------------------

echo "==> [6/7] Injecting knotd into image (fast path)"
echo "    cp $BASE_IMG -> $WORK_IMG"
cp "$BASE_IMG" "$WORK_IMG"

mount_image "$WORK_IMG"

echo "    copying KnotOS files"
install -m 755 -D "$FILES_DIR/knotd"               "$MOUNT_DIR/usr/local/bin/knotd"
install -m 755 -D "$FILES_DIR/knotctl"             "$MOUNT_DIR/usr/local/bin/knotctl"
install -m 644 -D "$FILES_DIR/knotd.service"       "$MOUNT_DIR/etc/systemd/system/knotd.service"
install -m 600 -D "$FILES_DIR/default-config.yaml" "$MOUNT_DIR/etc/knot/config.yaml"
chmod 700 "$MOUNT_DIR/etc/knot"

# sing-box VPN engine. Started lazily by knotd when a profile with
# real outbounds is applied; otherwise sits idle on disk.
install -m 755 -D "$SINGBOX_FILES_DIR/sing-box" "$MOUNT_DIR/usr/local/bin/sing-box"

# Recovery / ops scripts:
#   knot-bootlog       — writes startup diagnostics to /boot/firmware
#                        on every boot (FAT, readable from Windows).
#   knot-update-receive — receives a new knotd binary over serial,
#                        atomically installs it, restarts knotd.
#                        Counterpart to scripts/update-knot.ps1 -Serial.
RECOVERY_DIR="$STAGE_DIR/02-recovery/files"
install -m 755 -D "$RECOVERY_DIR/knot-bootlog"         "$MOUNT_DIR/usr/local/bin/knot-bootlog"
install -m 644 -D "$RECOVERY_DIR/knot-bootlog.service" "$MOUNT_DIR/etc/systemd/system/knot-bootlog.service"
install -m 755 -D "$RECOVERY_DIR/knot-update-receive"  "$MOUNT_DIR/usr/local/bin/knot-update-receive"

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

# Enable knot-specific services (the rest of the system services are
# already configured in the cached base image).
run_in_chroot "
    set -e
    systemctl enable knotd.service
    systemctl enable knot-bootlog.service
"

# A README on the FAT partition so the user can read the recovery
# instructions from Windows without booting the Pi. Written every
# build so doc updates show up without rebuilding the base image.
cat > "$MOUNT_DIR/boot/firmware/SERIAL-CONSOLE.txt" <<'EOF'
KnotOS — Serial console
=======================

Two ways to get a console without a working network. Use whichever is
easier for your situation. Both give a login prompt; user "knot",
password "knot" (change with passwd after first login).

------------------------------------------------------------
1. USB OTG  (no extra hardware — recommended)
------------------------------------------------------------

The Pi Zero 2W has TWO micro-USB ports. The one closer to the HDMI
port is the data port (sometimes labelled "USB"); the other is power-only.

Plug a single micro-USB cable from the Pi's data port into your computer.
The Pi powers itself from that cable AND appears as a USB serial device:

  - Windows:  "USB Serial Device (COMx)" in Device Manager.
              Open in PuTTY / TeraTerm at 115200 8N1.
  - macOS:    /dev/cu.usbmodemXXXX  — open with screen, e.g.
              screen /dev/cu.usbmodem* 115200
  - Linux:    /dev/ttyACM0 — screen /dev/ttyACM0 115200

If nothing appears, wait 30 seconds — the USB gadget needs the Pi to
finish booting before it enumerates.

------------------------------------------------------------
2. GPIO UART  (needs a 3.3V USB-TTL adapter)
------------------------------------------------------------

Settings: 115200 8N1, no flow control.

Pi Zero 2W GPIO pinout (physical pin numbers, pin 1 marked with a
small square on the board):

    Pin  6  GND   <->  USB-TTL GND
    Pin  8  TXD   <->  USB-TTL RX
    Pin 10  RXD   <->  USB-TTL TX

USE A 3.3V USB-TTL ADAPTER. A 5V adapter will damage the Pi.
Do NOT connect the adapter's VCC — the Pi powers itself.

------------------------------------------------------------
Once you are in:
------------------------------------------------------------

    sudo systemctl status knotd
    sudo journalctl -u knotd -b
    sudo cat /boot/firmware/knot-startup.log

The boot diagnostics file is also written to /boot/firmware on every
boot — readable from Windows just by popping the SD into a card
reader, no console needed.
EOF

# ---- 7. Tear down chroot, compress, deploy --------------------------------

echo "==> [7/7] Cleaning up and compressing image"
unmount_image
trap - EXIT

# Wipe any old .img.xz from previous builds so the deploy/ dir only
# ever has the most recent artifact - keeps the Windows-side rsync
# back-copy from accumulating stale images.
rm -f "$DEPLOY_DIR"/*.img.xz

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
