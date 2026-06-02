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
  | `KNOT_HOST_SOCKET` | Unix socket of knotd's host API |
  | `KNOT_HOST_TOKEN` | bearer token to authenticate host-API calls |

## Serving UI / API

The plugin runs an HTTP server on `KNOT_PLUGIN_SOCKET`. knotd
reverse-proxies it (auth-gated, operators only) at:

```
/api/plugins/<id>/proxy/<path>   →   http://<plugin-socket>/<path>
```

The UI opens it in an iframe at the SPA route `/plugins/<id>`. The
proxied request carries `X-Knot-Plugin-Base` so the plugin can build
correct self-links if needed.

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

## Reference

`plugins/example-hello/` is a complete, dependency-free Go plugin: it
serves a page that reads live router state through the host API. Build
it with `cd plugins/example-hello && GOOS=linux GOARCH=arm64 go build
-o example-hello .` (the image build does this automatically and ships
only the manifest + binary).
