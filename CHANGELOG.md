# Changelog

All notable changes to KnotOS are documented here.

The format is loosely based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Starting with v2026.05.1 the project switches to **CalVer** (`v<year>.<month>.<release>[-<patch>]`) — semver no longer fits a routinely-deployed appliance whose user-visible "version" is mostly the date the image was built.

## [2026.07.1-1] — 2026-07-25

### Fixed

- **Zapret now runs on a cellular-modem WAN.** In modem mode the config's WAN interface is empty (the `wwanN` device is discovered at connect time), so Zapret saw "no WAN", stopped `nfqws`, and never even reached the binary download — the UI sat on "will be downloaded / not running" forever. Both egress-interface resolvers now ask the backend for the live modem interface, so enabling Zapret on a modem downloads `nfqws` and starts as expected. Auto-tune (which had the same blind spot, failing with "no WAN") is fixed too.
- **Refreshing strategies no longer shrinks the list.** Flowseal changing a `.bat`'s format used to overwrite a working strategy with an unparseable one, which `LoadStrategies` then dropped — so a refresh could leave *fewer* strategies than shipped. Now the loader prefers a disk copy only when it actually converts and otherwise falls back to the embedded seed (so a bad refresh self-heals on the next load), and the refresh itself skips a renamed/removed upstream file (and validates a download before overwriting) instead of aborting the whole run on the first error.

## [2026.07.1] — 2026-07-25

### Cellular modem WAN — Phase 2 (keepalive + resilience)

Validated on real hardware (Quectel EC25-EC) and hardened for day-to-day use as the primary WAN.

- **Keepalive watchdog.** A cellular WAN used to connect once, at apply, and never recover: a modem that dropped out (USB autosuspend, network re-registration, a SIM hot-swap) stayed down until a reboot. A background watchdog now runs while a modem is the WAN and, every 30s, **reconnects** a dropped modem and **resets** one stuck in ModemManager's `failed` state (rate-limited) — so the router self-heals, including after reinserting the SIM, without a reboot. Recovery refreshes NAT for the live interface but never restarts hostapd/dnsmasq, so Wi-Fi clients aren't bounced.
- **USB autosuspend disabled for the modem.** The most common cause of "internet works for a while, then the modem goes `disabled`": Linux suspends the idle USB modem and drops its control port. knotd now pins the modem's whole USB device chain to `on` at connect time.
- **Real failure reasons in the UI.** A failed connect used to surface a bare "failed". The modem's own diagnosis (`state-failed-reason`, e.g. `sim-missing`) now flows to the Modem page and the wizard — with an actionable hint (reseat the SIM, etc.).
- **SIM-slot switching** for modems that expose more than one slot: a slot selector appears on the Modem page (gated on ModemManager reporting multiple slots) and switches the active SIM via `--set-primary-sim-slot`. Inert on single-slot modems (e.g. EC25).
- **APN is now truly optional.** The field is marked optional, and the image bundles `mobile-broadband-provider-info` so ModemManager can auto-provision the APN from the SIM's carrier when the field is left blank.
- **Dashboard shows the modem WAN correctly.** In modem mode the WAN card no longer reads "down / waiting for Ethernet" — status now reflects the live modem interface (up when it has an IP), with modem-aware copy and a SIM icon.
- Wizard/role copy is no longer Ethernet-only ("wired **or** cellular internet in, Wi-Fi out"), and the modem-page wording was made neutral and professional.

## [2026.06.16-1] — 2026-06-06

### Added

- **Cellular modem in the setup wizard.** The first-run wizard's connection step now asks "how does this router get internet?" with an **Ethernet** vs **Cellular modem** choice (router mode). Picking the modem shows live detection (model / carrier / SIM-lock) polled from ModemManager, plus an APN field (and PIN when the SIM is locked). This is what makes onboarding a cellular-only device possible — e.g. a remote site with no wired internet — since the wizard runs over the device's own AP and brings the modem up as the WAN. The post-setup Modem page remains for changing it later. `POST /setup/complete` and a new `GET /setup/modem` carry the cellular config through onboarding.

## [2026.06.16] — 2026-06-06

### Cellular modem WAN — Phase 1 (experimental)

- New **Cellular modem** page: share internet from a USB LTE/4G modem. A new WAN mode `modem` drives the modem via **ModemManager** (`mmcli`) — unlock the SIM (PIN), attach with the carrier **APN**, and adopt the modem's data interface (e.g. `wwan0`) as the router WAN; NAT/DHCP/AP are identical to the Ethernet path from there. QMI static-addressed bearers are applied directly; DHCP bearers get a `dhclient`. HiLink dongles that show up as a plain network card can still be used via the normal `dhcp` WAN instead.
- Live status (polled): modem model, carrier, access tech (LTE/5G), **signal bars**, connection state, and SIM-lock prompt. The image now bundles `modemmanager`, `libqmi-utils`, `libmbim-utils`, and `usb-modeswitch`.
- **Off by default** — your current Ethernet/Wi-Fi WAN is untouched until you switch the mode on the Modem page. If the modem isn't connectable, the apply no longer aborts: the LAN/AP and dashboard stay up so you can fix the SIM/APN.
- **Experimental:** built to the ModemManager spec without modem hardware on hand. Recommended modem for external **MIMO** antennas: **Quectel EC25-E / EP06-E** (or Sierra EM7455) in a USB enclosure. Failover (auto-switch wired ↔ cellular) is Phase 2.

## [2026.06.15-3] — 2026-06-06

### Fixed

- **Changing any Zapret setting no longer drops the Wi-Fi.** Toggling Zapret, switching strategy, or running auto-detect went through the full transactional config-apply, which re-applies the whole network stack — restarting hostapd and kicking every wireless client. Zapret only drives the `nfqws` daemon and its own isolated nft table, so it now persists + reconciles **without** a `backend.Apply`, exactly like the profiles/devices endpoints. Wi-Fi stays up.

## [2026.06.15-2] — 2026-06-05

### Changed

- **Zapret strategies are now the real Flowseal ones, and they update from upstream.** The previous hand-written presets used dated desync methods that ISPs block. KnotOS now ships the actual `Flowseal/zapret-discord-youtube` strategy `.bat` files (General + ALT…ALT6 + FAKE TLS AUTO + SIMPLE FAKE), converted winws→nfqws at load time: windivert `--wf-*` flags are stripped (their ports drive the nft queue instead), `%BIN%/%LISTS%` become on-disk paths, and the empty game-filter profiles are dropped. So you get current methods like `multisplit`+`seqovl`, `hostfakesplit` and `fake-tls-mod` (ALT3) verbatim.
- **"Update from Flowseal"** now refreshes both the domain lists **and** the strategy catalogue from the upstream repo — strategies are data on disk, so they track upstream without a new knotd build. The nft queue ports are derived per-strategy from each strategy's filter, so strategies that need extra ports (e.g. discord.media on 2053/8443) get them automatically.

## [2026.06.15-1] — 2026-06-05

### Added

- **Zapret auto-detect.** An "Auto-detect" button on the Zapret page cycles through every preset, applies each live, and probes the YouTube/Discord test hosts (TLS handshake to `i.ytimg.com`, `www.youtube.com`, `cdn.discordapp.com`, `gateway.discord.gg`) through the active bypass — then picks and saves the preset with the most successful handshakes (lowest latency breaks ties). Mirrors zapret's `blockcheck`. Runs on the router itself (the nft hook sits on WAN postrouting, which also catches knotd's own connections) and shows a per-strategy score table including an "off" baseline. Best at spotting RST-based blocking (Discord, RST-throttled YouTube); pure bandwidth throttling, where handshakes succeed regardless, is harder to detect — fall back to trying presets by eye there.

## [2026.06.15] — 2026-06-05

### Zapret — DPI bypass for YouTube / Discord

- A new **Zapret** page brings the [bol-van/zapret](https://github.com/bol-van/zapret) `nfqws` engine to KnotOS: reach throttled YouTube/Discord through ISP DPI without a full tunnel. Packet-level desync (TLS/QUIC fragmentation, fake packets) applied **only** to those services' domains via on-disk hostlists, so the rest of the LAN's traffic is untouched.
- **Strategy presets** ported from [Flowseal/zapret-discord-youtube](https://github.com/Flowseal/zapret-discord-youtube) (General / ALT / ALT2 / Discord-only) in a dropdown — DPI behaviour is ISP-specific, so you switch presets until one breaks through, exactly like the Flowseal `.bat` collection. A **custom-strategy** field takes a raw nfqws argument string for full control.
- **Updatable without a new firmware build.** The desync strategy is config + on-disk lists, not baked into knotd. Domain lists live under `/var/lib/knot/zapret/` and a one-click **"Update from internet"** re-pulls them from the upstream Flowseal repo. The binary ships only seed defaults (written on first run when absent), so refreshed lists survive a knotd update.
- **Safe by construction:** the nftables hook lives in its own `inet zapret` table, applied/torn down independently of the main ruleset — a bad queue rule can't break NAT/forwarding — and uses `queue … bypass`, so if nfqws is down the packets pass through untouched.
- **Delivery:** `nfqws` (static arm64, ~125 KB) is staged into the image *and* downloaded-on-demand with a pinned SHA-256 when missing, so a device updated over OTA from an older image still gets the feature without reflashing. Active only in router mode (it acts on WAN-egress traffic).

## [2026.06.14] — 2026-06-02

Theme: **"Sharper control over traffic"** — three network features the daemon had the plumbing for but no surface to use.

### Per-domain split tunnel (VPN)

- A profile that routes through a tunnel can now do so **only for chosen domains** instead of the whole device. On the VPN page each assigned profile gets a **Split** button: paste `youtube.com`, `netflix.com`, … and only traffic to those domains (matched by sniffed SNI) takes the tunnel — everything else stays direct, at full speed. Empty list = the previous whole-device behaviour. Split devices keep direct DNS (no forced tunnel resolver), and a missing server still kill-switches the matched domains rather than leaking them. New device status badge: **Split**.

### SafeSearch enforcement (profiles)

- A per-profile **Force SafeSearch** toggle. The resolver CNAME-rewrites Google (incl. all country TLDs), Bing, DuckDuckGo and YouTube to the providers' own enforcement hosts (`forcesafesearch.google.com`, `restrict.youtube.com`, …) for devices on that profile — no client config, works over plain DNS. HTTPS/SVCB lookups get NODATA so browsers fall back to the rewritten address. Pairs naturally with the existing schedule + ad-block in the kids profile.

### Inbound port forwarding (router mode)

- A new **Port forwarding** page: map a WAN port to a LAN device (`tcp`/`udp`/both), optional different internal port, per-rule enable toggle, with a device-IP autocomplete. Renders as nftables `prerouting` DNAT + a scoped forward-accept in the router ruleset; duplicate `(proto, port)` pairs are rejected. Changes go through the transactional apply path (snapshot + health-check + auto-rollback). Saved-but-inactive with a notice when not in router mode (the extender has no WAN of its own).

## [2026.06.13-3] — 2026-06-02

### Fixed

- **Sidebar footer showed a hardcoded `v0.1.0-dev`** instead of the running version. It now reflects the real version reported by `/api/status`.

## [2026.06.13-2] — 2026-06-02

### Fixed

- **Devices no longer double up when a phone rotates its private MAC.** A phone with a rotating randomized Wi-Fi address used to appear as a new device on every reconnect (e.g. after a knotd update or reboot), leaving ghosts in the list. The registry now recognizes locally-administered (private) MACs and, when one reappears under a new MAC with the same DHCP hostname while the old entry is offline, **carries the device's name, profile, pause, and approval forward** and drops the old entry — so it stays one device. Anonymous, uncustomized rotation ghosts that are offline for over 6 hours are also pruned hourly. Configured devices are never auto-removed.

## [2026.06.13-1] — 2026-06-02

### Fixed

- **Blocked-device landing page never appeared** — the plain-HTTP listener's HTTPS 301 ran *before* the landing check, so a blocked device's captive probe got redirected to a cert it couldn't validate ("can't reach the site") instead of the page. The landing now serves on plain HTTP before any redirect, so captive detection pops it up. (First patch-numbered release — small fixes ship as `-N` now.)

## [2026.06.13] — 2026-06-02

### Blocked-device landing page

- An optional toggle (Devices page): when a device is **paused**, **awaiting quarantine approval**, or **schedule-blocked**, its DNS is captive-redirected to the router, which serves a clear bilingual page saying *why* it has no internet. Because OS captive-portal detection probes plain HTTP, the page **pops up automatically** and any site the user opens lands on it — no nftables surgery, just DNS + an HTTP intercept. Off by default; the device still gets no internet either way, this just explains it.

### Versioning

- **Auto-update now understands patch versions.** `isNewer` compares the `-PATCH` suffix numerically (`2026.06.13-2 > 2026.06.13-1 > 2026.06.13`), so small follow-ups can ship as patch tags (`vYEAR.MONTH.RELEASE-N`) instead of burning a release number each time. This is the last plain release bump needed to teach the updater that; subsequent small changes will be patches.

## [2026.06.12] — 2026-06-02

Theme: **"Hands on the devices"** — direct access control over what's connected.

### Per-device internet pause (+ timer)

- Block a device's internet **right now** from its detail page — for 15 min / 1 hour / 8 hours / until you resume. Auto-resumes when the timer expires; survives a reboot. Enforcement is immediate (scheduler kick), not on the next tick. The devices list shows a **Paused** badge.

### New-device quarantine

- A network-wide switch (toggle on the Devices page): while on, a device that appears for the first time gets **no internet until you approve it**. Turning quarantine on auto-approves every device already known, so it never strands the household — only genuinely new arrivals are gated. Pending devices show an **approval** badge and an Approve button.

Both ride the existing nftables block-set + scheduler (which now blocks on *paused OR un-approved-under-quarantine OR schedule*), and persist in the device registry. New API: `POST /devices/{mac}/{pause,resume,approve}` and `GET/PUT /devices/access`.

*(Per-device speed limiting via `tc` is the next, separate step — it can break connectivity if mis-applied, so it warrants on-device validation rather than shipping blind into auto-update.)*

## [2026.06.11] — 2026-06-02

Theme: **"A plugin you'd actually install"** — Dynamic DNS, plus the platform pieces a real configurable plugin needs.

### Dynamic DNS plugin

- **New `ddns` plugin** (in the store): keeps a DuckDNS — or any custom-URL provider — hostname pointed at your changing public IP, so you can reach home from anywhere. It reads the WAN IP from the host API, reacts to `wan_status` events to update immediately on an IP change (plus a 5-min re-assert), and shows status (public IP, domain, last update + result) with a config form (provider, domain, token, custom URL). Declares the `network` permission; stores its config in the plugin data dir.

### Platform additions that made it possible

- **Interactive plugin UI.** The declarative UI spec gained `input`, `select`, and `action` items. Clicking an action POSTs the form values to the plugin (`/api/plugins/<id>/proxy/<action>`); still rendered natively by KnotOS, still no plugin HTML/JS in the browser.
- **Per-plugin data directory.** Plugins now get `KNOT_PLUGIN_DATA` — a persistent, writable directory (`/var/lib/knot/plugins/<id>`, owned by the plugin user) — the one place an unprivileged, sandboxed plugin may persist config/state.
- **Release pipeline** builds, zips, and signs *every* bundled Go plugin (glob-based), so adding a plugin needs no CI change.

## [2026.06.10] — 2026-06-02

### M5 — plugin namespace isolation

- **Plugins run in private PID / IPC / UTS namespaces** on top of the unprivileged-user drop, so a plugin can't see or signal other processes, share SysV IPC, or touch the host's hostname.
- **Network is default-deny.** A plugin runs in an empty network namespace — no internet, no LAN — *unless* it declares the new `network` permission. Its Unix sockets (the host API and its own UI socket) keep working because those are filesystem objects, so a typical plugin (like the reference one) needs no network at all. A plugin that must reach the internet opts in explicitly with `network`. Documented in [docs/plugin-api.md](docs/plugin-api.md).

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
