//go:build linux

package linux

import (
	"context"
	"fmt"
	"sync"
	"syscall"

	"github.com/knot-os/knot-os/core/internal/singbox"
)

// SingBoxRunner is the Linux supervisor for the sing-box client
// process. Implements the singbox.Runner interface so the
// platform-agnostic Manager (in core/internal/singbox) can drive
// the lifecycle without pulling syscall imports into that package.
//
// One process per device; we don't run multiple sing-box instances.
// Reload is implemented via SIGHUP — sing-box ≥ 1.5 picks up the
// new config from disk. On older versions / SIGHUP failure the
// caller (Manager) falls back to Stop+Start.
type SingBoxRunner struct {
	mu   sync.Mutex
	proc *supervisedProc
}

// NewSingBoxRunner builds an empty supervisor. Start populates
// the inner process; Stop tears it down.
func NewSingBoxRunner() *SingBoxRunner {
	return &SingBoxRunner{}
}

// Compile-time check that SingBoxRunner satisfies the contract
// the singbox.Manager calls into. If sing-box's Runner shape
// drifts, this fails the build instead of an obscure runtime
// nil-method panic.
var _ singbox.Runner = (*SingBoxRunner)(nil)

// Start launches sing-box with the given config path. No-op when
// already running — Manager calls Reload for that case.
func (r *SingBoxRunner) Start(ctx context.Context, confPath string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.proc != nil && r.proc.Running() {
		return nil
	}
	// `-D /run/knot` keeps any working files (clash UI cache, etc.)
	// inside the tmpfs runtime dir so they get wiped on reboot.
	r.proc = newSupervisedProc(
		"sing-box",
		singbox.BinPath,
		"run",
		"-c", confPath,
		"-D", RuntimeDir,
	)
	if err := r.proc.Start(ctx); err != nil {
		r.proc = nil
		return fmt.Errorf("sing-box: start: %w", err)
	}
	return nil
}

// Reload sends SIGHUP to the sing-box process. Confidence is
// medium — older builds will ignore the signal, in which case
// Manager falls back to a full Stop+Start.
func (r *SingBoxRunner) Reload(_ context.Context, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.proc == nil || !r.proc.Running() {
		return fmt.Errorf("sing-box: not running")
	}
	r.proc.mu.Lock()
	cmd := r.proc.cmd
	r.proc.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return fmt.Errorf("sing-box: no process handle")
	}
	if err := cmd.Process.Signal(syscall.SIGHUP); err != nil {
		return fmt.Errorf("sing-box: SIGHUP: %w", err)
	}
	return nil
}

// Stop terminates the sing-box process group. Best-effort.
func (r *SingBoxRunner) Stop(_ context.Context) error {
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
func (r *SingBoxRunner) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.proc == nil {
		return false
	}
	return r.proc.Running()
}
