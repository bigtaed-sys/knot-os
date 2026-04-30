# Plugins

This directory holds plugins shipped with KnotOS. The image build copies every subdirectory here into `/usr/lib/knot/plugins/` on the device, where `knotd` discovers them on startup.

## Layout

```
plugins/
└── <id>/
    └── plugin.yaml      # required manifest
```

Subdirectories that lack a valid `plugin.yaml` are ignored. `id` in the manifest must equal the directory name; mismatches are rejected so URL paths (`/api/plugins/<id>`) stay predictable.

## Manifest

```yaml
id: example-hello
name: Hello Plugin
version: 0.1.0
description: One-or-two-line summary shown on the plugin list.
```

| Field | Required | Notes |
|-------|----------|-------|
| `id` | yes | Letters, digits, `.-_`. Must match the directory name. |
| `name` | yes | Human-readable label. |
| `version` | yes | Free-form, conventionally semver. |
| `description` | no | Shown on the plugin list in the UI. |

## v0.1 scope

A plugin in v0.1 is metadata only. It can be enabled/disabled from the UI and the state persists across reboots, but no plugin code runs yet. The actual runtime (gRPC contract, sandboxing, UI extension points) lands in v0.2.
