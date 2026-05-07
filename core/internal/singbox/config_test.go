package singbox

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderDefaultsHasDirectAndBlock(t *testing.T) {
	c := DefaultsConfig()
	js, err := c.RenderJSON()
	if err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(js, &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, js)
	}
	outs, _ := doc["outbounds"].([]any)
	tags := map[string]bool{}
	for _, o := range outs {
		m, _ := o.(map[string]any)
		tag, _ := m["tag"].(string)
		tags[tag] = true
	}
	for _, want := range []string{"direct", "block", "dns-out"} {
		if !tags[want] {
			t.Errorf("missing built-in outbound %q", want)
		}
	}
}

func TestRenderRejectsDuplicateTags(t *testing.T) {
	c := Config{
		Outbounds: []Outbound{
			{Tag: "x", Type: OutboundDirect},
			{Tag: "x", Type: OutboundBlock},
		},
	}
	if _, err := c.RenderJSON(); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("expected duplicate-tag error, got %v", err)
	}
}

func TestVLESSREALITYRendersFlowAndPubkey(t *testing.T) {
	c := Config{
		Outbounds: []Outbound{
			{
				Tag:    "tokyo",
				Type:   OutboundVLESS,
				Server: "tokyo.example.com",
				Port:   443,
				UUID:   "12345678-1234-1234-1234-123456789012",
				TLS: &TLSOptions{
					Enabled:         true,
					SNI:             "www.microsoft.com",
					UTLSFingerprint: "chrome",
					VLESSFlow:       "xtls-rprx-vision",
					REALITY: &REALITYOptions{
						Enabled:   true,
						PublicKey: "pubkeybase64here",
						ShortID:   "abcd1234",
					},
				},
			},
		},
	}
	js, err := c.RenderJSON()
	if err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	for _, want := range []string{
		`"type": "vless"`,
		`"tag": "tokyo"`,
		`"server": "tokyo.example.com"`,
		`"server_port": 443`,
		`"uuid": "12345678-1234-1234-1234-123456789012"`,
		`"flow": "xtls-rprx-vision"`,
		`"server_name": "www.microsoft.com"`,
		`"public_key": "pubkeybase64here"`,
		`"short_id": "abcd1234"`,
		`"fingerprint": "chrome"`,
	} {
		if !strings.Contains(string(js), want) {
			t.Errorf("output missing %q\n--- output ---\n%s", want, js)
		}
	}
}

func TestWireGuardRendersPeers(t *testing.T) {
	c := Config{
		Outbounds: []Outbound{
			{
				Tag:              "mullvad",
				Type:             OutboundWireGuard,
				Server:           "nl-ams.mullvad.net",
				Port:             51820,
				WGPrivateKey:     "privkeyhere",
				WGPeerPublicKey:  "pubkeyhere",
				WGLocalAddresses: []string{"10.66.66.2/32"},
			},
		},
	}
	js, err := c.RenderJSON()
	if err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	for _, want := range []string{
		`"type": "wireguard"`,
		`"private_key": "privkeyhere"`,
		`"public_key": "pubkeyhere"`,
		`"server": "nl-ams.mullvad.net"`,
		`"server_port": 51820`,
		`"local_address"`,
	} {
		if !strings.Contains(string(js), want) {
			t.Errorf("output missing %q\n--- output ---\n%s", want, js)
		}
	}
}

func TestSelectorRendersMembers(t *testing.T) {
	c := Config{
		Outbounds: []Outbound{
			{Tag: "tokyo", Type: OutboundDirect},
			{Tag: "frankfurt", Type: OutboundDirect},
			{
				Tag:                "auto",
				Type:               OutboundURLTest,
				Members:            []string{"tokyo", "frankfurt"},
				URLTestIntervalSec: 60,
			},
		},
	}
	js, err := c.RenderJSON()
	if err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	for _, want := range []string{
		`"type": "urltest"`,
		`"interval": "60s"`,
		`"url": "http://www.gstatic.com/generate_204"`,
		`"tokyo"`,
		`"frankfurt"`,
	} {
		if !strings.Contains(string(js), want) {
			t.Errorf("output missing %q\n--- output ---\n%s", want, js)
		}
	}
}

func TestRouteRuleSourceIP(t *testing.T) {
	c := Config{
		Outbounds: []Outbound{
			{Tag: "tokyo", Type: OutboundDirect},
		},
		Routes: []RouteRule{
			{
				Outbound:     "tokyo",
				SourceIPCIDR: []string{"192.168.42.55/32"},
			},
			PreLANBypass("192.168.42.0/24"),
		},
	}
	js, err := c.RenderJSON()
	if err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	for _, want := range []string{
		`"source_ip_cidr"`,
		`"192.168.42.55/32"`,
		`"192.168.42.0/24"`,
		`"127.0.0.0/8"`,
		`"10.0.0.0/8"`,
	} {
		if !strings.Contains(string(js), want) {
			t.Errorf("output missing %q\n--- output ---\n%s", want, js)
		}
	}
}

func TestRouteRuleRequiresOutbound(t *testing.T) {
	c := Config{
		Outbounds: []Outbound{{Tag: "x", Type: OutboundDirect}},
		Routes: []RouteRule{
			{SourceIPCIDR: []string{"1.2.3.4/32"}}, // no outbound
		},
	}
	if _, err := c.RenderJSON(); err == nil {
		t.Error("expected error for empty outbound on route rule")
	}
}

func TestREALITYValidate(t *testing.T) {
	cases := []struct {
		name string
		t    *TLSOptions
		ok   bool
	}{
		{"nil", nil, true},
		{"disabled", &TLSOptions{}, true},
		{"reality without pubkey", &TLSOptions{
			Enabled: true, SNI: "x.com",
			REALITY: &REALITYOptions{Enabled: true},
		}, false},
		{"reality without SNI", &TLSOptions{
			Enabled: true,
			REALITY: &REALITYOptions{Enabled: true, PublicKey: "abc"},
		}, false},
		{"reality complete", &TLSOptions{
			Enabled: true, SNI: "x.com",
			REALITY: &REALITYOptions{Enabled: true, PublicKey: "abc"},
		}, true},
	}
	for _, c := range cases {
		err := c.t.Validate()
		if (err == nil) != c.ok {
			t.Errorf("%s: ok=%v err=%v", c.name, c.ok, err)
		}
	}
}

func TestVLESSWithoutUUIDFails(t *testing.T) {
	c := Config{
		Outbounds: []Outbound{
			{Tag: "x", Type: OutboundVLESS, Server: "h", Port: 443},
		},
	}
	if _, err := c.RenderJSON(); err == nil {
		t.Error("expected error for VLESS without UUID")
	}
}

func TestShadowsocksRequiresMethodAndPassword(t *testing.T) {
	for _, c := range []Config{
		{Outbounds: []Outbound{{Tag: "x", Type: OutboundShadowsocks, Server: "h", Port: 443}}},
		{Outbounds: []Outbound{{Tag: "x", Type: OutboundShadowsocks, Server: "h", Port: 443, Method: "aes-256-gcm"}}},
	} {
		if _, err := c.RenderJSON(); err == nil {
			t.Error("expected error for incomplete SS outbound")
		}
	}
}

func TestWebSocketTransportRendersHeaders(t *testing.T) {
	c := Config{
		Outbounds: []Outbound{
			{
				Tag: "ws", Type: OutboundVLESS, Server: "h", Port: 443,
				UUID:      "12345678-1234-1234-1234-123456789012",
				Transport: "ws", WSPath: "/api/v1", WSHost: "cdn.example.com",
			},
		},
	}
	js, err := c.RenderJSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"type": "ws"`,
		`"path": "/api/v1"`,
		`"Host": "cdn.example.com"`,
	} {
		if !strings.Contains(string(js), want) {
			t.Errorf("missing %q in:\n%s", want, js)
		}
	}
}

func TestPreLANBypassAlwaysDirect(t *testing.T) {
	r := PreLANBypass("192.168.42.0/24")
	if r.Outbound != "direct" {
		t.Errorf("PreLANBypass.Outbound = %q, want direct", r.Outbound)
	}
}
