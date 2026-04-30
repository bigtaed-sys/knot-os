# KnotOS

> *Ties your network together.*

**KnotOS** is an open-source router firmware for Raspberry Pi. It boots a patched Raspberry Pi OS Lite image, replaces the network stack with a declarative configuration system, exposes a modern web UI, and supports a plugin ecosystem for extending functionality.

The project is designed to scale across hardware: a Raspberry Pi Zero 2W can run as a Wi-Fi extender out of the box, while a Pi 4 or Pi 5 with a USB Ethernet dongle can act as a full router with VPN, mesh, and per-device routing.

## Status

**v0.1.0 — first release.** Builds a flashable image for Raspberry Pi Zero 2W that boots into a first-run wizard and configures itself as a Wi-Fi extender. See [CHANGELOG.md](CHANGELOG.md) for what works, the known limitations, and what's deferred.

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

Issues and design discussions are very welcome. Pull requests are accepted but please open a discussion first for anything beyond a small fix — the architecture is still settling and a quick check saves rework.

### Quick development loop

```bash
# Daemon in dev mode (mock network, no root)
go build -o ./dist/knotd ./core/cmd/knotd
./dist/knotd -dev -listen :8080 -config ./tmp/config.yaml

# UI dev server in another terminal (live reload, proxies /api to :8080)
cd ui && npm run dev
```

### Test suite

```bash
go test ./core/...
cd ui && npm run check
```

CI runs on every push: vet, native build, `linux/arm64` cross-build, the full test suite, plus the SvelteKit typecheck and bundle. Image building is not part of CI — it runs on demand via `image/build.sh` (see [docs/building.md](docs/building.md)).
