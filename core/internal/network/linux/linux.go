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

	// guestProvider, when non-nil, is queried on every Apply for
	// the currently-active guest session. The session drives a
	// secondary BSS in hostapd and isolation rules in nftables.
	// Decoupled as an interface so the linux backend has no import
	// dependency on the guest package itself.
	guestProvider GuestSessionProvider

	mu      sync.Mutex
	current config.Config
	hasCfg  bool

	// modemErr holds the reason the last cellular connect failed, so
	// ModemStatus can report it to the UI. Guarded by its own mutex
	// (not b.mu): ModemStatus polls frequently and must never block on
	// an in-flight Apply, and Apply must never block on a status read.
	// modemWatchCancel stops the cellular keepalive loop; non-nil only
	// while a modem is the WAN.
	modemMu          sync.Mutex
	modemErr         string
	modemIface       string
	modemWatchCancel context.CancelFunc
}

// setModemIface records the modem's live data interface (or "" when the
// modem is down) so Status can report the cellular WAN — the config's
// WAN.Interface is empty in modem mode (the iface is discovered).
func (b *LinuxBackend) setModemIface(iface string) {
	b.modemMu.Lock()
	b.modemIface = iface
	b.modemMu.Unlock()
}

// lastModemIface returns the modem's live data interface, or "".
func (b *LinuxBackend) lastModemIface() string {
	b.modemMu.Lock()
	defer b.modemMu.Unlock()
	return b.modemIface
}

// setModemErr records (or clears, with "") the last cellular connect
// failure reason for ModemStatus to surface.
func (b *LinuxBackend) setModemErr(s string) {
	b.modemMu.Lock()
	b.modemErr = s
	b.modemMu.Unlock()
}

// lastModemErr returns the last recorded cellular connect failure.
func (b *LinuxBackend) lastModemErr() string {
	b.modemMu.Lock()
	defer b.modemMu.Unlock()
	return b.modemErr
}

// SetGuestProvider wires a registry into the backend. Pass nil to
// disable guest BSS entirely. Type definitions for the provider
// interface live in paths.go so non-Linux dev builds still see them.
func (b *LinuxBackend) SetGuestProvider(p GuestSessionProvider) {
	b.guestProvider = p
}

// activeGuest returns the current session or zero value (nothing
// active). Centralised here so the apply paths don't have to
// nil-check the provider individually.
func (b *LinuxBackend) activeGuest() ActiveGuestSession {
	if b.guestProvider == nil {
		return ActiveGuestSession{}
	}
	return b.guestProvider.CurrentGuestSession()
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

	// Cellular keepalive: run the watchdog only while a modem is the
	// WAN. It reconnects the modem after a drop and resets it out of the
	// "failed" state (e.g. after a SIM hot-swap) so the router self-heals
	// without a reboot. start/stop take modemMu, not b.mu — no deadlock.
	if cfg.Role == config.RoleWiFiRouter && cfg.Network.WAN != nil && cfg.Network.WAN.Mode == "modem" {
		b.startModemWatch()
	} else {
		b.stopModemWatch()
	}
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
	if b.current.Network.WAN != nil {
		// In modem mode the WAN interface is discovered at connect time,
		// not stored in the config — use the live modem iface the connect/
		// watchdog path recorded. A raw-ip wwan device reports operstate
		// "unknown" even when carrying traffic, so for modems we treat
		// "has an IPv4 address" as the up signal instead of operstate.
		iface := b.current.Network.WAN.Interface
		modemMode := b.current.Network.WAN.Mode == "modem"
		if modemMode {
			iface = b.lastModemIface()
		}
		w := &network.WANStatus{Interface: iface, Mode: b.current.Network.WAN.Mode}
		if iface != "" {
			// Address: first IPv4 address bound to the interface, if any.
			ip, _ := readPrimaryIPv4(iface)
			w.IP = ip
			if modemMode {
				w.Up = ip != ""
			} else {
				// Carrier state from /sys/class/net/<iface>/operstate. "up"
				// means the link is alive; everything else (down, dormant,
				// notpresent) is treated as no carrier.
				if state, err := readOperstate(iface); err == nil {
					w.Up = state == "up"
				}
			}
		}
		st.WAN = w
	}
	return st, nil
}

// Scan is implemented in scan.go.

// Close stops every supervised daemon. Intended for shutdown only.
func (b *LinuxBackend) Close() {
	b.stopModemWatch()
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, p := range []*supervisedProc{b.hostapd, b.wpaSupp, b.dnsmasq} {
		if p != nil {
			p.Stop()
		}
	}
}
