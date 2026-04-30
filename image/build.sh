#!/usr/bin/env bash
# Build a flashable KnotOS image for Raspberry Pi Zero 2W (linux/arm64).
#
# Usage:  sudo bash image/build.sh
#
# Requirements:
#   - Linux host (WSL2 Ubuntu 22.04+ works; Docker Desktop's
#     own distro does not — install Ubuntu).
#   - Go 1.22+ and Node 18+ on PATH.
#   - apt packages: quilt parted qemu-user-static debootstrap zerofree
#     zip dosfstools libcap2-bin grep rsync xz-utils file git curl bc
#     binfmt-support qemu-utils kpartx pigz arch-test
#   - Run as root (pi-gen needs to chroot, debootstrap, and loop-mount).
#
# Output:
#   image/deploy/<timestamp>-KnotOS-zero2w.img.xz

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

VERSION="${VERSION:-0.1.0-dev}"
PIGEN_REF="${PIGEN_REF:-master}"            # pin via env to a tag/commit later
PIGEN_REPO="${PIGEN_REPO:-https://github.com/RPi-Distro/pi-gen.git}"
PIGEN_DIR="$ROOT/image/pi-gen"
DEPLOY_DIR="$ROOT/image/deploy"
STAGE_DIR="$ROOT/image/stage-knot"
FILES_DIR="$STAGE_DIR/00-install-knotd/files"

# ---- Pre-flight ------------------------------------------------------------

if [[ "$(uname -s)" != "Linux" ]]; then
    echo "fatal: pi-gen only runs on Linux. Use WSL2 on Windows." >&2
    exit 1
fi

if [[ "$EUID" -ne 0 ]]; then
    echo "fatal: must run as root (pi-gen needs to chroot/mount). Try: sudo bash $0" >&2
    exit 1
fi

# Run Go and npm as the original (non-root) user where possible to
# avoid littering home dirs with root-owned caches. SUDO_USER is set
# when invoked via sudo; falls back to root if directly logged in.
#
# `sudo -E` alone is not enough — it preserves HOME=/root from the
# parent environment, and then npm/go (running as the target user)
# blow up trying to read root-owned $HOME/.npm and $HOME/.cache. We
# need -H so HOME is reset to the target user's home, while keeping
# -E so PATH / GOPATH / etc. survive.
RUN_AS_USER="${SUDO_USER:-root}"
run_user() {
    if [[ "$RUN_AS_USER" == "root" ]]; then
        "$@"
    else
        sudo -u "$RUN_AS_USER" -H -E "$@"
    fi
}

# ---- 1. Cross-compile knotd + knotctl --------------------------------------

echo "==> [1/5] Building UI"
run_user bash "$ROOT/scripts/build-ui.sh"

echo "==> [2/5] Cross-compiling knotd + knotctl (linux/arm64)"
run_user env GOOS=linux GOARCH=arm64 \
    go build -trimpath -ldflags "-s -w -X main.Version=$VERSION" \
    -o "$FILES_DIR/knotd" "$ROOT/core/cmd/knotd"
run_user env GOOS=linux GOARCH=arm64 \
    go build -trimpath -ldflags "-s -w -X main.Version=$VERSION" \
    -o "$FILES_DIR/knotctl" "$ROOT/cli/cmd/knotctl"
chmod +x "$FILES_DIR/knotd" "$FILES_DIR/knotctl"
echo "    knotd:    $(stat -c '%s' "$FILES_DIR/knotd")    bytes"
echo "    knotctl:  $(stat -c '%s' "$FILES_DIR/knotctl")  bytes"

# Stage bundled plugins so the image has them on first boot.
PLUGIN_FILES_DIR="$STAGE_DIR/01-install-plugins/files"
echo "    staging plugins from $ROOT/plugins"
rm -rf "${PLUGIN_FILES_DIR:?}"/*
shopt -s nullglob
for p in "$ROOT/plugins"/*/; do
    name="$(basename "$p")"
    [[ "$name" == "README.md" ]] && continue
    if [[ -f "$p/plugin.yaml" ]]; then
        cp -r "$p" "$PLUGIN_FILES_DIR/"
        echo "      + $name"
    fi
done
shopt -u nullglob

# ---- 3. Pull pi-gen --------------------------------------------------------

echo "==> [3/5] Preparing pi-gen ($PIGEN_REF)"
# pi-gen runs as root and uses `git` against this directory internally
# (e.g. for revision stamping). If we clone as the unprivileged user,
# git's "dubious ownership" check rejects every later operation. Clone
# as root so the directory matches the privilege level pi-gen runs at.
if [[ ! -d "$PIGEN_DIR/.git" ]]; then
    git clone --depth=1 --branch="$PIGEN_REF" "$PIGEN_REPO" "$PIGEN_DIR"
else
    git -C "$PIGEN_DIR" fetch origin "$PIGEN_REF"
    git -C "$PIGEN_DIR" checkout -q "$PIGEN_REF"
    git -C "$PIGEN_DIR" reset --hard "origin/$PIGEN_REF"
fi

# Symlink (not copy) our stage into pi-gen so source stays canonical.
ln -sfn "$STAGE_DIR" "$PIGEN_DIR/stage-knot"

# Skip the heavy desktop stages and the secondary export-stages; we only
# need stages 0-2 (Lite base) before our stage runs.
for s in stage3 stage4 stage5; do
    : > "$PIGEN_DIR/$s/SKIP" 2>/dev/null || true
    : > "$PIGEN_DIR/$s/SKIP_IMAGES" 2>/dev/null || true
done
# We export only our stage.
: > "$PIGEN_DIR/stage2/SKIP_IMAGES"

# ---- 4. Write pi-gen config ------------------------------------------------

echo "==> [4/5] Writing pi-gen config"
cat > "$PIGEN_DIR/config" <<EOF
IMG_NAME='KnotOS'
RELEASE='bookworm'
TARGET_HOSTNAME='knot'
ENABLE_SSH=1
LOCALE_DEFAULT='en_US.UTF-8'
KEYBOARD_KEYMAP='us'
KEYBOARD_LAYOUT='English (US)'
TIMEZONE_DEFAULT='Etc/UTC'
FIRST_USER_NAME='knot'
FIRST_USER_PASS='knot'
DISABLE_FIRST_BOOT_USER_RENAME=1

# arm64 build (Pi Zero 2W is ARMv8 64-bit). Pi-gen's recent versions
# accept --arch via env; older versions may need master branch.
PI_GEN='pi-gen'
ARCH='arm64'
APT_PROXY=
EOF

# ---- 5. Run pi-gen ---------------------------------------------------------

echo "==> [5/5] Running pi-gen build (this takes 30-60 minutes)"
mkdir -p "$DEPLOY_DIR"

cd "$PIGEN_DIR"
./build.sh

# Move the produced image out of pi-gen/deploy into ours, with a name
# we control.
TS="$(date +%Y%m%d-%H%M)"
SRC_IMG="$(ls -1t "$PIGEN_DIR/deploy"/*.img.xz 2>/dev/null | head -1 || true)"
if [[ -z "$SRC_IMG" ]]; then
    echo "fatal: pi-gen produced no .img.xz in $PIGEN_DIR/deploy" >&2
    exit 1
fi
DST_IMG="$DEPLOY_DIR/${TS}-KnotOS-zero2w-${VERSION}.img.xz"
mv "$SRC_IMG" "$DST_IMG"

echo
echo "==> Done."
echo "    Image: $DST_IMG"
echo "    Size:  $(stat -c '%s' "$DST_IMG") bytes"
echo
echo "Flash with Raspberry Pi Imager → Choose OS → Use custom → select the .img.xz."
