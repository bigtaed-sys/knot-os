//go:build linux

package linux

import (
	"context"
	"fmt"
	"net"
	"os"

	"github.com/knot-os/knot-os/core/internal/config"
)

// applySetup brings the host into the open-AP onboarding role:
//
//  1. Create ap0 on phy0 (idempotent).
//  2. Stop wpa_supplicant on wlan0 if present (free the radio).
//  3. Assign 192.168.42.1/24 to ap0.
//  4. Write hostapd.conf and start hostapd (open SSID).
//  5. Write dnsmasq.conf with captive-portal DNS catch-all and start
//     dnsmasq on ap0.
//  6. Load nftables rules that DNAT 80/443 from ap0 to knotd.
//
// Steps 1-5 are written to be idempotent: calling applySetup twice is
// a no-op the second time except for restarting daemons with possibly
// updated configs. The function always restarts hostapd / dnsmasq,
// which is the simplest correct choice — a wizard apply happens
// rarely and the disruption is bounded.
func (b *LinuxBackend) applySetup(ctx context.Context, cfg config.Config) error {
	lan := cfg.Network.LAN
	if lan == nil {
		lan = DefaultLAN()
	}
	gw, err := firstUsableIP(lan.CIDR)
	if err != nil {
		return fmt.Errorf("applySetup: %w", err)
	}

	// 1. ap0 must exist. We pull wlan0's MAC into the SSID so two
	//    KnotOS devices in the same room don't fight for the same
	//    name.
	if err := b.ensureAP(ctx); err != nil {
		return err
	}
	mac, _ := readMAC(IfaceWlan)
	ssid := SetupSSID(mac)

	// 2. Make sure the radio is not held by an earlier wpa_supplicant
	//    instance. wlan0 in setup mode has no role.
	if b.wpaSupp != nil {
		b.wpaSupp.Stop()
		b.wpaSupp = nil
	}
	b.linkDown(ctx, IfaceWlan)

	// 3. Bring ap0 up with the gateway IP.
	b.addrFlush(ctx, IfaceAP)
	if err := b.addrAdd(ctx, IfaceAP, gw+"/"+cidrPrefix(lan.CIDR)); err != nil {
		return fmt.Errorf("applySetup: assign %s to ap0: %w", gw, err)
	}
	if err := b.linkUp(ctx, IfaceAP); err != nil {
		return fmt.Errorf("applySetup: bring ap0 up: %w", err)
	}

	// 4. hostapd.
	hostapdConf := BuildHostapdConf(HostapdParams{
		Interface: IfaceAP,
		SSID:      ssid,
		Country:   cfg.Device.Country,
		Channel:   6,
		Band:      "2.4",
	})
	if err := writeRuntimeFile(HostapdConfPath, hostapdConf); err != nil {
		return err
	}
	if b.hostapd == nil {
		b.hostapd = newSupervisedProc("hostapd", "hostapd", HostapdConfPath)
	}
	if err := b.hostapd.Restart(ctx); err != nil {
		return fmt.Errorf("applySetup: hostapd: %w", err)
	}

	// 5. dnsmasq with captive-portal mode.
	dnsmasqConf := BuildDnsmasqConf(DnsmasqParams{
		Interface:     IfaceAP,
		ListenIP:      gw,
		DHCPPoolStart: lan.DHCP.PoolStart,
		DHCPPoolEnd:   lan.DHCP.PoolEnd,
		CaptivePortal: true,
	})
	if err := writeRuntimeFile(DnsmasqConfPath, dnsmasqConf); err != nil {
		return err
	}
	if b.dnsmasq == nil {
		b.dnsmasq = newSupervisedProc("dnsmasq", "dnsmasq",
			"--keep-in-foreground",
			"--conf-file="+DnsmasqConfPath,
			"--pid-file=", // empty path = no pid file written
		)
	}
	if err := b.dnsmasq.Restart(ctx); err != nil {
		return fmt.Errorf("applySetup: dnsmasq: %w", err)
	}

	// 6. Captive-portal redirect: every HTTP(S) request from ap0 lands
	//    on knotd. Loaded with `nft -f`. We always flush our table
	//    first to make this idempotent across role changes.
	rules := BuildNftablesCaptive(CaptivePortalParams{
		LANInterface: IfaceAP,
		GatewayIP:    gw,
		HTTPPort:     b.HTTPPort,
	})
	if err := b.applyNftables(ctx, rules); err != nil {
		return fmt.Errorf("applySetup: nftables: %w", err)
	}

	b.logger.Printf("setup role active: ssid=%q gateway=%s", ssid, gw)
	return nil
}

// applyNftables flushes any prior knot-owned tables and loads the
// given ruleset. The tables we own are deterministic: knot, knot_nat,
// knot_captive — flushing them is safe.
func (b *LinuxBackend) applyNftables(ctx context.Context, ruleset string) error {
	if err := writeRuntimeFile(NftablesRulesetPath, ruleset); err != nil {
		return err
	}
	for _, t := range []string{"knot", "knot_nat", "knot_captive"} {
		// `nft delete table` errors if the table does not exist —
		// that's fine on first apply and on role changes.
		b.r.runIgnoreError(ctx, "nft", "delete", "table", "inet", t)
		b.r.runIgnoreError(ctx, "nft", "delete", "table", "ip", t)
	}
	return b.r.runOK(ctx, "nft", "-f", NftablesRulesetPath)
}

// writeRuntimeFile writes content to path with mode 0o644, creating
// parent directories as needed.
func writeRuntimeFile(path, content string) error {
	if err := os.MkdirAll(RuntimeDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", RuntimeDir, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// firstUsableIP returns the first usable host address in the CIDR
// (i.e. network address + 1). For 192.168.42.0/24 → 192.168.42.1.
func firstUsableIP(cidr string) (string, error) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", err
	}
	ip := ipnet.IP.To4()
	if ip == nil {
		return "", fmt.Errorf("only IPv4 CIDRs are supported")
	}
	gw := make(net.IP, 4)
	copy(gw, ip)
	gw[3]++
	return gw.String(), nil
}

// cidrPrefix returns the prefix length of a CIDR string ("192.168.42.0/24" → "24").
func cidrPrefix(cidr string) string {
	for i := len(cidr) - 1; i >= 0; i-- {
		if cidr[i] == '/' {
			return cidr[i+1:]
		}
	}
	return "24"
}
