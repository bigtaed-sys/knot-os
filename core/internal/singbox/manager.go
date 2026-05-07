package singbox

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// Runner is the platform-specific hook the Manager uses to start
// / stop / signal the sing-box process. Implementations live in
// core/internal/network/linux (real supervisor) and a no-op stub
// for non-Linux dev builds.
//
// Decoupling here keeps the singbox package linux-clean — config
// rendering + state management has no syscall dependencies and
// can be tested on any host.
type Runner interface {
	// Start launches the sing-box process with the rendered
	// config at confPath. Idempotent: a second call while
	// already running is a no-op (the caller should use Reload
	// instead).
	Start(ctx context.Context, confPath string) error

	// Reload tells the running process to re-read its config
	// (SIGHUP on Linux ≥ 1.5 sing-box; full restart fallback
	// for older versions).
	Reload(ctx context.Context, confPath string) error

	// Stop terminates the process. Best-effort.
	Stop(ctx context.Context) error

	// Running reports whether the supervisor believes the
	// process is alive right now.
	Running() bool
}

// Manager owns the rendered config + lifecycle. Construct via
// NewManager; subsequent Apply calls swap the config and signal
// the runner.
//
// Idle when no Apply has been called yet — the runner stays
// stopped. First Apply with a non-empty Config starts it; later
// Applies hot-reload it.
type Manager struct {
	mu       sync.Mutex
	cfg      Config
	hasCfg   bool
	runner   Runner
	logger   *log.Logger
	confPath string // full path to the rendered file (overridable in tests)
}

// NewManager builds a Manager. runner can be nil — in that case
// Apply just renders the config to disk for inspection but never
// tries to start a process. Useful for the dev path on a laptop.
func NewManager(runner Runner, logger *log.Logger) *Manager {
	if logger == nil {
		logger = log.Default()
	}
	return &Manager{
		runner:   runner,
		logger:   logger,
		confPath: ConfigPath,
	}
}

// WithConfigPath swaps the rendered-file location, mostly for
// tests that don't want to write into /run/knot/.
func (m *Manager) WithConfigPath(p string) { m.confPath = p }

// ConfigPath returns the path the manager writes its config to.
// Live, not the package-level constant — tests can override it.
func (m *Manager) ConfigPath() string { return m.confPath }

// Apply writes the rendered config to disk and starts / reloads
// the runner. The renderer's deterministic output means a
// no-op apply (same Config as last time) leaves the file's
// mtime unchanged — useful for SIGHUP-on-change semantics.
//
// When cfg.Outbounds is empty (no real servers configured yet),
// Apply still writes the trivial-direct config but does NOT start
// the runner. Sing-box has nothing useful to do in that state.
func (m *Manager) Apply(ctx context.Context, cfg Config) error {
	js, err := cfg.RenderJSON()
	if err != nil {
		return fmt.Errorf("singbox: render: %w", err)
	}
	if err := m.writeConfig(js); err != nil {
		return fmt.Errorf("singbox: write %s: %w", m.confPath, err)
	}

	m.mu.Lock()
	prev := m.cfg
	prevHad := m.hasCfg
	m.cfg = cfg
	m.hasCfg = true
	confPath := m.confPath
	m.mu.Unlock()

	wantRun := hasUserOutbounds(cfg)
	prevRun := prevHad && hasUserOutbounds(prev)

	if m.runner == nil {
		return nil
	}

	switch {
	case !wantRun && prevRun:
		if err := m.runner.Stop(ctx); err != nil {
			m.logger.Printf("singbox: stop: %v", err)
		}
	case wantRun && !m.runner.Running():
		if err := m.runner.Start(ctx, confPath); err != nil {
			return fmt.Errorf("singbox: start: %w", err)
		}
		m.logger.Printf("singbox: started with %d user outbounds", userOutboundCount(cfg))
	case wantRun && m.runner.Running():
		if err := m.runner.Reload(ctx, confPath); err != nil {
			m.logger.Printf("singbox: reload (falling back to restart): %v", err)
			_ = m.runner.Stop(ctx)
			if err := m.runner.Start(ctx, confPath); err != nil {
				return fmt.Errorf("singbox: restart after failed reload: %w", err)
			}
		}
	}
	return nil
}

// Stop terminates the sing-box process. Used at daemon shutdown.
func (m *Manager) Stop(ctx context.Context) error {
	if m.runner == nil {
		return nil
	}
	return m.runner.Stop(ctx)
}

// Snapshot returns a copy of the active config. Empty Config{}
// before the first Apply.
func (m *Manager) Snapshot() Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg
}

// Running reports whether the supervised process is currently up.
// Always false on a Manager with a nil runner.
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
	tmp, err := os.CreateTemp(dir, ".sing-box-*.tmp")
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

// hasUserOutbounds is true when the Config contains at least one
// real-server outbound (i.e. anything other than the auto-added
// direct/block/dns-out built-ins). The supervisor only starts
// sing-box when there's an actual job for it.
func hasUserOutbounds(c Config) bool {
	return userOutboundCount(c) > 0
}

func userOutboundCount(c Config) int {
	n := 0
	for _, o := range c.Outbounds {
		switch o.Type {
		case OutboundDirect, OutboundBlock:
			continue
		}
		n++
	}
	return n
}

// ErrNotRunning is returned by methods that require the runner to
// be active — e.g. live status pulls from the clash API.
var ErrNotRunning = errors.New("singbox: not running")
