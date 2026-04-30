//go:build linux

package linux

import (
	"context"
	"errors"

	"github.com/knot-os/knot-os/core/internal/config"
)

// applyExtender transitions the host into the wifi-extender role.
// Implementation lands in M5d.
func (b *LinuxBackend) applyExtender(_ context.Context, _ config.Config) error {
	return errors.New("LinuxBackend.applyExtender: not yet implemented (M5d)")
}
