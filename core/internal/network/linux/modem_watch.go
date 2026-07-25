//go:build linux

package linux

import (
	"context"
	"fmt"
	"time"

	"github.com/knot-os/knot-os/core/internal/config"
)

// Cellular WAN keepalive ("modem watchdog").
//
// connectModem runs once, during Apply. That's not enough for a modem
// used as the WAN: USB modems drop out over time (autosuspend — see
// keepModemAwake — network re-registration, a SIM hot-swap that leaves
// ModemManager in the "failed" state). Without a keepalive the internet
// dies until the next manual apply or a reboot, which is exactly the
// symptom users hit.
//
// The watchdog is a small ticker loop, started while a modem is the WAN
// and stopped otherwise. Every tick it inspects the modem and:
//   - connected            → nothing to do (clear any stale error).
//   - failed               → reset it (rate-limited) so it re-initialises;
//                            this recovers a SIM hot-swap on its own.
//   - present but not up    → reconnect and refresh NAT for the data iface.
//
// It deliberately never touches hostapd/dnsmasq, so recovering the WAN
// doesn't bounce Wi-Fi clients.

const (
	// modemWatchInterval is how often the keepalive inspects the modem.
	modemWatchInterval = 30 * time.Second
	// modemResetCooldown rate-limits resets so a genuinely SIM-less modem
	// (permanently "failed") doesn't reset in a tight loop.
	modemResetCooldown = 3 * time.Minute
)

// startModemWatch launches the cellular keepalive loop if it isn't
// already running. Called from Apply (under b.mu) when a modem is the
// WAN; guards its own state with modemMu so it can't deadlock against
// b.mu.
func (b *LinuxBackend) startModemWatch() {
	b.modemMu.Lock()
	defer b.modemMu.Unlock()
	if b.modemWatchCancel != nil {
		return // already running
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.modemWatchCancel = cancel
	go b.modemWatchLoop(ctx)
	b.logger.Printf("modem: keepalive watchdog started")
}

// stopModemWatch cancels the keepalive loop if it's running.
func (b *LinuxBackend) stopModemWatch() {
	b.modemMu.Lock()
	defer b.modemMu.Unlock()
	if b.modemWatchCancel != nil {
		b.modemWatchCancel()
		b.modemWatchCancel = nil
		b.logger.Printf("modem: keepalive watchdog stopped")
	}
}

func (b *LinuxBackend) modemWatchLoop(ctx context.Context) {
	t := time.NewTicker(modemWatchInterval)
	defer t.Stop()
	var lastReset time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b.modemWatchOnce(ctx, &lastReset)
		}
	}
}

// modemWatchOnce runs one keepalive check. State reads are done without
// b.mu (they must never block on an in-flight Apply); only the mutating
// reconnect path takes b.mu, to serialise against Apply.
func (b *LinuxBackend) modemWatchOnce(ctx context.Context, lastReset *time.Time) {
	idx, ok := b.firstModemIndex(ctx)
	if !ok {
		// Modem gone — mid-reset re-enumeration or physically unplugged.
		// Nothing to do this tick; it'll reappear (with a new index).
		return
	}
	kv, err := b.mmcliKV(ctx, "-m", idx)
	if err != nil {
		return
	}

	switch modemActionFor(kv["modem.generic.state"]) {
	case modemNoAction:
		b.setModemErr("")
	case modemReset:
		// A failed modem won't recover on its own (typical after a SIM
		// hot-swap). Reset it so ModemManager re-initialises the module.
		reason := kv["modem.generic.state-failed-reason"]
		b.setModemErr(fmt.Sprintf("modem in failed state (%s): %s",
			orUnknown(reason), failedStateHint(reason)))
		if time.Since(*lastReset) >= modemResetCooldown {
			b.logger.Printf("modem: watchdog resetting modem in failed state (%s)", orUnknown(reason))
			b.r.runIgnoreError(ctx, "mmcli", "-m", idx, "--reset")
			*lastReset = time.Now()
		}
	case modemReconnect:
		// Present but not connected (disabled / registered / searching /
		// disconnecting …). Bring the data link back up.
		b.reconnectModemWAN(ctx)
	}
}

// modemAction is the watchdog's decision for a given ModemManager state.
type modemAction int

const (
	modemNoAction  modemAction = iota // connected — leave it
	modemReset                        // failed — reset to re-initialise
	modemReconnect                    // present but down — re-dial
)

// modemActionFor maps a ModemManager modem state to the keepalive action.
// Pure (no I/O) so the watchdog's core decision is unit-testable.
func modemActionFor(state string) modemAction {
	switch state {
	case "connected":
		return modemNoAction
	case "failed":
		return modemReset
	default:
		return modemReconnect
	}
}

// reconnectModemWAN re-establishes the cellular WAN after a drop and
// refreshes NAT for the (possibly new) data interface — without touching
// hostapd/dnsmasq, so Wi-Fi clients aren't bounced. Takes b.mu to
// serialise against Apply, and re-validates the config under the lock so
// it no-ops if the WAN changed out from under the watchdog.
func (b *LinuxBackend) reconnectModemWAN(ctx context.Context) {
	b.mu.Lock()
	defer b.mu.Unlock()

	cfg := b.current
	if !b.hasCfg || cfg.Role != config.RoleWiFiRouter ||
		cfg.Network.WAN == nil || cfg.Network.WAN.Mode != "modem" {
		return
	}

	iface, dhcp, err := b.connectModem(ctx, cfg.Network.WAN.Modem)
	if err != nil {
		b.setModemErr(err.Error())
		b.setModemIface("")
		b.logger.Printf("modem: watchdog reconnect failed: %v", err)
		return
	}
	b.setModemErr("")
	b.setModemIface(iface)
	if err := b.linkUp(ctx, iface); err != nil {
		b.logger.Printf("modem: watchdog link up %s: %v", iface, err)
		return
	}
	if dhcp {
		b.r.runIgnoreError(ctx, "pkill", "-f", "dhclient.*"+iface)
		if err := b.r.runOK(ctx, "dhclient", iface); err != nil {
			b.logger.Printf("modem: watchdog dhclient %s: %v (continuing)", iface, err)
		}
	}

	// Refresh NAT so the ruleset points at the live data iface. At the
	// original apply the modem may have been down (nft points at the
	// knot-nowan sentinel), or a reset may have produced a different
	// wwanN — either way NAT wouldn't match without this.
	lan := cfg.Network.LAN
	if lan == nil {
		lan = DefaultLAN()
	}
	guest := b.activeGuest()
	var guestBSS *HostapdGuestBSS
	if guest.SSID != "" {
		guestBSS = &HostapdGuestBSS{Interface: IfaceAPGuest, SSID: guest.SSID, PSK: guest.PSK}
	}
	rules := BuildNftablesRouter(RouterNftablesParams{
		WANInterface:   iface,
		LANInterface:   IfaceWlan,
		LANCIDR:        lan.CIDR,
		GuestInterface: guestNftIface(guestBSS),
		GuestCIDR:      guestNftCIDR(guestBSS),
		PortForwards:   cfg.Network.PortForwards,
	})
	if err := b.applyNftables(ctx, rules); err != nil {
		b.logger.Printf("modem: watchdog nft refresh: %v", err)
		return
	}
	b.logger.Printf("modem: watchdog reconnected cellular WAN on %s (dhcp=%v)", iface, dhcp)
}
