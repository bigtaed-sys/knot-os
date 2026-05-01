# Changelog

All notable changes to KnotOS are documented here.

The format is loosely based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project adheres to semantic versioning.

## [0.1.0] — 2026-05-01

First release. Targets Raspberry Pi Zero 2W as a Wi-Fi extender — connects to an upstream Wi-Fi network and re-broadcasts a separate SSID, NATting between the two. No second radio, no Ethernet dongle required.

### What works

- **Flashable image** built via `sudo bash image/build.sh` — downloads the official Raspberry Pi OS Lite arm64 image, chroots in to install our extras (hostapd, dnsmasq, nftables, iw, isc-dhcp-client, avahi-daemon), injects the ~11 MB knotd binary (with the embedded SvelteKit UI) plus its systemd unit, and repackages. ~5–10 min total.
- **First-run wizard**: open onboarding AP `KnotOS-setup-XXXX`, captive portal at `192.168.42.1`, four steps (device + admin password, scan + select upstream Wi-Fi, broadcast SSID, review). Issues a session cookie on completion.
- **Wi-Fi extender role** on a single radio: simultaneous `ap0` + `wlan0` virtual interfaces on `phy0`, hostapd + wpa_supplicant + dnsmasq + nftables NAT, channel auto-aligned to the upstream.
- **Web UI**: dashboard with live uplink/AP status (RSSI, client count), plugin list with enable/disable toggles, login screen with bcrypt-hashed admin password, session cookies with 24h TTL.
- **Plugin discovery** at `/usr/lib/knot/plugins/`. `plugin.yaml` manifest. v0.1 plugins are metadata-only — runtime ships in v0.2.
- **CLI**: `knotctl` skeleton (subcommands flesh out in v0.2).
- **CI**: every push runs vet, native build, linux/arm64 cross-build, full test suite, UI typecheck and bundle.
- **50+ unit tests** across config, auth, api, network, plugin, and Linux-backend (templates + iface helpers + scan parser).

### Known limitations

- Single-radio repeater shares a channel with the upstream and gets ~50% throughput by design — expected on the BCM43436.
- Wi-Fi PSKs are stored in plaintext under `/etc/knot/config.yaml` (root-owned, 0600). Per-device-key encryption is in the v0.2 backlog.
- Sessions live in memory and are lost on restart.
- No HTTPS yet — the dashboard is HTTP only on the LAN.
- Plugins discover and toggle but cannot run code yet.

### What's deferred

- Network Time-Machine (config snapshots + rollback) — dropped from v0.1 in favor of the "simple device" goal.
- Mesh / multi-node discovery.
- Per-device profiles, schedules, parental controls.
- Guest network with QR.
- USB-Ethernet WAN (full-router role).
- Plugin runtime (gRPC, sandboxing, UI extension points).
- Armbian build profile for non-Pi boards (Orange Pi, Rock Pi) — gated on confirming AP-mode Wi-Fi driver support per board.
