package tgproxy

import (
	"context"
	"log"
	"strings"
	"sync"
)

// Runner supervises the tg-ws-proxy process. The real implementation
// lives in core/internal/network/linux; a no-op stub covers dev hosts.
type Runner interface {
	// Start (re)launches the proxy with args (it listens on the LAN
	// port itself). Implementations should be idempotent-friendly.
	Start(ctx context.Context, binPath string, args []string) error
	// Stop terminates the proxy.
	Stop(ctx context.Context) error
	// Running reports whether the proxy is up.
	Running() bool
}

// Manager owns the proxy lifecycle. Idle until Apply enables it.
type Manager struct {
	mu      sync.Mutex
	runner  Runner
	logger  *log.Logger
	base    string
	lastKey string // dedupe identical applies
}

// NewManager builds a Manager rooted at RuntimeDir. A nil runner makes
// Apply a no-op beyond binary bookkeeping (dev path).
func NewManager(runner Runner, logger *log.Logger) *Manager {
	if logger == nil {
		logger = log.Default()
	}
	return &Manager{runner: runner, logger: logger, base: RuntimeDir}
}

// WithBaseDir overrides the runtime root (tests).
func (m *Manager) WithBaseDir(dir string) *Manager { m.base = dir; return m }

// BaseDir returns the runtime root.
func (m *Manager) BaseDir() string { return m.base }

// Running reports whether the proxy is up.
func (m *Manager) Running() bool {
	if m.runner == nil {
		return false
	}
	return m.runner.Running()
}

// BinaryPresent reports whether a usable proxy binary is already on disk.
func (m *Manager) BinaryPresent() bool {
	_, ok := LocateBinary(m.base)
	return ok
}

// Apply reconciles the running proxy with s. Enabling ensures the binary
// is present (image copy or verified download) and starts it; disabling
// stops it.
func (m *Manager) Apply(ctx context.Context, s Settings) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !s.Enabled {
		m.lastKey = ""
		if m.runner != nil {
			return m.runner.Stop(ctx)
		}
		return nil
	}

	args := BuildArgs(s)
	key := strings.Join(args, "\x00")
	if m.runner == nil {
		m.lastKey = key
		return nil
	}
	if key == m.lastKey && m.runner.Running() {
		return nil // nothing changed and it's up
	}

	binPath, err := EnsureBinary(ctx, m.base)
	if err != nil {
		return err
	}
	if err := m.runner.Start(ctx, binPath, args); err != nil {
		return err
	}
	m.lastKey = key
	return nil
}
