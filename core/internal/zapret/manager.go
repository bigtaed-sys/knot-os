package zapret

import (
	"context"
	"log"
	"strings"
	"sync"
)

// Runner is the platform-specific hook that owns both the nfqws process
// and its isolated nftables queue rule. The real implementation lives
// in core/internal/network/linux; a no-op stub covers dev hosts.
type Runner interface {
	// Start applies the nft queue rule for wanIface and (re)starts
	// nfqws with args. Implementations should be idempotent-friendly:
	// the Manager only calls Start when the effective config changed.
	Start(ctx context.Context, binPath string, args []string, wanIface string) error
	// Stop tears down the nft rule and stops nfqws.
	Stop(ctx context.Context) error
	// Running reports whether nfqws is currently up.
	Running() bool
}

// Manager owns the engine lifecycle. Idle until Apply is called with an
// enabled Settings that has a WAN interface; disabling stops nfqws and
// removes the nft hook.
type Manager struct {
	mu      sync.Mutex
	runner  Runner
	logger  *log.Logger
	base    string
	lastKey string // dedupe identical applies (args+iface)
}

// NewManager builds a Manager rooted at RuntimeDir. A nil runner makes
// Apply a no-op beyond asset seeding (dev path).
func NewManager(runner Runner, logger *log.Logger) *Manager {
	if logger == nil {
		logger = log.Default()
	}
	return &Manager{runner: runner, logger: logger, base: RuntimeDir}
}

// WithBaseDir overrides the runtime/asset root (tests).
func (m *Manager) WithBaseDir(dir string) *Manager { m.base = dir; return m }

// BaseDir returns the runtime/asset root.
func (m *Manager) BaseDir() string { return m.base }

// Running reports whether nfqws is currently up.
func (m *Manager) Running() bool {
	if m.runner == nil {
		return false
	}
	return m.runner.Running()
}

// BinaryPresent reports whether a usable nfqws binary is already on
// disk (image-staged or previously downloaded) — the UI surfaces this
// so the user knows whether enabling will trigger a download first.
func (m *Manager) BinaryPresent() bool {
	_, ok := LocateBinary(m.base)
	return ok
}

// Apply reconciles the running engine with s. Enabling seeds assets,
// ensures the binary is present (image copy or verified download),
// renders the strategy args, and starts nfqws + its nft hook. Disabling
// (or no WAN interface) stops everything.
func (m *Manager) Apply(ctx context.Context, s Settings) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !s.Enabled || s.WANInterface == "" {
		m.lastKey = ""
		if m.runner != nil {
			return m.runner.Stop(ctx)
		}
		return nil
	}

	// Seed first-run assets (no-op when already on disk).
	if _, err := MaterializeAssets(m.base); err != nil {
		return err
	}

	args, err := RenderArgs(s, m.base)
	if err != nil {
		return err
	}

	key := s.WANInterface + "\x00" + strings.Join(args, "\x00")
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
	if err := m.runner.Start(ctx, binPath, args, s.WANInterface); err != nil {
		return err
	}
	m.lastKey = key
	return nil
}
