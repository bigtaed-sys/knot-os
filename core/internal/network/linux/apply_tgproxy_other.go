//go:build !linux

package linux

import (
	"context"

	"github.com/knot-os/knot-os/core/internal/tgproxy"
)

// TGProxyRunner is a no-op on non-Linux dev hosts: the sidecar proxy is
// only supervised on the real device.
type TGProxyRunner struct{}

// NewTGProxyRunner builds the no-op stub.
func NewTGProxyRunner() *TGProxyRunner { return &TGProxyRunner{} }

var _ tgproxy.Runner = (*TGProxyRunner)(nil)

func (r *TGProxyRunner) Start(_ context.Context, _ string, _ []string) error { return nil }
func (r *TGProxyRunner) Stop(_ context.Context) error                        { return nil }
func (r *TGProxyRunner) Running() bool                                       { return false }
