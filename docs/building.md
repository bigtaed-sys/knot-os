# Building KnotOS

Two separate flows depending on what you want to do.

## Local development of the daemon and UI

For coding on `knotd`, `knotctl`, or the UI without flashing hardware. Works on Linux, macOS, and Windows.

Requires Go 1.22+ and Node 18+.

```bash
# Build everything
bash scripts/build-ui.sh
go build -o ./dist/knotd ./core/cmd/knotd
go build -o ./dist/knotctl ./cli/cmd/knotctl

# Run the daemon in dev mode (mock network backend, no root needed)
./dist/knotd -dev -listen :8080 -config ./tmp/config.yaml

# Open the UI
# http://localhost:8080
```

In dev mode, `knotd` uses a `MockBackend` that records calls instead of touching the network stack. The wizard flow, login, dashboard, and config persistence all work; Wi-Fi scanning returns four hardcoded fake networks.

For a faster UI iteration loop, run the SvelteKit dev server separately:

```bash
# Terminal 1
./dist/knotd -dev -listen :8080 -config ./tmp/config.yaml

# Terminal 2
cd ui && npm run dev
# Vite proxies /api/* to localhost:8080
# Open the URL Vite prints (usually http://localhost:5173)
```

Tests:

```bash
go test ./core/...
cd ui && npm run check
```

## Building a flashable image (Raspberry Pi Zero 2W)

Produces `image/deploy/<timestamp>-KnotOS-zero2w-<version>.img.xz`.

### Requirements

- A Linux host. **On Windows, use WSL2 with Ubuntu 22.04+** — Docker Desktop's bundled distro won't work because pi-gen needs to chroot and loop-mount.
- Root (`sudo`).
- These apt packages:

  ```bash
  sudo apt update
  sudo apt install -y \
    quilt parted qemu-user-static debootstrap zerofree \
    zip dosfstools libcap2-bin grep rsync xz-utils file \
    git curl bc binfmt-support qemu-utils kpartx pigz arch-test
  ```

- Go 1.22+ and Node 18+ on `PATH` (these run as your normal user, not root).

### One-shot build

```bash
sudo bash image/build.sh
```

Takes 30–60 minutes on a typical laptop. The output is `image/deploy/<timestamp>-KnotOS-zero2w-<version>.img.xz`.

### Flashing

Use **Raspberry Pi Imager** (recommended) or any tool that can write `.img.xz` to an SD card.

In Raspberry Pi Imager:
1. *Choose Device* → Raspberry Pi Zero 2 W
2. *Choose OS* → *Use custom* → select the `.img.xz`
3. *Choose Storage* → your SD card
4. *Write*

Skip the OS-customization dialog (knotd handles its own onboarding).

### First boot

1. Insert the SD card and apply power. First boot takes ~60 seconds (filesystem expansion, knotd start).
2. From a phone or laptop, look for an open Wi-Fi called `KnotOS-setup-XXXX` (XXXX = last 4 hex of the device's MAC) and connect.
3. Open any URL in your browser → captive portal redirects you to `http://192.168.42.1`.
4. Walk through the wizard: device name, admin password, upstream Wi-Fi, broadcast Wi-Fi.
5. After "Apply", the setup network disappears and your chosen broadcast SSID comes up. Reconnect to it and find the dashboard at `http://<device-name>.local`.

### Recovery / SSH access

The image ships with SSH enabled and a fallback user `knot` / password `knot`. Change this immediately on a real deployment.

If the Wi-Fi setup ever leaves you locked out, plug a USB Ethernet adapter into the Pi (planned for v0.2; for v0.1 the only out-of-band path is removing the SD card and editing `/etc/knot/config.yaml` directly).

## Pinning pi-gen to a specific revision

`image/build.sh` clones `RPi-Distro/pi-gen` at `master` by default. To pin:

```bash
sudo PIGEN_REF=2024-11-19-raspios-bookworm bash image/build.sh
```

The pinned commit/tag must support arm64 (most recent ones do).

## Build cache

`image/pi-gen/` is checked out in place and reused across runs. Subsequent builds skip debootstrap if `pi-gen/work/` is intact, dropping the typical rebuild to 5–15 minutes. To force a clean rebuild:

```bash
sudo rm -rf image/pi-gen/work image/deploy
sudo bash image/build.sh
```
