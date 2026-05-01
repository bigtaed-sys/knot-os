package linux

import (
	"strings"
	"testing"
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
		"dhcp-range=192.168.42.100,192.168.42.200,12h",
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
