//go:build !linux

package linux

import (
	"context"

	"github.com/knot-os/knot-os/core/internal/xray"
)

// Compile-time check on dev hosts too.
var _ xray.Runner = (*XrayRunner)(nil)

// XrayRunner stub for non-Linux dev hosts. Implements xray.Runner so
// main.go can construct one without build-tag gymnastics; every
// method is a no-op since the binary only exists in the Pi image.
type XrayRunner struct{}

// NewXrayRunner returns a no-op runner.
func NewXrayRunner() *XrayRunner { return &XrayRunner{} }

// Start is a no-op on non-Linux.
func (r *XrayRunner) Start(_ context.Context, _ string) error { return nil }

// Reload is a no-op on non-Linux.
func (r *XrayRunner) Reload(_ context.Context, _ string) error { return nil }

// Stop is a no-op on non-Linux.
func (r *XrayRunner) Stop(_ context.Context) error { return nil }

// Running always returns false on non-Linux.
func (r *XrayRunner) Running() bool { return false }
