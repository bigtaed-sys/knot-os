//go:build linux

package linux

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// supervisedProc is a long-running child process (hostapd, dnsmasq,
// wpa_supplicant) owned by knotd. The process is started by Start,
// stopped by Stop, and Restarted by — well, Restart.
//
// We deliberately do not auto-restart on crash in v0.1: a crashed
// hostapd usually means a misconfiguration, and silently respawning
// would mask the bug. The dashboard surfaces "AP down" via
// /api/status and a future M9 will add health monitoring.
type supervisedProc struct {
	name string
	bin  string
	args []string

	mu  sync.Mutex
	cmd *exec.Cmd
}

func newSupervisedProc(name, bin string, args ...string) *supervisedProc {
	return &supervisedProc{name: name, bin: bin, args: args}
}

// Start launches the process. Calling Start when one is already
// running is an error.
func (p *supervisedProc) Start(_ context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd != nil {
		return fmt.Errorf("%s: already running (pid %d)", p.name, p.cmd.Process.Pid)
	}

	// We do not pass the caller's ctx to exec.CommandContext because
	// these processes need to outlive a single Apply call — they run
	// for the lifetime of the role until Stop is called.
	cmd := exec.Command(p.bin, p.args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: start: %w", p.name, err)
	}
	p.cmd = cmd
	return nil
}

// Stop sends SIGTERM, waits up to 5s, then SIGKILL. Always succeeds —
// returns nil if the process was already gone.
func (p *supervisedProc) Stop() {
	p.mu.Lock()
	cmd := p.cmd
	p.cmd = nil
	p.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}

	// Negative PID kills the whole process group (Setpgid above).
	pgid := cmd.Process.Pid
	_ = syscall.Kill(-pgid, syscall.SIGTERM)

	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-done
	}
}

// Running reports whether the process is currently believed to be alive.
func (p *supervisedProc) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd == nil || p.cmd.Process == nil {
		return false
	}
	// Sending signal 0 returns nil if the process exists and we have
	// permission to signal it; ESRCH means it's gone.
	return p.cmd.Process.Signal(syscall.Signal(0)) == nil
}

// Restart stops and starts the process. Equivalent to Stop+Start with
// no exposed window where the proc is half-started.
func (p *supervisedProc) Restart(ctx context.Context) error {
	p.Stop()
	return p.Start(ctx)
}
