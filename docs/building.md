# Building

> Status: stub. Full instructions land in M7.

## Local development (no hardware)

Requires Go 1.22+ and Node 18+. Works on Linux, macOS, or Windows (with WSL2 for the eventual image build, but not for daemon/UI development).

```bash
# Build daemon and CLI
go build -o ./dist/knotd ./core/cmd/knotd
go build -o ./dist/knotctl ./cli/cmd/knotctl

# Build UI
cd ui
npm install
npm run build
cd ..

# Run daemon in dev mode
./dist/knotd -dev
```

## Image build (Raspberry Pi Zero 2W)

> Not wired up yet — added in M7.

The image is produced via `pi-gen` with a custom stage `image/stage-knot/`. Builds run inside WSL2 (Ubuntu 22.04+). The output is `image/deploy/KnotOS-zero2w-vX.Y.Z.img.xz`, flashable with Raspberry Pi Imager.
