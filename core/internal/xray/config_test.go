package xray

import (
	"encoding/json"
	"testing"

	"github.com/knot-os/knot-os/core/internal/singbox"
)

func xhttpRealityServer() singbox.Outbound {
	return singbox.Outbound{
		Type:      singbox.OutboundVLESS,
		Server:    "edge.example.com",
		Port:      443,
		UUID:      "11111111-2222-3333-4444-555555555555",
		Transport: "xhttp",
		XHTTPPath: "/down",
		XHTTPHost: "edge.example.com",
		XHTTPMode: "auto",
		TLS: &singbox.TLSOptions{
			Enabled:         true,
			SNI:             "www.microsoft.com",
			UTLSFingerprint: "chrome",
			VLESSFlow:       "xtls-rprx-vision",
			REALITY: &singbox.REALITYOptions{
				Enabled:   true,
				PublicKey: "PBKPBKPBK",
				ShortID:   "abcd",
			},
		},
	}
}

func TestRenderXHTTPRealityVLESS(t *testing.T) {
	cfg := Config{Upstreams: []Upstream{
		{Tag: "vpnus:srv1", SocksPort: 10800, Outbound: xhttpRealityServer()},
	}}
	raw, err := cfg.RenderJSON()
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	var doc struct {
		Inbounds []struct {
			Tag      string `json:"tag"`
			Port     int    `json:"port"`
			Listen   string `json:"listen"`
			Protocol string `json:"protocol"`
		} `json:"inbounds"`
		Outbounds []struct {
			Tag            string `json:"tag"`
			Protocol       string `json:"protocol"`
			StreamSettings struct {
				Network         string `json:"network"`
				Security        string `json:"security"`
				RealitySettings struct {
					ServerName  string `json:"serverName"`
					PublicKey   string `json:"publicKey"`
					ShortID     string `json:"shortId"`
					Fingerprint string `json:"fingerprint"`
				} `json:"realitySettings"`
				XHTTPSettings struct {
					Path string `json:"path"`
					Host string `json:"host"`
					Mode string `json:"mode"`
				} `json:"xhttpSettings"`
			} `json:"streamSettings"`
			Settings struct {
				Vnext []struct {
					Address string `json:"address"`
					Port    int    `json:"port"`
					Users   []struct {
						ID   string `json:"id"`
						Flow string `json:"flow"`
					} `json:"users"`
				} `json:"vnext"`
			} `json:"settings"`
		} `json:"outbounds"`
		Routing struct {
			Rules []struct {
				InboundTag  []string `json:"inboundTag"`
				OutboundTag string   `json:"outboundTag"`
			} `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal rendered config: %v\n%s", err, raw)
	}

	// One SOCKS inbound on the assigned localhost port.
	if len(doc.Inbounds) != 1 {
		t.Fatalf("want 1 inbound, got %d", len(doc.Inbounds))
	}
	in := doc.Inbounds[0]
	if in.Tag != "in-vpnus:srv1" || in.Port != 10800 || in.Listen != "127.0.0.1" || in.Protocol != "socks" {
		t.Errorf("inbound wrong: %+v", in)
	}

	// First outbound is the real server; last is freedom/direct.
	if len(doc.Outbounds) != 2 {
		t.Fatalf("want 2 outbounds (server + freedom), got %d", len(doc.Outbounds))
	}
	ob := doc.Outbounds[0]
	if ob.Tag != "out-vpnus:srv1" || ob.Protocol != "vless" {
		t.Errorf("outbound tag/proto wrong: %+v", ob)
	}
	if ob.StreamSettings.Network != "xhttp" {
		t.Errorf("network: want xhttp, got %q", ob.StreamSettings.Network)
	}
	if ob.StreamSettings.Security != "reality" {
		t.Errorf("security: want reality, got %q", ob.StreamSettings.Security)
	}
	if ob.StreamSettings.RealitySettings.PublicKey != "PBKPBKPBK" ||
		ob.StreamSettings.RealitySettings.ServerName != "www.microsoft.com" ||
		ob.StreamSettings.RealitySettings.ShortID != "abcd" ||
		ob.StreamSettings.RealitySettings.Fingerprint != "chrome" {
		t.Errorf("realitySettings wrong: %+v", ob.StreamSettings.RealitySettings)
	}
	if ob.StreamSettings.XHTTPSettings.Path != "/down" ||
		ob.StreamSettings.XHTTPSettings.Mode != "auto" {
		t.Errorf("xhttpSettings wrong: %+v", ob.StreamSettings.XHTTPSettings)
	}
	if len(ob.Settings.Vnext) != 1 || ob.Settings.Vnext[0].Users[0].ID != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("vnext wrong: %+v", ob.Settings.Vnext)
	}
	if ob.Settings.Vnext[0].Users[0].Flow != "xtls-rprx-vision" {
		t.Errorf("flow not carried: %+v", ob.Settings.Vnext[0].Users)
	}

	// Routing pins the inbound to the outbound.
	if len(doc.Routing.Rules) != 1 ||
		doc.Routing.Rules[0].OutboundTag != "out-vpnus:srv1" ||
		len(doc.Routing.Rules[0].InboundTag) != 1 ||
		doc.Routing.Rules[0].InboundTag[0] != "in-vpnus:srv1" {
		t.Errorf("routing rule wrong: %+v", doc.Routing.Rules)
	}
}

func TestCanRenderAcceptsXHTTPRejectsGarbage(t *testing.T) {
	if err := CanRender(xhttpRealityServer()); err != nil {
		t.Errorf("xhttp+reality vless should be renderable by xray: %v", err)
	}
	// Missing UUID → not renderable.
	bad := xhttpRealityServer()
	bad.UUID = ""
	if err := CanRender(bad); err == nil {
		t.Error("vless without UUID should not be renderable")
	}
	// Unknown transport → not renderable.
	bad2 := xhttpRealityServer()
	bad2.Transport = "carrier-pigeon"
	if err := CanRender(bad2); err == nil {
		t.Error("unknown transport should not be renderable")
	}
}

func TestRenderDeterministicAndDedupes(t *testing.T) {
	cfg := Config{Upstreams: []Upstream{
		{Tag: "b:2", SocksPort: 10801, Outbound: xhttpRealityServer()},
		{Tag: "a:1", SocksPort: 10800, Outbound: xhttpRealityServer()},
	}}
	a, err := cfg.RenderJSON()
	if err != nil {
		t.Fatal(err)
	}
	b, err := cfg.RenderJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Error("render not deterministic")
	}

	// Duplicate ports are rejected.
	dup := Config{Upstreams: []Upstream{
		{Tag: "a:1", SocksPort: 10800, Outbound: xhttpRealityServer()},
		{Tag: "a:2", SocksPort: 10800, Outbound: xhttpRealityServer()},
	}}
	if _, err := dup.RenderJSON(); err == nil {
		t.Error("duplicate socks port should be rejected")
	}
}
