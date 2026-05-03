//go:build linux

package linux

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/knot-os/knot-os/core/internal/config"
)

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
	if cfg.Network.WAN == nil || cfg.Network.WAN.Interface == "" {
		return errors.New("applyRouter: wan.interface missing — config is invalid")
	}
	if cfg.Network.AP == nil {
		return errors.New("applyRouter: ap missing — config is invalid")
	}
	wan := cfg.Network.WAN.Interface
	if !interfaceExists(wan) {
		return fmt.Errorf("applyRouter: WAN interface %q not present", wan)
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

	// 1. Free wlan0 from any extender-mode leftovers. wpa_supplicant
	//    holding wlan0 would block hostapd from claiming it, and a
	//    leftover ap0 would just be dead weight.
	if b.wpaSupp != nil {
		b.wpaSupp.Stop()
		b.wpaSupp = nil
	}
	b.removeAP(ctx)
	b.addrFlush(ctx, IfaceWlan)

	// 2. WAN: bring the link up, run dhclient -1 against it. 30s timeout
	//    matches the extender's wlan0 dhclient.
	if err := b.linkUp(ctx, wan); err != nil {
		return fmt.Errorf("applyRouter: bring %s up: %w", wan, err)
	}
	dhcpCtx, dhcpCancel := context.WithTimeout(ctx, 30*time.Second)
	defer dhcpCancel()
	if err := b.r.runOK(dhcpCtx, "dhclient", "-1", wan); err != nil {
		return fmt.Errorf("applyRouter: dhclient on %s: %w", wan, err)
	}

	// 3. wlan0 LAN side. Same gateway-on-the-LAN-CIDR pattern as
	//    extender, but the AP runs straight on wlan0 (no ap0).
	if err := b.addrAdd(ctx, IfaceWlan, gw+"/"+cidrPrefix(lan.CIDR)); err != nil {
		return fmt.Errorf("applyRouter: assign %s to %s: %w", gw, IfaceWlan, err)
	}
	if err := b.linkUp(ctx, IfaceWlan); err != nil {
		return fmt.Errorf("applyRouter: bring %s up: %w", IfaceWlan, err)
	}

	// 4. hostapd on wlan0 with the user-picked channel. Channel 0 means
	//    "auto"; BuildHostapdConf falls back to channel 6.
	hostapdConf := BuildHostapdConf(HostapdParams{
		Interface: IfaceWlan,
		SSID:      cfg.Network.AP.SSID,
		Country:   effectiveCountry(cfg.Device.Country),
		Channel:   cfg.Network.AP.Channel,
		Band:      cfg.Network.AP.Band,
		PSK:       cfg.Network.AP.PSK,
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

	// 5. dnsmasq on wlan0, DHCP only (knotd's resolver owns 53).
	dnsmasqConf := BuildDnsmasqConf(DnsmasqParams{
		Interface:     IfaceWlan,
		ListenIP:      gw,
		DHCPPoolStart: lan.DHCP.PoolStart,
		DHCPPoolEnd:   lan.DHCP.PoolEnd,
		DisableDNS:    true,
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
		WANInterface: wan,
		LANInterface: IfaceWlan,
		LANCIDR:      lan.CIDR,
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
