//go:build !linux

package linux

import (
	"context"

	"github.com/knot-os/knot-os/core/internal/zapret"
)

// ZapretRunner is a no-op on non-Linux dev hosts: nfqws + nftables are
// Linux-only, so the manager renders config but starts nothing.
type ZapretRunner struct{}

// NewZapretRunner builds the no-op stub.
func NewZapretRunner() *ZapretRunner { return &ZapretRunner{} }

var _ zapret.Runner = (*ZapretRunner)(nil)

func (r *ZapretRunner) Start(_ context.Context, _ string, _ []string, _ string) error { return nil }
func (r *ZapretRunner) Stop(_ context.Context) error                                  { return nil }
func (r *ZapretRunner) Running() bool                                                 { return false }
