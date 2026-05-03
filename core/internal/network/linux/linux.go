//go:build linux

package linux

import (
	"context"
	"errors"
	"log"
	"os"
	"sync"

	"github.com/knot-os/knot-os/core/internal/config"
	"github.com/knot-os/knot-os/core/internal/network"
)

// LinuxBackend is the production network.Backend implementation. It
// orchestrates hostapd, wpa_supplicant, dnsmasq, and nftables on a
// real Linux system to realize the desired role.
//
// Lifecycle:
//   - Build a Backend with New(). Nothing runs yet.
//   - Init prepares /run/knot, removes stale ap0, etc. Called once.
//   - Apply transitions to the requested config. Idempotent.
//   - Status reports current runtime state.
//   - Scan returns visible Wi-Fi networks. May briefly disrupt ap0.
//   - Close stops every supervised process (used on shutdown).
type LinuxBackend struct {
	logger *log.Logger
	r      *runner

	// HTTPPort is the port knotd's HTTP server is listening on. The
	// captive-portal nftables rules redirect 80/443 to this port.
	HTTPPort int

	// supervised processes
	hostapd      *supervisedProc
	wpaSupp      *supervisedProc
	dnsmasq      *supervisedProc

	mu      sync.Mutex
	current config.Config
	hasCfg  bool
}

// Options configures New.
type Options struct {
	Logger   *log.Logger
	HTTPPort int
}

// New constructs a LinuxBackend. It does not touch the system; call
// Init before the first Apply.
func New(opts Options) *LinuxBackend {
	if opts.Logger == nil {
		opts.Logger = log.Default()
	}
	if opts.HTTPPort == 0 {
		opts.HTTPPort = 80
	}
	return &LinuxBackend{
		logger:   opts.Logger,
		r:        newRunner(opts.Logger),
		HTTPPort: opts.HTTPPort,
	}
}

// Name implements network.Backend.
func (b *LinuxBackend) Name() string { return "linux" }

// Init prepares the host: ensures /run/knot exists, removes any stale
// ap0 from a previous knotd run, and verifies wlan0 is present.
func (b *LinuxBackend) Init(ctx context.Context) error {
	if err := os.MkdirAll(RuntimeDir, 0o755); err != nil {
		return err
	}
	if !interfaceExists(IfaceWlan) {
		return errors.New("Linux backend: wlan0 not present — Wi-Fi hardware missing or not yet enumerated")
	}
	// A previous run may have left ap0 around. Wipe it so we start
	// from a known state; ensureAP rebuilds it inside Apply.
	b.removeAP(ctx)
	return nil
}

// Apply implements network.Backend. The role-specific orchestration is
// in apply_setup.go and apply_extender.go (M5c/M5d).
func (b *LinuxBackend) Apply(ctx context.Context, cfg config.Config) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch cfg.Role {
	case config.RoleSetup:
		if err := b.applySetup(ctx, cfg); err != nil {
			return err
		}
	case config.RoleWiFiExtender:
		if err := b.applyExtender(ctx, cfg); err != nil {
			return err
		}
	case config.RoleWiFiRouter:
		if err := b.applyRouter(ctx, cfg); err != nil {
			return err
		}
	default:
		return errors.New("LinuxBackend.Apply: unsupported role " + string(cfg.Role))
	}
	b.current = cfg
	b.hasCfg = true
	return nil
}

// Status implements network.Backend. Live-state probing of hostapd /
// wpa_supplicant lands in M5d alongside the extender role; for now we
// report process-up booleans.
func (b *LinuxBackend) Status(_ context.Context) (network.Status, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	st := network.Status{Backend: b.Name()}
	if !b.hasCfg {
		st.Role = config.RoleSetup
		return st, nil
	}
	st.Role = b.current.Role
	if b.current.Network.Uplink != nil {
		st.Uplink = &network.UplinkStatus{
			SSID:      b.current.Network.Uplink.SSID,
			Connected: b.wpaSupp != nil && b.wpaSupp.Running(),
		}
	}
	if b.current.Network.AP != nil {
		st.AP = &network.APStatus{
			SSID: b.current.Network.AP.SSID,
			Up:   b.hostapd != nil && b.hostapd.Running(),
		}
	}
	return st, nil
}

// Scan is implemented in scan.go.

// Close stops every supervised daemon. Intended for shutdown only.
func (b *LinuxBackend) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, p := range []*supervisedProc{b.hostapd, b.wpaSupp, b.dnsmasq} {
		if p != nil {
			p.Stop()
		}
	}
}
