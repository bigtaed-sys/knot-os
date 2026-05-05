//go:build linux

package linux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/knot-os/knot-os/core/internal/vpn"
)

// WireGuardConfPath is where wg-quick reads its config from. Using
// the canonical location means a recovery user can also bring the
// interface up by hand with `wg-quick up wg0`.
const WireGuardConfPath = "/etc/wireguard/wg0.conf"

// WireGuardIface is the interface name we manage. Hard-coded
// because wg-quick's unit-template expects one interface per
// service (`wg-quick@wg0.service`); supporting multiple wg
// interfaces is a v0.5 problem.
const WireGuardIface = "wg0"

// ApplyWireGuard writes the wg-quick config and brings the
// interface up. Calling with cfg.Enabled=false tears it down.
//
// Idempotent. Safe to call on every Apply: wg-quick handles a
// running interface gracefully (`wg syncconf` under the hood).
func (b *LinuxBackend) ApplyWireGuard(ctx context.Context, srv vpn.ServerConfig, peers []vpn.Peer) error {
	if !srv.Enabled {
		return b.tearDownWireGuard(ctx)
	}
	if srv.PrivateKey == (vpn.Key{}) {
		return errors.New("applyWG: server private key missing")
	}
	conf := vpn.RenderServerConf(srv, peers)
	if err := writeWireGuardConf(conf); err != nil {
		return fmt.Errorf("applyWG: write conf: %w", err)
	}

	// Bring it up. `wg-quick up wg0` is a one-shot — running it
	// twice fails with "Interface already exists". So we strip-and-
	// re-add via `down` (which is no-op when nothing is up). For an
	// already-running interface we use `wg syncconf` instead, which
	// applies the new conf without dropping the listener — but
	// wg-quick conf format isn't directly accepted by `wg setconf`,
	// so we strip it via wg-quick's own helper.
	if interfaceExists(WireGuardIface) {
		// Sync without restart. wg-quick ships /usr/bin/wg with the
		// "strip" subcommand which converts the wg-quick file into
		// the wg setconf format on stdout.
		if err := b.r.runOK(ctx, "bash", "-c",
			"wg syncconf "+WireGuardIface+" <(wg-quick strip "+WireGuardConfPath+")"); err != nil {
			// Fall back to a full down+up cycle.
			b.r.runIgnoreError(ctx, "wg-quick", "down", WireGuardIface)
			if err := b.r.runOK(ctx, "wg-quick", "up", WireGuardIface); err != nil {
				return fmt.Errorf("applyWG: wg-quick up after sync fail: %w", err)
			}
		}
		return nil
	}
	if err := b.r.runOK(ctx, "wg-quick", "up", WireGuardIface); err != nil {
		return fmt.Errorf("applyWG: wg-quick up: %w", err)
	}
	return nil
}

func (b *LinuxBackend) tearDownWireGuard(ctx context.Context) error {
	if !interfaceExists(WireGuardIface) {
		return nil
	}
	b.r.runIgnoreError(ctx, "wg-quick", "down", WireGuardIface)
	return nil
}

func writeWireGuardConf(content string) error {
	dir := filepath.Dir(WireGuardConfPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".wg0-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	return os.Rename(tmpName, WireGuardConfPath)
}
