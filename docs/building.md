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
    qemu-user-static binfmt-support \
    parted e2fsprogs dosfstools xz-utils \
    rsync curl ca-certificates openssl
  ```

- Go 1.22+ and Node 18+ on `PATH` (these run as your normal user, not root).

### One-shot build (Linux / macOS)

```bash
sudo bash image/build.sh
```

Takes about 5–10 minutes on a typical laptop (~2 min download for the Pi OS Lite base on first run, then chroot-install of our extras + image compression). The output is `image/deploy/<timestamp>-KnotOS-zero2w-<version>.img.xz`.

The script downloads the latest official Raspberry Pi OS Lite arm64 image, mounts it, installs `hostapd`, `dnsmasq`, `nftables`, `iw`, `isc-dhcp-client`, and `avahi-daemon` into the chroot, drops in `knotd` + `knotctl` + the systemd unit + the seed config + bundled plugins, masks the stock services that race for `wlan0`, and repackages. The base image is cached under `image/cache/` so subsequent runs skip the download.

### One-shot build (Windows)

Use the PowerShell wrapper — it drives WSL2 for you.

```powershell
.\image\build.ps1
```

Or double-click `image\build.bat` in Explorer.

The wrapper auto-picks an Ubuntu distro, installs the apt packages pi-gen needs (the first run takes a few extra minutes for `apt install`), syncs the repo into WSL's native filesystem (`~/.knot-os-build/`), runs the build there, and copies the resulting `.img.xz` back into `image\deploy\`.

> **Why not just run pi-gen on `/mnt/e/...` directly?** The 9P protocol that exposes Windows drives inside WSL2 doesn't support every operation pi-gen needs (some `chmod`/`mknod` flags, sgid bits). Building from `~/...` inside the distro is reliable; building from `/mnt/...` fails partway through.

Useful flags:

| Flag | Effect |
|------|--------|
| `-Distro Ubuntu-22.04` | Pick a specific WSL distro instead of the auto-chosen one. |
| `-Clean` | Wipe `~/.knot-os-build/` in WSL before syncing — start fresh. |

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

### Recovery

The image ships with three independent recovery channels:

1. **Boot-time diagnostic log.** On every boot, `knotd-bootlog.service` writes `systemctl status`, `journalctl`, `iw`, `rfkill`, and recent `dmesg` output to `/boot/firmware/knot-startup.log`. Pop the SD into your computer; Windows mounts the boot partition automatically and you can open the file in any text editor.

2. **Serial console.** Enabled on UART0 at 115200 8N1. Connect a **3.3V** USB-TTL adapter to GPIO:
   - Pin 6 (GND) ↔ USB-TTL GND
   - Pin 8 (TXD) ↔ USB-TTL RX
   - Pin 10 (RXD) ↔ USB-TTL TX

   **Do not use a 5V adapter — it will damage the Pi.** Open `/dev/ttyUSB0` (Linux) or the COMx port (Windows) at 115200 8N1, no flow control. Login: `knot` / `knot`. The same pinout is mirrored to `/boot/firmware/SERIAL-CONSOLE.txt` for offline reference.

3. **SSH** is enabled by default and listens on whatever IP knotd assigns once the Wi-Fi extender role is up. Default credentials `knot` / `knot` — change immediately on a real deployment.

For v0.1, USB-Ethernet WAN is not supported. Recovery in extreme cases (corrupted config, broken extender role) involves popping the SD into a computer and editing `/etc/knot/config.yaml` directly.

## Pinning the base image

By default the script downloads the latest Raspberry Pi OS Lite arm64 from the redirector at `downloads.raspberrypi.com`. Override via env to pin a specific release:

```bash
sudo LITE_URL=https://downloads.raspberrypi.com/raspios_lite_arm64/images/raspios_lite_arm64-2025-05-13/2025-05-13-raspios-bookworm-arm64-lite.img.xz \
    bash image/build.sh
```

## Build cache

`image/cache/raspios_lite_arm64.img.xz` is reused across builds. To force re-download (e.g. to pick up a newer Lite release):

```bash
sudo rm -rf image/cache image/work image/deploy
sudo bash image/build.sh
```
