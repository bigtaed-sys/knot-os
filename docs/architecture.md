# Architecture

> Status: skeleton. This document is a stub for v0.1 and will be filled in alongside M2–M5.

## Overview

KnotOS is a single Go daemon (`knotd`) plus a static SvelteKit UI embedded into the daemon binary. The daemon owns the configuration, applies it to the Linux networking stack via dedicated backends, and hosts plugins over a gRPC interface.

```
┌─────────────────────────────────────────────────────┐
│  Web browser (phone/laptop on the LAN)              │
│  ↓ HTTP / WebSocket                                 │
│ ┌─────────────────────────────────────────────────┐ │
│ │  knotd  (Go, single binary)                     │ │
│ │  ├── REST + WS API                              │ │
│ │  ├── Embedded SvelteKit static bundle           │ │
│ │  ├── Config engine  (YAML + git snapshots)      │ │
│ │  ├── Role engine + capability detection         │ │
│ │  ├── Network backends (hostapd, wpa_sup,        │ │
│ │  │     dnsmasq, nftables, netlink)              │ │
│ │  └── Plugin host (gRPC over Unix socket)        │ │
│ └─────────────────────────────────────────────────┘ │
│  ↓ gRPC                                             │
│ ┌─────────────────────────────────────────────────┐ │
│ │  Plugins (separate processes under systemd)     │ │
│ └─────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────┘
```

## Where to read more

- The v0.1 implementation plan lives outside the repo (in the planning workspace) and will be turned into milestone issues during M1.
- Plugin contract: see [plugin-api.md](plugin-api.md) (stub).
- Image build: see [building.md](building.md) (stub).
