//go:build linux

package linux

import (
	"context"
	"errors"
	"fmt"

	"github.com/knot-os/knot-os/core/internal/config"
)

// guestNftIface returns the guest interface name for nftables, or
// "" when no guest BSS is active. Centralised so the apply paths
// don't have to nil-check inline.
func guestNftIface(g *HostapdGuestBSS) string {
	if g == nil {
		return ""
	}
	return g.Interface
}

// guestNftCIDR returns the guest /24 CIDR when active.
func guestNftCIDR(g *HostapdGuestBSS) string {
	if g == nil {
		return ""
	}
	return GuestLANCIDR
}

// guestDnsmasqExtras returns dnsmasq's secondary-interface list for
// the active guest BSS, or nil when none.
func guestDnsmasqExtras(g *HostapdGuestBSS) []DnsmasqExtra {
	if g == nil {
		return nil
	}
	return []DnsmasqExtra{{
		Interface:     g.Interface,
		ListenIP:      GuestLANGateway,
		DHCPPoolStart: GuestLANPoolStart,
		DHCPPoolEnd:   GuestLANPoolEnd,
	}}
}

// applyRouter brings the host into the wifi-router role.
//
// Topology (Pi Zero 2W or Pi 4/5 with USB-Ethernet):
//
//	[client device] --wifi--> wlan0 (AP) --NAT--> <wan> --DHCP--> upstream
//
// Order of operations:
//
//  1. Tear down anything left over from previous extender state
//     (wpa_supplicant on wlan0, ap0 virtual interface) — wlan0 must
//     be free for hostapd to claim it directly.
//  2. Bring WAN up and obtain a DHCP lease there.
//  3. Configure wlan0 LAN side: assign the gateway IP, link up.
//  4. Write hostapd.conf for the user-picked channel, restart hostapd.
//  5. Restart dnsmasq with knotd's port=0 mode (DNS handled by knotd's
//     own resolver on the gateway IP).
//  6. Enable forwarding + load NAT rules for wlan0 → wan.
func (b *LinuxBackend) applyRouter(ctx context.Context, cfg config.Config) error {
	if cfg.Network.WAN == nil {
		return errors.New("applyRouter: wan missing — config is invalid")
	}
	if cfg.Network.AP == nil {
		return errors.New("applyRouter: ap missing — config is invalid")
	}
	modemMode := cfg.Network.WAN.Mode == "modem"
	wan := cfg.Network.WAN.Interface
	// In Ethernet mode the interface must exist up front; in modem mode
	// the data interface is discovered after we connect the modem below.
	if !modemMode {
		if wan == "" {
			return errors.New("applyRouter: wan.interface missing — config is invalid")
		}
		if !interfaceExists(wan) {
			return fmt.Errorf("applyRouter: WAN interface %q not present", wan)
		}
	}
	lan := cfg.Network.LAN
	if lan == nil {
		lan = DefaultLAN()
	}
	gw, err := firstUsableIP(lan.CIDR)
	if err != nil {
		return fmt.Errorf("applyRouter: %w", err)
	}

	// 0. rfkill / regdomain — same as extender, the radio still needs
	//    to be unblocked even though we don't run an STA.
	b.unblockAndRegulate(ctx, cfg.Device.Country)

	// 1. Free wlan0 from any setup/extender leftovers.
	//
	// Order matters: hostapd may currently be running on ap0 (setup
	// mode) or wlan0 (re-apply within router mode). Removing ap0
	// while hostapd holds it leaves the daemon in an ENODEV zombie
	// state — the next Restart() then can't cleanly bring it back
	// on wlan0, the AP never appears, and the user has to re-apply
	// once more for it to recover. So:
	//
	//   1a. Stop hostapd (regardless of which interface it owned).
	//   1b. Stop any wpa_supplicant.
	//   1c. Now it's safe to delete ap0 + flush wlan0.
	if b.hostapd != nil {
		b.hostapd.Stop()
	}
	if b.wpaSupp != nil {
		b.wpaSupp.Stop()
		b.wpaSupp = nil
	}
	b.removeAP(ctx)
	b.addrFlush(ctx, IfaceWlan)

	// 2. WAN: bring the link up, kick dhclient in the background.
	//
	// We DON'T block on a successful lease here. Reason: a freshly
	// configured Pi may have no Ethernet cable plugged in yet (user
	// is still wiring things up at their desk), and dhclient -1
	// with no carrier just sits for 30+ seconds before failing.
	// Old behaviour: the whole apply aborted, hostapd never started,
	// the LAN-side AP never came up, the user couldn't even reach
	// the dashboard to see what was wrong.
	//
	// New behaviour: spawn dhclient as a long-lived self-daemonizing
	// process. It keeps retrying internally; whenever the cable gets
	// plugged in, a lease arrives within a few seconds and the
	// dashboard's WAN tile flips from "down" to "up" with the IP.
	// The AP, NAT, DNS, and admin UI come up immediately and stay
	// reachable from the LAN regardless of the WAN's state.
	// Cellular WAN: connect the modem via ModemManager and adopt its
	// data interface as the WAN. needsDHCP is false when the modem
	// negotiated a static address (typical for QMI) — connectModem
	// applied it already, so we skip dhclient below. A modem that
	// isn't plugged in / connectable doesn't abort the apply: the AP,
	// NAT and admin UI still come up so the user can fix the SIM/APN.
	// Cellular WAN: connect the modem via ModemManager and adopt its
	// data interface as the WAN. needsDHCP is false when the modem
	// negotiated a static address (typical for QMI) — connectModem
	// applied it already, so we skip dhclient. A modem that isn't
	// plugged in / connectable doesn't abort the apply: the AP, NAT
	// and admin UI still come up so the user can fix the SIM/APN.
	modemDHCP := true
	if modemMode {
		iface, dhcp, err := b.connectModem(ctx, cfg.Network.WAN.Modem)
		if err != nil {
			// Don't abort — the AP/LAN/admin UI must still come up so the
			// user can fix the SIM/APN. But record the reason: the apply
			// otherwise "succeeds" with WAN down, and the UI would only
			// show a bare "failed". ModemStatus surfaces modemErr.
			b.logger.Printf("applyRouter: modem: %v (continuing, WAN down)", err)
			b.setModemErr(err.Error())
			b.setModemIface("")
			wan = ""
		} else {
			b.setModemErr("")
			b.setModemIface(iface)
			wan, modemDHCP = iface, dhcp
			b.logger.Printf("applyRouter: cellular WAN up on %s (dhcp=%v)", wan, dhcp)
		}
	}

	if wan == "" {
		// No usable WAN yet (modem absent / unconnectable). The LAN side
		// still comes up so the dashboard stays reachable; a re-apply
		// once the modem is ready wires up NAT.
		b.logger.Printf("applyRouter: no WAN interface available — bringing up LAN only")
	} else {
		if err := b.linkUp(ctx, wan); err != nil {
			return fmt.Errorf("applyRouter: bring %s up: %w", wan, err)
		}
		if modemDHCP {
			// Kill any leftover dhclient on this interface before starting
			// a fresh self-daemonizing one. Best-effort; the AP path below
			// is what users actually need to function.
			b.r.runIgnoreError(ctx, "pkill", "-f", "dhclient.*"+wan)
			if err := b.r.runOK(ctx, "dhclient", wan); err != nil {
				b.logger.Printf("applyRouter: dhclient on %s returned %v (continuing)", wan, err)
			}
		}
	}

	// Downstream nft/NAT need a non-empty interface token even when no
	// WAN is up yet; a sentinel name (no such device) keeps the ruleset
	// valid while its WAN rules simply never match.
	if wan == "" {
		wan = "knot-nowan"
	}

	// 3. wlan0 LAN side. Same gateway-on-the-LAN-CIDR pattern as
	//    extender, but the AP runs straight on wlan0 (no ap0).
	if err := b.addrAdd(ctx, IfaceWlan, gw+"/"+cidrPrefix(lan.CIDR)); err != nil {
		return fmt.Errorf("applyRouter: assign %s to %s: %w", gw, IfaceWlan, err)
	}
	if err := b.linkUp(ctx, IfaceWlan); err != nil {
		return fmt.Errorf("applyRouter: bring %s up: %w", IfaceWlan, err)
	}

	// 4. Optional guest BSS — comes up only when a guest session is
	//    active. Created BEFORE hostapd starts so the daemon sees
	//    both interfaces ready and the multi-BSS config block in
	//    hostapd.conf can reference ap_guest by name.
	guest := b.activeGuest()
	var guestBSS *HostapdGuestBSS
	if guest.SSID != "" {
		if err := b.ensureAPGuest(ctx); err != nil {
			b.logger.Printf("applyRouter: ensureAPGuest: %v (continuing without guest BSS)", err)
		} else {
			guestBSS = &HostapdGuestBSS{
				Interface: IfaceAPGuest,
				SSID:      guest.SSID,
				PSK:       guest.PSK,
			}
		}
	} else {
		b.removeAPGuest(ctx)
	}

	// 5. hostapd on wlan0 with the user-picked channel. Channel 0 means
	//    "auto"; BuildHostapdConf falls back to channel 6. Guest BSS,
	//    when set, is appended as a second [bss=ap_guest] section.
	hostapdConf := BuildHostapdConf(HostapdParams{
		Interface: IfaceWlan,
		SSID:      cfg.Network.AP.SSID,
		Country:   effectiveCountry(cfg.Device.Country),
		Channel:   cfg.Network.AP.Channel,
		Band:      cfg.Network.AP.Band,
		PSK:       cfg.Network.AP.PSK,
		Guest:     guestBSS,
	})
	if err := writeRuntimeFile(HostapdConfPath, hostapdConf); err != nil {
		return err
	}
	if b.hostapd == nil {
		b.hostapd = newSupervisedProc("hostapd", "hostapd", HostapdConfPath)
	}
	if err := b.hostapd.Restart(ctx); err != nil {
		return fmt.Errorf("applyRouter: hostapd: %w", err)
	}

	// Guest interface needs an IP, link up, and a DHCP scope so
	// guests can actually use it once hostapd is talking. Idempotent;
	// if the interface didn't come up the helper just no-ops.
	if guestBSS != nil {
		b.addrFlush(ctx, IfaceAPGuest)
		if err := b.addrAdd(ctx, IfaceAPGuest, GuestLANGateway+"/"+cidrPrefix(GuestLANCIDR)); err != nil {
			b.logger.Printf("applyRouter: guest addr: %v", err)
		}
		if err := b.linkUp(ctx, IfaceAPGuest); err != nil {
			b.logger.Printf("applyRouter: guest link up: %v", err)
		}
	}

	// 6. dnsmasq on wlan0 (and ap_guest when active), DHCP only —
	//    knotd's resolver owns 53. The guest pool is appended as a
	//    second `dhcp-range=<iface>,...` line; dnsmasq picks the
	//    interface based on the DHCP request's source.
	dnsmasqConf := BuildDnsmasqConf(DnsmasqParams{
		Interface:       IfaceWlan,
		ListenIP:        gw,
		DHCPPoolStart:   lan.DHCP.PoolStart,
		DHCPPoolEnd:     lan.DHCP.PoolEnd,
		DisableDNS:      true,
		ExtraInterfaces: guestDnsmasqExtras(guestBSS),
	})
	if err := writeRuntimeFile(DnsmasqConfPath, dnsmasqConf); err != nil {
		return err
	}
	if b.dnsmasq == nil {
		b.dnsmasq = newSupervisedProc("dnsmasq", "dnsmasq",
			"--keep-in-foreground",
			"--conf-file="+DnsmasqConfPath,
			"--pid-file=",
		)
	}
	if err := b.dnsmasq.Restart(ctx); err != nil {
		return fmt.Errorf("applyRouter: dnsmasq: %w", err)
	}

	// 6. IP forwarding + NAT.
	if err := b.r.runOK(ctx, "sysctl", "-w", "net.ipv4.ip_forward=1"); err != nil {
		return fmt.Errorf("applyRouter: enable forwarding: %w", err)
	}
	rules := BuildNftablesRouter(RouterNftablesParams{
		WANInterface:   wan,
		LANInterface:   IfaceWlan,
		LANCIDR:        lan.CIDR,
		GuestInterface: guestNftIface(guestBSS),
		GuestCIDR:      guestNftCIDR(guestBSS),
		PortForwards:   cfg.Network.PortForwards,
	})
	if err := b.applyNftables(ctx, rules); err != nil {
		return fmt.Errorf("applyRouter: nftables: %w", err)
	}

	channel := cfg.Network.AP.Channel
	if channel == 0 {
		channel = 6
	}
	b.logger.Printf("wifi-router role active: wan=%s ap=%q channel=%d gw=%s",
		wan, cfg.Network.AP.SSID, channel, gw)
	return nil
}
