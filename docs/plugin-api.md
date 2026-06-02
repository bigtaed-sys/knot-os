# Plugin API

> Status: **v2 runtime — live.** Plugins run as supervised subprocesses
> that speak HTTP over a Unix socket. knotd reverse-proxies their UI and
> exposes a permissioned host API they call back through.

## Anatomy

A plugin is a directory under `/usr/lib/knot/plugins/<id>/`:

```
<id>/
├── plugin.yaml      # manifest (required)
└── <id>             # the executable launched while enabled (any language)
```

## Manifest

```yaml
id: example-hello            # must equal the directory name; URL-safe
name: Hello Plugin
version: 0.2.0
description: One-liner shown in the plugins list.

# argv knotd runs while the plugin is enabled. A "./"-prefixed argv[0]
# resolves against the plugin's own directory. Omit `exec` for a
# metadata-only plugin (discovered + toggleable, runs nothing).
exec:
  - ./example-hello

# Host-API scopes. The host returns 403 for anything outside this set.
permissions:
  - status:read
  - devices:read

# Optional sidebar entries (shown only while enabled).
menu:
  - path: /plugins/example-hello   # SPA route hosting the plugin page
    label: Hello
    icon: bi-stars                 # Bootstrap Icons class
    order: 100
```

## Lifecycle

- Enable a plugin (UI / `PUT /api/plugins/{id}`) → knotd starts its
  process. Disable → knotd stops it. Crashes are restarted with capped
  exponential backoff. State is persisted, so enabled plugins come back
  up on reboot.
- The process is launched with its directory as the working directory
  and these environment variables:

  | Env | Meaning |
  |---|---|
  | `KNOT_PLUGIN_ID` | the plugin's id |
  | `KNOT_PLUGIN_SOCKET` | Unix socket path the plugin **must listen on** (its HTTP server) |
  | `KNOT_PLUGIN_DATA` | a persistent, writable directory for the plugin's config/state (the one place an unprivileged plugin may write) |
  | `KNOT_HOST_SOCKET` | Unix socket of knotd's host API |
  | `KNOT_HOST_TOKEN` | bearer token to authenticate host-API calls |

## UI — declarative, native KnotOS look

The plugin runs an HTTP server on `KNOT_PLUGIN_SOCKET` and returns a
**JSON UI spec** from its root (`GET /`). knotd reverse-proxies it
(auth-gated) at `/api/plugins/<id>/proxy/`, and the web UI renders the
spec with native KnotOS components at the route `/plugins/<id>`. The
plugin never ships HTML or JS to the browser — so every plugin page
matches the rest of the app, and there's no third-party code running
in the admin UI.

```json
{
  "title": "Hello Plugin",
  "refresh_sec": 5,
  "sections": [
    { "title": "Router", "items": [
      { "type": "text",  "text": "A short description." },
      { "type": "stat",  "label": "Device", "value": "knot" },
      { "type": "stat",  "label": "WAN", "value": "up", "tone": "ok" },
      { "type": "badge", "text": "online", "tone": "ok" },
      { "type": "table", "columns": ["Time","Event"], "rows": [["12:00","device_joined"]] }
    ]}
  ]
}
```

Display item types: `stat` (label/value, optional `tone`), `text`,
`badge`, `table`. `tone` ∈ `ok` | `warn` | `bad` | `neutral`.
`refresh_sec` (>0) makes the UI re-fetch on that interval.

Interactive item types:

- `input` — `{ "type":"input", "key":"...", "label":"...", "value":"...", "placeholder":"...", "secret":true }`
- `select` — `{ "type":"select", "key":"...", "label":"...", "value":"...", "options":[{"value":"","label":""}] }`
- `action` — `{ "type":"action", "label":"Save", "action":"save", "style":"primary|ghost" }`

When the user clicks an `action`, the UI POSTs the current values of
every `input`/`select` (keyed by `key`) as a JSON object to
`/api/plugins/<id>/proxy/<action>`, then re-fetches the spec. The
plugin handles that path (e.g. saves config) and returns `200`. Echo a
secret back as empty with a "leave blank to keep" placeholder rather
than re-sending it.

## Sandbox

On the device, knotd confines each plugin process:

- **Unprivileged user** — runs as `knot-plugin` (not root), so it
  can't read root-owned config/secrets. Override with
  `knotd -plugin-user`; a missing user ⇒ unconfined (logged).
- **Lifetime + process group** — own pgroup, `Pdeathsig` so it's
  killed if knotd dies.
- **Namespaces** — private PID / IPC / UTS namespaces (can't see or
  signal other processes, share IPC, or touch the hostname).
- **Network default-deny** — a plugin runs in an *empty network
  namespace* (no internet, no LAN) **unless** it declares the
  `network` permission. Its Unix sockets (host API + its own UI
  socket) keep working regardless, since those are filesystem objects.
  Declare `network` only if the plugin must reach the internet.

The host-API token additionally scopes what a plugin may *ask* knotd
to do. Future layers (seccomp, mount confinement, cgroup limits) can
stack on top.

### Permissions summary

| Permission | Grants |
|---|---|
| `status:read` | `GET /host/v1/status` |
| `devices:read` | `GET /host/v1/devices` |
| `devices:write` | `POST /host/v1/devices/{mac}/profile` |
| `events:read` | `GET /host/v1/events` |
| `network` | outbound network access (else net-isolated) |

## Host API

Plugins call knotd over `KNOT_HOST_SOCKET`, presenting
`Authorization: Bearer $KNOT_HOST_TOKEN`. Each endpoint requires a
permission declared in the manifest.

| Endpoint | Permission | Returns / Body |
|---|---|---|
| `GET /host/v1/whoami` | — | `{plugin_id, permissions}` |
| `GET /host/v1/status` | `status:read` | `{role, device_name, version, wan_up, wan_ip}` |
| `GET /host/v1/devices` | `devices:read` | `{devices: [{mac, label, ip, online}]}` |
| `POST /host/v1/devices/{mac}/profile` | `devices:write` | body `{profile_id}` → reassigns the device (scheduler kick + routing rebuild + bus event) |
| `GET /host/v1/events` | `events:read` | Server-Sent Events stream of router events |

### Event stream

`GET /host/v1/events` is a long-lived `text/event-stream`. Each frame is
`event: <kind>` + `data: {kind, when, payload}`; a `: ping` comment
every 25s keeps it alive. Kinds include `device_joined`, `wan_status`,
`device_profile_changed`, `guest_session`, `update_available`. A plugin
subscribes once and reacts (see the reference plugin's live feed).

More endpoints and write scopes are added as the contract grows; the
permission model means a plugin only ever reaches what it asked for.

## Store

The installable catalog lives in a dedicated repo,
[bigtaed-sys/knot-os-plugins](https://github.com/bigtaed-sys/knot-os-plugins)
(`store.json`). knotd fetches it by default; override with
`knotd -plugins-index <url>`. A package signed by the firmware release
key installs as **official**; anything else needs explicit operator
confirmation. See that repo's README for the catalog format and how to
submit a plugin.

## Reference

`plugins/example-hello/` is a complete, dependency-free Go plugin: it
serves a page that reads live router state through the host API. Build
it with `cd plugins/example-hello && GOOS=linux GOARCH=arm64 go build
-o example-hello .` (the image build does this automatically and ships
only the manifest + binary).
