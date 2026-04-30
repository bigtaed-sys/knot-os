# Plugin API

> Status: stub. The contract is finalized in M6.

## Plugin shape (planned)

A plugin is a directory under `/usr/lib/knot/plugins/<id>/` containing:

```
<id>/
├── plugin.yaml      # manifest
├── <id>             # native binary (or .wasm in v0.2+)
└── ui/              # optional SvelteKit-built JS module mounted under /plugins/<id>/
```

## Manifest skeleton

```yaml
id: ru.example.hello
name: Hello Plugin
version: 0.1.0
runtime: native
permissions: []
hooks: []
ui:
  pages:
    - path: /hello
      entry: ./ui/index.js
api:
  exposes: /api/v1/hello
```

Permission, hook, and lifecycle definitions are documented during M6.
