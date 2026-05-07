//go:build !linux

package linux

import (
	"context"

	"github.com/knot-os/knot-os/core/internal/singbox"
)

// Compile-time check on dev hosts too.
var _ singbox.Runner = (*SingBoxRunner)(nil)

// SingBoxRunner stub for non-Linux dev hosts. Implements the
// singbox.Runner interface so main.go can construct one without
// build-tag gymnastics, but Start always errors out: the binary
// only exists in the Pi image.
type SingBoxRunner struct{}

// NewSingBoxRunner returns a no-op runner.
func NewSingBoxRunner() *SingBoxRunner { return &SingBoxRunner{} }

// Start is a no-op on non-Linux. Returns nil so a dev-host Apply
// can render the config to disk for inspection without crashing.
func (r *SingBoxRunner) Start(_ context.Context, _ string) error { return nil }

// Reload is a no-op on non-Linux.
func (r *SingBoxRunner) Reload(_ context.Context, _ string) error { return nil }

// Stop is a no-op on non-Linux.
func (r *SingBoxRunner) Stop(_ context.Context) error { return nil }

// Running always returns false on non-Linux.
func (r *SingBoxRunner) Running() bool { return false }
