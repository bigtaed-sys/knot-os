//go:build linux

package linux

import (
	"context"
	"fmt"
	"sync"

	"github.com/knot-os/knot-os/core/internal/tgproxy"
)

// TGProxyRunner supervises the tg-ws-proxy sidecar. It implements
// tgproxy.Runner so the platform-agnostic tgproxy.Manager can drive the
// lifecycle without exec imports. The proxy is a plain userspace LAN
// listener — no nftables, so it can't affect NAT/forwarding.
type TGProxyRunner struct {
	mu   sync.Mutex
	proc *supervisedProc
}

// NewTGProxyRunner builds an empty supervisor.
func NewTGProxyRunner() *TGProxyRunner { return &TGProxyRunner{} }

var _ tgproxy.Runner = (*TGProxyRunner)(nil)

// Start (re)launches the proxy with args.
func (r *TGProxyRunner) Start(ctx context.Context, binPath string, args []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.proc != nil {
		r.proc.Stop()
		r.proc = nil
	}
	r.proc = newSupervisedProc("tg-ws-proxy", binPath, args...)
	if err := r.proc.Start(ctx); err != nil {
		r.proc = nil
		return fmt.Errorf("tgproxy: start: %w", err)
	}
	return nil
}

// Stop terminates the proxy. Best-effort.
func (r *TGProxyRunner) Stop(_ context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.proc != nil {
		r.proc.Stop()
		r.proc = nil
	}
	return nil
}

// Running reports the supervisor's view of the proxy.
func (r *TGProxyRunner) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.proc != nil && r.proc.Running()
}
