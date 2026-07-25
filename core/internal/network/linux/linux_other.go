//go:build !linux

package linux

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/knot-os/knot-os/core/internal/config"
	"github.com/knot-os/knot-os/core/internal/network"
)

// LinuxBackend on a non-Linux host is a tombstone: it satisfies the
// network.Backend interface so the code compiles, but every operation
// returns an error. main.go only constructs it under -dev=false on
// Linux, so this stub should never see traffic in production.
type LinuxBackend struct {
	HTTPPort int
}

// Options mirrors the linux-build Options.
type Options struct {
	Logger   *log.Logger
	HTTPPort int
}

// New on a non-Linux host returns a placeholder.
func New(opts Options) *LinuxBackend { return &LinuxBackend{HTTPPort: opts.HTTPPort} }

// Name implements network.Backend.
func (b *LinuxBackend) Name() string { return "linux" }

// Init returns an error on every non-Linux platform.
func (b *LinuxBackend) Init(_ context.Context) error {
	return errors.New("LinuxBackend is only available on Linux")
}

// Apply returns an error on every non-Linux platform.
func (b *LinuxBackend) Apply(_ context.Context, _ config.Config) error {
	return errors.New("LinuxBackend is only available on Linux")
}

// Status returns an error on every non-Linux platform.
func (b *LinuxBackend) Status(_ context.Context) (network.Status, error) {
	return network.Status{}, errors.New("LinuxBackend is only available on Linux")
}

// Scan returns an error on every non-Linux platform.
func (b *LinuxBackend) Scan(_ context.Context) ([]network.ScannedNetwork, error) {
	return nil, errors.New("LinuxBackend is only available on Linux")
}

// Close is a no-op on non-Linux platforms.
func (b *LinuxBackend) Close() {}

// UpdateBlockedMACs returns an error on non-Linux platforms.
// Production callers should use the scheduler's nop-updater fallback
// when running with the mock backend.
func (b *LinuxBackend) UpdateBlockedMACs(_ []string) error {
	return errors.New("LinuxBackend is only available on Linux")
}

// SetGuestProvider is a no-op on non-Linux platforms — the apply
// paths that consume it are linux-only anyway.
func (b *LinuxBackend) SetGuestProvider(_ GuestSessionProvider) {}

// LiveModemIface has no meaning off Linux (no ModemManager).
func (b *LinuxBackend) LiveModemIface() string { return "" }

// SetModemObserver is a no-op off Linux (no cellular metrics source).
func (b *LinuxBackend) SetModemObserver(_ func(at time.Time, iface string, rxBytes, txBytes uint64, signal int)) {
}
