//go:build linux

package linux

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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
//
// Pre-flight: ensure the TUN module is loaded and IP forwarding is
// on. sing-box's auto_route does the iproute2 / nftables work once
// the TUN device is open, but it can't pull the kernel module out
// of nowhere.
func (r *SingBoxRunner) Start(ctx context.Context, confPath string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.proc != nil && r.proc.Running() {
		return nil
	}
	if err := ensureTUNAvailable(ctx); err != nil {
		return fmt.Errorf("sing-box: tun preflight: %w", err)
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

// ensureTUNAvailable loads the tun kernel module if /dev/net/tun
// isn't there yet, and turns on IPv4 forwarding (idempotent — Wi-Fi
// router mode already does this; we re-set it to be defensive on a
// freshly-boot LAN-router-only host).
//
// We tolerate modprobe failures softly: on Pi OS the tun module is
// always builtin or auto-loaded, but on a custom kernel it might
// be missing entirely. Sing-box will fail with a clearer error then
// than a generic "config rejected".
func ensureTUNAvailable(ctx context.Context) error {
	if _, err := os.Stat("/dev/net/tun"); err == nil {
		// Already there — module's loaded or builtin.
	} else {
		_ = exec.CommandContext(ctx, "modprobe", "tun").Run()
	}
	// Sing-box auto_route sets the iproute2 + nft rules but doesn't
	// flip net.ipv4.ip_forward; do it explicitly so the LAN→TUN→WAN
	// path forwards.
	_ = exec.CommandContext(ctx, "sysctl", "-w", "net.ipv4.ip_forward=1").Run()
	_ = exec.CommandContext(ctx, "sysctl", "-w", "net.ipv6.conf.all.forwarding=1").Run()
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
