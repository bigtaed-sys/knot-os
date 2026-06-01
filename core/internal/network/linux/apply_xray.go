//go:build linux

package linux

import (
	"context"
	"fmt"
	"sync"

	"github.com/knot-os/knot-os/core/internal/xray"
)

// XrayRunner supervises the Xray-core process. It implements
// xray.Runner so the platform-agnostic xray.Manager can drive the
// lifecycle without syscall imports.
//
// Unlike sing-box, Xray-core has no SIGHUP config reload, so Reload
// is a clean Stop+Start. That only happens when the set of upstream
// servers changes, which is rare, so the brief restart is fine —
// sing-box keeps the TUN up throughout; only the loopback SOCKS hop
// blips.
type XrayRunner struct {
	mu   sync.Mutex
	proc *supervisedProc
}

// NewXrayRunner builds an empty supervisor.
func NewXrayRunner() *XrayRunner { return &XrayRunner{} }

var _ xray.Runner = (*XrayRunner)(nil)

// Start launches `xray run -c <confPath>`. No-op when already up.
func (r *XrayRunner) Start(ctx context.Context, confPath string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.proc != nil && r.proc.Running() {
		return nil
	}
	r.proc = newSupervisedProc(
		"xray",
		xray.BinPath,
		"run",
		"-c", confPath,
	)
	if err := r.proc.Start(ctx); err != nil {
		r.proc = nil
		return fmt.Errorf("xray: start: %w", err)
	}
	return nil
}

// Reload restarts the process — Xray can't hot-reload its config.
func (r *XrayRunner) Reload(ctx context.Context, confPath string) error {
	r.Stop(ctx)
	return r.Start(ctx, confPath)
}

// Stop terminates the process group. Best-effort.
func (r *XrayRunner) Stop(_ context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.proc == nil {
		return nil
	}
	r.proc.Stop()
	r.proc = nil
	return nil
}

// Running reports the supervisor's view of the process state.
func (r *XrayRunner) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.proc == nil {
		return false
	}
	return r.proc.Running()
}
