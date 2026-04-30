//go:build linux

package linux

import (
	"context"
	"errors"

	"github.com/knot-os/knot-os/core/internal/config"
)

// applySetup transitions the host into the open-AP onboarding role.
// Implementation lands in M5c.
func (b *LinuxBackend) applySetup(_ context.Context, _ config.Config) error {
	return errors.New("LinuxBackend.applySetup: not yet implemented (M5c)")
}
