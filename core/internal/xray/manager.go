package xray

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// Runner is the platform-specific process hook, mirroring
// singbox.Runner. Real implementation lives in
// core/internal/network/linux; a no-op stub covers dev hosts.
type Runner interface {
	Start(ctx context.Context, confPath string) error
	Reload(ctx context.Context, confPath string) error
	Stop(ctx context.Context) error
	Running() bool
}

// Manager owns the rendered Xray config + process lifecycle. Idle
// until the first Apply with at least one upstream; later Applies
// hot-reload (Xray has no SIGHUP reload, so the Linux runner falls
// back to restart — fine, it only happens when servers change).
type Manager struct {
	mu       sync.Mutex
	cfg      Config
	hasCfg   bool
	runner   Runner
	logger   *log.Logger
	confPath string
}

// NewManager builds a Manager. A nil runner renders config to disk
// but never starts a process (dev path).
func NewManager(runner Runner, logger *log.Logger) *Manager {
	if logger == nil {
		logger = log.Default()
	}
	return &Manager{runner: runner, logger: logger, confPath: ConfigPath}
}

// WithConfigPath overrides the rendered-file location (tests).
func (m *Manager) WithConfigPath(p string) { m.confPath = p }

// ConfigPath returns the live rendered-file path.
func (m *Manager) ConfigPath() string { return m.confPath }

// Apply renders the config to disk and starts / reloads / stops the
// runner to match. When there are no upstreams the process is
// stopped (nothing for Xray to do — sing-box handles everything it
// can natively).
func (m *Manager) Apply(ctx context.Context, cfg Config) error {
	js, err := cfg.RenderJSON()
	if err != nil {
		return fmt.Errorf("xray: render: %w", err)
	}
	if err := m.writeConfig(js); err != nil {
		return fmt.Errorf("xray: write %s: %w", m.confPath, err)
	}

	m.mu.Lock()
	prev := m.cfg
	prevHad := m.hasCfg
	m.cfg = cfg
	m.hasCfg = true
	confPath := m.confPath
	m.mu.Unlock()

	wantRun := cfg.HasUpstreams()
	prevRun := prevHad && prev.HasUpstreams()

	if m.runner == nil {
		return nil
	}

	switch {
	case !wantRun && prevRun:
		if err := m.runner.Stop(ctx); err != nil {
			m.logger.Printf("xray: stop: %v", err)
		}
	case wantRun && !m.runner.Running():
		if err := m.runner.Start(ctx, confPath); err != nil {
			return fmt.Errorf("xray: start: %w", err)
		}
		m.logger.Printf("xray: started with %d upstreams", len(cfg.Upstreams))
	case wantRun && m.runner.Running():
		if err := m.runner.Reload(ctx, confPath); err != nil {
			m.logger.Printf("xray: reload (falling back to restart): %v", err)
			_ = m.runner.Stop(ctx)
			if err := m.runner.Start(ctx, confPath); err != nil {
				return fmt.Errorf("xray: restart after failed reload: %w", err)
			}
		}
	}
	return nil
}

// Stop terminates the process at daemon shutdown.
func (m *Manager) Stop(ctx context.Context) error {
	if m.runner == nil {
		return nil
	}
	return m.runner.Stop(ctx)
}

// Running reports the supervisor's view of the process.
func (m *Manager) Running() bool {
	if m.runner == nil {
		return false
	}
	return m.runner.Running()
}

func (m *Manager) writeConfig(data []byte) error {
	dir := filepath.Dir(m.confPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".xray-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	return os.Rename(tmpName, m.confPath)
}
