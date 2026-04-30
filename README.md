# KnotOS

> *Ties your network together.*

**KnotOS** is an open-source router firmware for Raspberry Pi. It boots a patched Raspberry Pi OS Lite image, replaces the network stack with a declarative configuration system, exposes a modern web UI, and supports a plugin ecosystem for extending functionality.

The project is designed to scale across hardware: a Raspberry Pi Zero 2W can run as a Wi-Fi extender out of the box, while a Pi 4 or Pi 5 with a USB Ethernet dongle can act as a full router with VPN, mesh, and per-device routing.

## Status

**Pre-alpha, v0.1 in flight.** The daemon, UI, mock backend, Linux backend (hostapd / wpa_supplicant / dnsmasq / nftables / `iw`), authentication, and first-run wizard are all in place. `image/build.sh` produces a flashable Pi Zero 2W image. Plugin system and on-hardware acceptance testing are the remaining v0.1 work.

## Hardware support

| Board | Status | Notes |
|---|---|---|
| Raspberry Pi Zero 2W | Primary target for v0.1 | Wi-Fi extender role using simultaneous `ap0`+`wlan0` on BCM43436 |
| Raspberry Pi 4 / 5 | Planned for v0.2 | Adds full-router role with USB Ethernet WAN |

## Repository layout

```
core/      Go daemon (knotd) — networking, API, plugin host
cli/       Command-line client (knotctl)
ui/        SvelteKit web UI (embedded into knotd)
image/     pi-gen integration for building flashable images
plugins/   Reference plugins (example-hello)
scripts/   Dev helpers (dev-run.sh, cross-build.sh, flash.sh)
docs/      Architecture, plugin API, build instructions
```

## Building

Image builds require Linux (WSL2 on Windows is supported). See [docs/building.md](docs/building.md).

Local development of the daemon and UI works on any Go 1.22+ / Node 18+ host.

```bash
# Run the daemon in dev mode (mocks network operations)
./scripts/dev-run.sh

# In another terminal, run the UI dev server
cd ui && npm run dev
```

## License

- **Daemon, CLI, and image-build tooling**: GPLv3 — see [LICENSE](LICENSE).
- **Web UI**: AGPLv3 — see [ui/LICENSE](ui/LICENSE).

The license split mirrors the OpenWrt model: copyleft on the firmware to prevent proprietary forks, AGPL on the UI so any hosted variant must publish its source.

## Contributing

The project is in early scaffolding. Issues and design discussions are welcome; pull requests should wait until the v0.1 milestone closes and the contribution guidelines are in place.
