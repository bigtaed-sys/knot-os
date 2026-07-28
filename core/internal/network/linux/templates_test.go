package linux

import (
	"strings"
	"testing"

	"github.com/knot-os/knot-os/core/internal/config"
)

func TestHostapdOpenNetwork(t *testing.T) {
	out := BuildHostapdConf(HostapdParams{
		Interface: "ap0",
		SSID:      "KnotOS-setup-1234",
		Country:   "00",
		Channel:   6,
		Band:      "2.4",
	})

	mustContain(t, out,
		"interface=ap0",
		"ssid=KnotOS-setup-1234",
		"country_code=00",
		"channel=6",
		"hw_mode=g",
	)
	if strings.Contains(out, "wpa_passphrase") {
		t.Error("open AP should not include wpa_passphrase")
	}
	if strings.Contains(out, "wpa=2") {
		t.Error("open AP should not include wpa=2")
	}
}

func TestHostapdSecuredNetwork(t *testing.T) {
	out := BuildHostapdConf(HostapdParams{
		Interface: "ap0",
		SSID:      "KnotNet",
		Country:   "RU",
		Channel:   11,
		Band:      "2.4",
		PSK:       "mySecret",
	})

	mustContain(t, out,
		"ssid=KnotNet",
		"country_code=RU",
		"channel=11",
		"wpa=2",
		"wpa_key_mgmt=WPA-PSK",
		"wpa_passphrase=mySecret",
	)
}

func TestHostapdDefaultsChannel(t *testing.T) {
	out := BuildHostapdConf(HostapdParams{Interface: "ap0", SSID: "x", Country: "RU"})
	if !strings.Contains(out, "channel=6") {
		t.Error("Channel=0 should default to 6")
	}
}

func TestHostapdBridge(t *testing.T) {
	// With Bridge set, hostapd must be told to enslave its AP interface
	// into the LAN bridge; without it, no bridge= line appears.
	withBr := BuildHostapdConf(HostapdParams{
		Interface: "wlan0", Bridge: "br-lan", SSID: "x", Country: "RU", PSK: "secret12",
	})
	mustContain(t, withBr, "interface=wlan0", "bridge=br-lan")
	// bridge= must come before the driver line (hostapd is order-tolerant
	// but we keep it adjacent to interface= for readability).
	if i, j := strings.Index(withBr, "bridge=br-lan"), strings.Index(withBr, "driver=nl80211"); i < 0 || i > j {
		t.Errorf("bridge= should appear right after interface=, got:\n%s", withBr)
	}

	noBr := BuildHostapdConf(HostapdParams{Interface: "wlan0", SSID: "x", Country: "RU"})
	if strings.Contains(noBr, "bridge=") {
		t.Errorf("no bridge should emit no bridge= line, got:\n%s", noBr)
	}
}

func TestWpaSupplicantSecured(t *testing.T) {
	out := BuildWpaSupplicantConf(WpaSupplicantParams{
		Country: "RU",
		SSID:    "Home Wi-Fi",
		PSK:     "supersecret",
	})
	mustContain(t, out,
		"country=RU",
		`ssid="Home Wi-Fi"`,
		`psk="supersecret"`,
		"key_mgmt=WPA-PSK",
	)
}

func TestWpaSupplicantOpen(t *testing.T) {
	out := BuildWpaSupplicantConf(WpaSupplicantParams{Country: "RU", SSID: "FreeCafe"})
	mustContain(t, out, "key_mgmt=NONE")
	if strings.Contains(out, "psk=") {
		t.Error("open network must not have psk= line")
	}
}

func TestDnsmasqCaptivePortal(t *testing.T) {
	out := BuildDnsmasqConf(DnsmasqParams{
		Interface:     "ap0",
		ListenIP:      "192.168.42.1",
		DHCPPoolStart: "192.168.42.100",
		DHCPPoolEnd:   "192.168.42.200",
		CaptivePortal: true,
	})
	mustContain(t, out,
		"interface=ap0",
		"listen-address=192.168.42.1",
		"dhcp-range=set:lan,192.168.42.100,192.168.42.200,12h",
		"address=/#/192.168.42.1",
	)
}

func TestDnsmasqExtenderForwards(t *testing.T) {
	out := BuildDnsmasqConf(DnsmasqParams{
		Interface:     "ap0",
		ListenIP:      "192.168.42.1",
		DHCPPoolStart: "192.168.42.100",
		DHCPPoolEnd:   "192.168.42.200",
		Forwarders:    []string{"1.1.1.1", "8.8.8.8"},
	})
	mustContain(t, out,
		"server=1.1.1.1",
		"server=8.8.8.8",
	)
	if strings.Contains(out, "address=/#/") {
		t.Error("extender mode must not contain DNS catch-all (captive portal)")
	}
}

func TestNftablesExtenderHasNatAndForward(t *testing.T) {
	out := BuildNftablesExtender(NftablesParams{
		WANInterface: "wlan0",
		LANInterface: "ap0",
		LANCIDR:      "192.168.42.0/24",
	})
	mustContain(t, out,
		`iifname "ap0"`,
		`oifname "wlan0"`,
		"masquerade",
		"192.168.42.0/24",
		"established,related",
		// Block-set scaffolding for the scheduler:
		"set blocked_macs",
		"type ether_addr",
		"ether saddr @blocked_macs drop",
	)
}

func TestNftablesRouterUsesEthWAN(t *testing.T) {
	out := BuildNftablesRouter(RouterNftablesParams{
		WANInterface: "eth0",
		LANInterface: "wlan0",
		LANCIDR:      "192.168.42.0/24",
	})
	mustContain(t, out,
		`iifname "wlan0"`,
		`oifname "eth0"`,
		"masquerade",
		"192.168.42.0/24",
		"established,related",
		"set blocked_macs",
		"ether saddr @blocked_macs drop",
	)
}

func TestNftablesRouterIsolatesGuestBSS(t *testing.T) {
	out := BuildNftablesRouter(RouterNftablesParams{
		WANInterface:   "eth0",
		LANInterface:   "wlan0",
		LANCIDR:        "192.168.42.0/24",
		GuestInterface: "ap_guest",
		GuestCIDR:      "192.168.43.0/24",
	})
	mustContain(t, out,
		// Guest can reach WAN (and replies come back).
		`iifname "ap_guest" oifname "eth0" accept`,
		`iifname "eth0" oifname "ap_guest" ct state established,related accept`,
		// Guest is hard-walled off from the main LAN both directions.
		`iifname "ap_guest" oifname "wlan0" drop`,
		`iifname "wlan0" oifname "ap_guest" drop`,
		// Guest source IPs masqueraded out the WAN.
		`ip saddr 192.168.43.0/24 oifname "eth0" masquerade`,
	)
}

func TestNftablesRouterPortForwards(t *testing.T) {
	out := BuildNftablesRouter(RouterNftablesParams{
		WANInterface: "eth0",
		LANInterface: "wlan0",
		LANCIDR:      "192.168.42.0/24",
		PortForwards: []config.PortForward{
			{ID: "game", Proto: "tcp", WANPort: 25565, DestIP: "192.168.42.50", Enabled: true},
			{ID: "dns", Proto: "tcp/udp", WANPort: 5353, DestIP: "192.168.42.51", DestPort: 53, Enabled: true},
			{ID: "off", Proto: "tcp", WANPort: 9999, DestIP: "192.168.42.99", Enabled: false},
		},
	})
	mustContain(t, out,
		"chain prerouting",
		"type nat hook prerouting priority dstnat",
		// Single-proto rule, dest port defaults to WAN port.
		`iifname "eth0" tcp dport 25565 dnat to 192.168.42.50:25565`,
		`iifname "eth0" ip daddr 192.168.42.50 tcp dport 25565 ct state new,established,related accept`,
		// tcp/udp expands to both, explicit dest port.
		`iifname "eth0" tcp dport 5353 dnat to 192.168.42.51:53`,
		`iifname "eth0" udp dport 5353 dnat to 192.168.42.51:53`,
	)
	// Disabled rule must not appear.
	if strings.Contains(out, "9999") {
		t.Errorf("disabled port forward leaked into ruleset:\n%s", out)
	}
}

func TestNftablesRouterNoPortForwardChainWhenEmpty(t *testing.T) {
	out := BuildNftablesRouter(RouterNftablesParams{
		WANInterface: "eth0",
		LANInterface: "wlan0",
		LANCIDR:      "192.168.42.0/24",
	})
	if strings.Contains(out, "chain prerouting") {
		t.Errorf("prerouting chain emitted with no port forwards:\n%s", out)
	}
}

func TestHostapdConfWithGuestBSS(t *testing.T) {
	out := BuildHostapdConf(HostapdParams{
		Interface: "ap0",
		SSID:      "KnotNet",
		Country:   "RU",
		Channel:   6,
		Band:      "2.4",
		PSK:       "main-secret",
		Guest: &HostapdGuestBSS{
			Interface: "ap_guest",
			SSID:      "KnotNet-guest",
			PSK:       "guest-pass-12",
		},
	})
	mustContain(t, out,
		"interface=ap0",
		"ssid=KnotNet",
		"wpa_passphrase=main-secret",
		"bss=ap_guest",
		"ssid=KnotNet-guest",
		"wpa_passphrase=guest-pass-12",
		"ap_isolate=1",
	)
}

func TestNftablesCaptiveRedirects(t *testing.T) {
	out := BuildNftablesCaptive(CaptivePortalParams{
		LANInterface: "ap0",
		GatewayIP:    "192.168.42.1",
		HTTPPort:     80,
	})
	mustContain(t, out,
		`iifname "ap0"`,
		"tcp dport 80 dnat to 192.168.42.1:80",
		"tcp dport 443 dnat to 192.168.42.1:80",
	)
}

func TestSetupSSIDFromMAC(t *testing.T) {
	cases := map[string]string{
		"dc:a6:32:11:22:33": "KnotOS-setup-2233",
		"DC-A6-32-11-22-33": "KnotOS-setup-2233",
		"AABB":              "KnotOS-setup-AABB",
		"":                  "KnotOS-setup-0000",
	}
	for in, want := range cases {
		if got := SetupSSID(in); got != want {
			t.Errorf("SetupSSID(%q) = %q, want %q", in, got, want)
		}
	}
}

func mustContain(t *testing.T, out string, needles ...string) {
	t.Helper()
	for _, n := range needles {
		if !strings.Contains(out, n) {
			t.Errorf("missing %q in output:\n%s", n, out)
		}
	}
}
