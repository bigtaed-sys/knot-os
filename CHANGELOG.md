# Changelog

All notable changes to KnotOS are documented here.

The format is loosely based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project adheres to semantic versioning.

## [0.4.0] — 2026-05-05

Theme: **"Networks beyond your LAN"**. KnotOS stops being only a private-LAN appliance; the device now reaches out into the wider internet thoughtfully and lets you reach back into your home from anywhere.

### Three flagship features

- **WireGuard road-warrior server** with QR onboarding. Open System → VPN → Add peer → name → scan the resulting QR with the official WireGuard app on iOS/Android. Two seconds later your phone is tunneled into the home LAN. Per-peer profiles reuse the same `ProfileRegistry` that drives LAN ad-block + schedules — your kid's phone gets the same blocking policy on cafe Wi-Fi as it does at home. Ed25519 keys, all paths atomic, server-side never holds a peer's private half after the create response flushes.
- **Guest network with QR pass + auto-expire** (`wifi-router` role only in v0.4). Multi-BSSID hostapd on the same radio: separate SSID, generated 12-character PSK, `ap_isolate=1`. Guests get a separate `192.168.43.0/24` subnet with its own DHCP scope; nftables drops every forward between guest and main LAN both directions, allows guest → WAN with masquerade. Pick a duration (1h / 4h / 24h / "until I revoke"), get a fullscreen `WIFI:T:WPA;...` QR + the PSK in clear, share, walk away. 30-second sweeper tears the BSS down on expiry without operator action.
- **DNS-over-HTTPS upstream**, switchable from the Protection page. RFC 8484 over POST `/dns-query`, HTTP/2 connection pool to Cloudflare + Quad9 in the default DoH list (or any custom https URL the user adds). The cache, query log, and per-device blocklists work identically — DoH is a transport swap, not a separate code path. The provider sees only the TLS handshake to Cloudflare, not the actual domain names.

### Operational comforts

- **Wake-on-LAN button** on the device-detail page, visible only when the device is offline. Standard 102-byte magic packet sent as a directed broadcast on the LAN.
- **2.4 GHz channel scanner** in System (router role only). One click scans the airwaves, projects neighbour APs onto channels 1..13 with a 5-channel-wide overlap falloff, picks the least-loaded of {1, 6, 11}, offers a "switch to N" button that restarts the AP on the new channel.
- **Telegram bot** — full two-way control, not just push notifications. Token + 6-digit PIN to link a chat, then `/status` `/devices` `/protection` `/guest` `/lang` commands with inline keyboards: tap a device → wake it / change its profile from the chat. Notifications fire on WAN up/down, new devices joining, profile changes, guest-session lifecycle, available updates. Per-chat language (ru/en), bot token wrapped through the secrets sealer at rest, long-polling so no public URL or port-forward needed.

### Bug fixes

- Protection page no longer locks on the loading spinner when one of the four panel APIs hiccups; switched to `Promise.allSettled` + unconditional `loading=false` in `finally`.
- `apply_router` no longer aborts when the WAN cable isn't plugged in yet — `dhclient` runs in the background, the AP comes up regardless.
- Dashboard renders correctly in `wifi-router` role (i18n strings + a real WAN tile).

### What's deferred to v0.5

- Per-device bandwidth tracking (24h sparklines, top-N consumers on the dashboard) — needs a stable side-table for nftables counters that survives Apply, plus a persisted ringbuffer; its own milestone in scope.
- mDNS reflector for the guest network (avahi in reflector mode whitelisting only AirPlay / Chromecast service types).
- Mesh / multi-node discovery, plugin runtime, Cloudflare-tunnel-style remote access to the admin UI, DNSSEC validation, smart-home integrations.

### Plus catch-up entries (v0.2 + v0.3 weren't written up at the time)

- **0.2.0** — per-device profiles + ad-block:
  Device registry pulls live state from dnsmasq leases + a YAML store of user-set names/profiles, served at `/devices`. Profiles add schedules (block windows in the week-grid editor) plus DNS blocklists; the scheduler ticks every 30s and pushes blocked MACs into nftables, the DNS resolver does per-source blocklist filtering. New "Protection" page renders blocked-domain charts + the live query log. Tag landed 2026-05-02.
- **0.3.0** — closing the trust + hardware ceilings:
  HTTPS by default with a per-device self-signed root CA (download from the captive-portal / System page once, install on phones). Wi-Fi PSKs encrypted at rest with AES-256-GCM, key derived from a `/boot/firmware/knot-key-seed` mixed with `/etc/machine-id` so a stolen SD without the running system can't unwrap. Signed self-updates from GitHub Releases (Ed25519, release key baked in at build time, optional rescue key generated on first run). Persisted sessions across daemon restart. Full-router role on Pi 4/5 — and on Pi Zero 2W with a USB-Ethernet adapter (driver-agnostic detection covering r8152 / asix / cdc_ether). ARP-table presence detection so the "online" pill flips off within minutes of a phone leaving instead of waiting for the 12-hour DHCP lease. Tag landed 2026-05-03.

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
