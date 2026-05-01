//go:build linux

package linux

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/knot-os/knot-os/core/internal/config"
)

// applyExtender brings the host into the wifi-extender role.
//
// Order of operations matters because of the BCM43436 single-radio
// constraint — ap0 and wlan0 must end up on the same channel:
//
//  1. Ensure ap0 exists.
//  2. Connect wlan0 to the upstream via wpa_supplicant.
//  3. Run dhcpcd-equivalent (we use `dhclient -1` for v0.1 simplicity)
//     to obtain a DHCP lease on wlan0.
//  4. Discover wlan0's current channel (now that the radio is on it).
//  5. Configure ap0: assign LAN gateway, write hostapd.conf using the
//     STA's channel, restart hostapd, restart dnsmasq with upstream
//     DNS forwarders.
//  6. Enable IPv4 forwarding and load NAT rules ap0 -> wlan0.
func (b *LinuxBackend) applyExtender(ctx context.Context, cfg config.Config) error {
	if cfg.Network.Uplink == nil {
		return errors.New("applyExtender: uplink missing — config is invalid")
	}
	if cfg.Network.AP == nil {
		return errors.New("applyExtender: ap missing — config is invalid")
	}
	lan := cfg.Network.LAN
	if lan == nil {
		lan = DefaultLAN()
	}
	gw, err := firstUsableIP(lan.CIDR)
	if err != nil {
		return fmt.Errorf("applyExtender: %w", err)
	}

	// 0. Radio prerequisites: unblock rfkill, set the regulatory
	//    domain. Necessary on a fresh boot where Pi OS Lite leaves
	//    the radio rfkill-blocked.
	b.unblockAndRegulate(ctx, cfg.Device.Country)

	// 1. ap0 must exist before anything else; on a single radio we
	//    can create the interface even before the STA associates.
	if err := b.ensureAP(ctx); err != nil {
		return err
	}

	// 2. Bring up the upstream STA. We restart hostapd LAST so it
	//    picks up the channel we end up on.
	if err := b.startUplink(ctx, cfg); err != nil {
		return fmt.Errorf("applyExtender: uplink: %w", err)
	}

	// 3. dhclient on wlan0. -1 makes it exit after one successful
	//    lease, blocking until then. We give it ~30s.
	dhcpCtx, dhcpCancel := context.WithTimeout(ctx, 30*time.Second)
	defer dhcpCancel()
	if err := b.r.runOK(dhcpCtx, "dhclient", "-1", IfaceWlan); err != nil {
		return fmt.Errorf("applyExtender: dhclient on wlan0: %w", err)
	}

	// 4. Find the channel wlan0 ended up on (governed by the AP we
	//    associated to).
	channel, err := b.currentChannel(ctx, IfaceWlan)
	if err != nil {
		// Non-fatal — fall back to the default channel and let
		// hostapd cope. On modern kernels the driver will reject
		// a mismatched channel and hostapd will log clearly.
		b.logger.Printf("warn: could not detect wlan0 channel (%v); falling back to ch 6", err)
		channel = 6
	}

	// 5. ap0 LAN side.
	b.addrFlush(ctx, IfaceAP)
	if err := b.addrAdd(ctx, IfaceAP, gw+"/"+cidrPrefix(lan.CIDR)); err != nil {
		return fmt.Errorf("applyExtender: assign %s to ap0: %w", gw, err)
	}
	if err := b.linkUp(ctx, IfaceAP); err != nil {
		return fmt.Errorf("applyExtender: bring ap0 up: %w", err)
	}

	hostapdConf := BuildHostapdConf(HostapdParams{
		Interface: IfaceAP,
		SSID:      cfg.Network.AP.SSID,
		Country:   effectiveCountry(cfg.Device.Country),
		Channel:   channel,
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
		return fmt.Errorf("applyExtender: hostapd: %w", err)
	}

	dnsmasqConf := BuildDnsmasqConf(DnsmasqParams{
		Interface:     IfaceAP,
		ListenIP:      gw,
		DHCPPoolStart: lan.DHCP.PoolStart,
		DHCPPoolEnd:   lan.DHCP.PoolEnd,
		Forwarders:    []string{"1.1.1.1", "8.8.8.8"},
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
		return fmt.Errorf("applyExtender: dnsmasq: %w", err)
	}

	// 6. IP forwarding + NAT.
	if err := b.r.runOK(ctx, "sysctl", "-w", "net.ipv4.ip_forward=1"); err != nil {
		return fmt.Errorf("applyExtender: enable forwarding: %w", err)
	}
	rules := BuildNftablesExtender(NftablesParams{
		WANInterface: IfaceWlan,
		LANInterface: IfaceAP,
		LANCIDR:      lan.CIDR,
	})
	if err := b.applyNftables(ctx, rules); err != nil {
		return fmt.Errorf("applyExtender: nftables: %w", err)
	}

	b.logger.Printf("wifi-extender role active: uplink=%q ap=%q channel=%d gw=%s",
		cfg.Network.Uplink.SSID, cfg.Network.AP.SSID, channel, gw)
	return nil
}

// startUplink writes wpa_supplicant.conf and (re)starts wpa_supplicant
// supervised on wlan0. Blocks briefly to let the supplicant launch.
func (b *LinuxBackend) startUplink(ctx context.Context, cfg config.Config) error {
	conf := BuildWpaSupplicantConf(WpaSupplicantParams{
		Country: effectiveCountry(cfg.Device.Country),
		SSID:    cfg.Network.Uplink.SSID,
		PSK:     cfg.Network.Uplink.PSK,
	})
	if err := writeRuntimeFile(WpaSupplicantConfPath, conf); err != nil {
		return err
	}

	if err := b.linkUp(ctx, IfaceWlan); err != nil {
		return fmt.Errorf("bring wlan0 up: %w", err)
	}

	if b.wpaSupp == nil {
		b.wpaSupp = newSupervisedProc("wpa_supplicant", "wpa_supplicant",
			"-i", IfaceWlan,
			"-c", WpaSupplicantConfPath,
			"-D", "nl80211",
		)
	}
	if err := b.wpaSupp.Restart(ctx); err != nil {
		return err
	}

	// Wait for association (`iw dev wlan0 link` shows "Connected to ..."
	// once associated). 15s is generous for a healthy upstream and
	// strict enough that a typo in the password doesn't hang the API.
	deadline, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for {
		if connected, _ := b.uplinkConnected(deadline); connected {
			return nil
		}
		select {
		case <-deadline.Done():
			return fmt.Errorf("uplink %q did not associate within 15s", cfg.Network.Uplink.SSID)
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// uplinkConnected reports whether wlan0 is currently associated.
func (b *LinuxBackend) uplinkConnected(ctx context.Context) (bool, error) {
	out, err := b.r.run(ctx, "iw", "dev", IfaceWlan, "link")
	if err != nil {
		return false, err
	}
	return strings.Contains(out, "Connected to"), nil
}

// currentChannel returns the channel an interface is currently on
// (parsed from `iw dev <iface> info`). Returns 0 + error if the
// interface is not associated to anything.
func (b *LinuxBackend) currentChannel(ctx context.Context, iface string) (int, error) {
	out, err := b.r.run(ctx, "iw", "dev", iface, "info")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		// Format: "channel 6 (2437 MHz), width: 20 MHz, ..."
		if !strings.HasPrefix(line, "channel ") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		ch, err := strconv.Atoi(parts[1])
		if err != nil {
			return 0, fmt.Errorf("parse channel %q: %w", parts[1], err)
		}
		return ch, nil
	}
	return 0, fmt.Errorf("no channel reported in `iw dev %s info`", iface)
}
