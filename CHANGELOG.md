# Changelog

All notable changes to KnotOS are documented here.

The format is loosely based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Starting with v2026.05.1 the project switches to **CalVer** (`v<year>.<month>.<release>[-<patch>]`) — semver no longer fits a routinely-deployed appliance whose user-visible "version" is mostly the date the image was built.

## [2026.06.9] — 2026-06-02

Theme: **"Plugins that look native and run boxed-in"**.

### Native plugin UI

- **Plugins no longer ship HTML.** A plugin returns a declarative JSON UI spec (sections of `stat` / `text` / `badge` / `table` items, optional `refresh_sec`) from its socket; the web UI renders it with native KnotOS components at `/plugins/<id>`. Every plugin page now matches the rest of the app, and no third-party HTML/JS runs in the admin UI — the iframe is gone. The reference plugin emits the spec; the contract is in [docs/plugin-api.md](docs/plugin-api.md).

### M4 — process sandbox

- **Plugins run unprivileged.** knotd drops each plugin process to a dedicated `knot-plugin` user (created by the image), in its own process group, with `Pdeathsig` so it dies with the daemon. A buggy or hostile plugin can't read root-owned config/secrets or outlive knotd; the host-API token still scopes what it can ask for. Configurable with `-plugin-user`; a missing user logs and runs unconfined (older images keep working). Tighter layers (seccomp, namespaces) are a future increment.

## [2026.06.8] — 2026-06-02

### Changed

- **Plugin store catalog moved to its own repo.** The store index now lives in the dedicated [bigtaed-sys/knot-os-plugins](https://github.com/bigtaed-sys/knot-os-plugins) repo (`store.json`) instead of inside the firmware tree, so the catalog can grow and accept third-party submissions independently of firmware releases. knotd's default `-plugins-index` points there; first-party packages are still built/signed by the firmware release pipeline and referenced from the catalog.

## [2026.06.7] — 2026-06-02

Theme: **"Plugin store"**. Browse and install plugins from a GitHub-hosted catalog — first-party packages verify automatically, third-party ones install only with explicit confirmation.

### Plugin store (M3)

- **Catalog from GitHub.** `GET /api/plugins/store` fetches a JSON index (`plugins/store.json`, default served from the project repo) and the Plugins page renders it with Install buttons and official / third-party badges.
- **Signed install, hardened.** `POST /api/plugins/install` downloads the zip package, verifies an optional detached Ed25519 signature against the release key (the same trust anchor as auto-update), and unpacks it — with zip-slip protection (no path traversal, no symlinks/absolute paths), size caps, and an atomic swap into place. A package signed by the release key is **official** and installs directly.
- **Third-party = explicit confirmation.** An unsigned / untrusted package returns `409 needs_confirmation`; the UI shows a clear "runs as a process on your router" warning and only proceeds when the operator confirms. Trust is decided by signature at install time, never by a catalog flag.
- **Uninstall.** `DELETE /api/plugins/{id}` stops the process and removes the directory.
- **Release pipeline** now builds, zips, and signs the reference plugin (`example-hello-linux-arm64.zip` + `.sig`) and the store catalog points at the release's `latest` assets, so the store works end to end out of the box.

## [2026.06.6] — 2026-06-02

Theme: **"Plugins that actually run"**. The plugin system graduates from metadata-only scaffolding to a real runtime.

### Plugin runtime

- **Enabled plugins run as supervised subprocesses.** knotd starts a plugin's process when enabled, stops it when disabled, restarts it with capped backoff on crash, and brings enabled plugins back up at boot. Runtime state (running / crashed / stopped, restart count, last error) is surfaced on the Plugins page.
- **HTTP-over-Unix-socket contract**, language-agnostic. A plugin listens on `KNOT_PLUGIN_SOCKET`; knotd reverse-proxies it (auth-gated) at `/api/plugins/<id>/proxy/` and the UI opens it in an iframe at `/plugins/<id>`.
- **Permissioned host API.** Plugins call back into knotd over `KNOT_HOST_SOCKET` with a per-plugin bearer token; each endpoint is gated by a permission the plugin declares in its manifest, so a plugin only reaches what it asked for. Read state (`/host/v1/{whoami,status,devices}`), **write** (`POST /host/v1/devices/{mac}/profile` under `devices:write` — reassigns a device, same scheduler-kick + routing-rebuild + bus event as the LAN UI), and **react** (`GET /host/v1/events` under `events:read` — a Server-Sent Events stream of router events: device joined, WAN up/down, profile changed, …).
- **Manifest v2**: `exec` (argv to launch) + `permissions`, fully backward-compatible — a manifest without `exec` stays a metadata-only plugin.
- **Reference plugin** `example-hello` rewritten as a real, dependency-free Go process that renders live router state pulled through the host API and a live feed of router events streamed from `/host/v1/events`. The image build now compiles bundled Go plugins for arm64 and ships only the manifest + binary. Contract documented in [docs/plugin-api.md](docs/plugin-api.md).

## [2026.06.5] — 2026-06-02

### Changed

- **Clearer VPN navigation.** The subscription / per-device routing page is now labelled **VPN** (it's what a user thinks of as "the VPN"), and the WireGuard road-warrior page is now **WireGuard server**. Previously the WireGuard page owned the "VPN" name while the actual VPN lived under "Routing", which was confusing. Sidebar order and icons updated to match (VPN with the globe, WireGuard server with a key).

## [2026.06.4] — 2026-06-02

Verification release — no functional changes. Cut to confirm the GitHub
auto-update path (check → download → verify → install → restart) works
end to end from the System page. The only observable difference is the
version string bumping from 2026.06.3.

## [2026.06.3] — 2026-06-02

### Fixed

- **Flashed devices didn't trust official releases.** `image/build.sh` never injected the release public key, so an image-built daemon trusted only its per-device rescue key and would reject every GitHub auto-update at the signature check (even after the repo fix). The release public key is now committed as the default in `update/keys.go` (it's public — safe to ship), so every build — image, local, and CI — verifies official releases. Install this build once with a rescue-signed binary; from then on GitHub auto-update works end to end.

## [2026.06.2] — 2026-06-02

### Fixed

- **Self-update checked a repo that doesn't exist.** The update manager defaulted to `knot-os/knot-os` (the Go module path, not a real GitHub repo), so "Check for updates" always failed with "could not reach GitHub" even with a healthy connection. The release repo is now configurable via `-update-repo` (default `bigtaed-sys/knot-os`). Install this build once manually; subsequent updates work from the System page.

## [2026.06.1] — 2026-06-02

Theme: **"Two engines, one tunnel — and it actually routes"**. The per-device VPN path is now reliable end-to-end on real hardware, gains a second proxy engine for the transports sing-box won't speak, and the setup wizard / subscription flow shed a string of first-boot papercuts.

### Per-device routing now actually carries traffic

- **The forward-chain fix.** The nftables `forward` chain (`policy drop`) only ever allowed `LAN ↔ WAN`, so once sing-box brought up its TUN, every tunneled device's packets (`LAN → knotvpn0`) hit the drop and died — "status: tunnel" with no internet. Both the router and extender rulesets now accept `LAN ↔ knotvpn0`. The TUN interface name is a shared constant (`singbox.TUNInterfaceName`) so the renderer and the firewall can't drift.
- **Routing changes take effect immediately.** Assigning a profile/device to a server (or refreshing a subscription) now fires the apply chain that re-renders sing-box — previously only the ad-block scheduler was kicked, so the tunnel assignment silently never reached the engine. The apply chain also runs once at boot, so tunnels (and WireGuard) come back up after a reboot without poking a setting.
- **A single unsupported server no longer takes everything down.** Outbounds sing-box can't render are dropped from its config (not fatal), and any device pinned to one trips the kill-switch instead of leaking direct. The TUN comes up whenever any device is routed, so the kill-switch is actually enforced even when every subscribed server is unusable.

### Xray-core alongside sing-box

- **`xhttp` (and the rest of the Xray-only matrix) now work.** sing-box keeps the TUN + per-device routing; servers it can't speak are hosted by a local **Xray-core** instance behind loopback SOCKS inbounds, which sing-box dials. The routing layer partitions each server: sing-box-native, Xray-via-SOCKS, or dropped. New `core/internal/xray` package (config render + manager), Linux supervisor, image stage `04-xray`, pinned version, deterministic SOCKS-port assignment.

### Routing UI

- **Server ping.** TCP-connect latency probe from the router to every server (`GET /api/subscriptions/ping`), shown as colour-banded badges in the server list and the picker. Works for every server regardless of engine support.

### Subscriptions

- **WAF redirect loops fixed.** The HTTP client gained a cookie jar, so DDoS-Guard / Cloudflare / Qrator clearance-cookie challenges no longer loop until "stopped after 20 redirects".

### Setup wizard

- Cable detection unified with the capability probe (USB-Ethernet adapters identified by uevent `PRODUCT=`, not just a `"usb"` path substring), so the wizard finally sees an RTL8152 on a Zero 2W and lets you pick the full-router role.
- `POST /setup/complete` 422 fixed: the wizard sent `device_name`/`country` flat while the backend expected a nested `device` object.
- Live Wi-Fi QR is now a real PNG (rendered server-side via the existing go-qrcode), not the raw `WIFI:` string.
- PPPoE WAN is greyed out with an "in development" tag (the backend only supports DHCP).

### Self-update

- The System page's manual update accepts an optional `.sig` file, so a rescue-key-signed binary can be installed straight from the browser (multipart) on production-keyed builds.

## [2026.05.1] — 2026-05-07

Theme: **"Per-device VPN, Happ-style"**. KnotOS gains a real subscription-driven VPN client that any LAN device can be routed through, picked per-profile from the admin UI or Telegram, with kill-switch and DNS-leak prevention by default.

### Versioning change

- Switched to CalVer: `v<year>.<month>.<release>[-<patch>]`. The previous `0.x.y` numbering is retired. Build artifacts now look like `KnotOS-zero2w-2026.05.1.img.xz`.

### Three flagship pieces

- **`sing-box` engine embedded in the image** with the full protocol matrix the Russian-market 2026 ecosystem uses: VLESS + REALITY + Vision flow, VMess (both v2rayN-JSON and URI), Trojan, Shadowsocks (SIP002 + legacy), WireGuard outbound. uTLS fingerprinting (`chrome` / `firefox` / `safari` / `random`). WebSocket / gRPC / HTTP/2 transports. Pinned upstream version, SHA-256 verified at image build, source-of-truth lives in one Go constant. `singbox.Manager` runs the supervisor: idle until the first user outbound appears, SIGHUP reload on changes, full stop when the last server is removed.
- **Subscription registry + parser**. Paste a provider's HTTPS subscription URL or a single share link (`vless://`, `vmess://`, `trojan://`, `ss://`) and KnotOS reads the bundle, decodes whatever encoding the provider chose (base64, raw text, the v2rayN-shaped JSON-in-base64), and emits ready-to-use outbounds. Stable per-server IDs (sha1 of the URI prefix) so the UI's "selected server" pointer survives a re-fetch even when upstream order shifts. A bad fetch never trampling the previous good snapshot — the registry records the error, keeps the cached server list. Header surfacing for `subscription-userinfo` (the de-facto quota header) and `profile-title`.
- **Per-device routing + kill-switch + DNS-leak fix**. `Profile.RouteVia` is the one new field — assign a server tag (`<sub-id>:<server-id>`) to a profile, every device in that profile is now tunneled. The render pipeline always prepends a LAN-bypass rule so RFC1918 traffic can never accidentally take a tunnel. A profile pointing at a vanished server (provider removed it on a re-fetch) is auto-routed to `block` instead of silently leaking through `direct` — this is the kill-switch. The `tun` inbound is enabled with `auto_route=true, strict_route=true` whenever any user outbound exists; sing-box itself owns the iproute2 + nftables rules, no mirror in `apply_router.go`. DNS for tunneled sources gets a parallel `dns.rules` entry that pins their resolution through the same outbound — no plaintext UDP/53 leaking to the LAN's dnsmasq.

### "Маршрутизация" UI page

- Top-line warning when any profile points at a vanished server (with the affected outbound tags).
- Subscription cards: per-card Refresh, "updated 3 min ago", inline server list, last-error in red.
- Profile → server picker grouped by subscription, includes a "Direct" option.
- Per-device live status: «Tunnel» / «Direct» / «Kill switch» (red), background re-poll every 15 s.
- Full ru/en localization with plural-aware strings.

### Telegram bot polish

Every visible string rewritten for warmth — empty states tell you what to do next, notifications carry a follow-up sentence, /help groups commands by section emoji. New `/routing` command shows bucketed counts (subs/servers/devices-by-status/missing) and warns about dead servers; `/tips` is a hidden Easter egg with five tricks new users won't find on their own. Build-time parity tests catch the easy mistake of adding a phrase to one language without the other or using mismatched format verbs.

### Operator-facing additions

- `GET /api/routing` — per-device decisions and `missing_outbounds` for the UI page and the new `/routing` command.
- `GET/POST/PATCH/DELETE /api/subscriptions[/{id}[/refresh]]` — full CRUD for subscriptions.
- `POST /api/subscriptions/manual/uris` — paste-a-link entry point.

### Hardware acceptance

See [`docs/v0.5-acceptance.md`](docs/v0.5-acceptance.md) for the full step-by-step. Highlights: confirm `/dev/net/tun` opens, sing-box installs `ip rule` + `nft` entries via auto-route, a tunneled device's exit IP at `ifconfig.me` is the provider's, the kill-switch fires when a server vanishes from a re-fetched subscription, and the dnsleaktest result shows only the provider's resolver.

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
