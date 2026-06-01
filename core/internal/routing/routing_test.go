package routing

import (
	"strings"
	"testing"

	"github.com/knot-os/knot-os/core/internal/deviceregistry"
	"github.com/knot-os/knot-os/core/internal/profile"
	"github.com/knot-os/knot-os/core/internal/singbox"
)

// fakeOutbound builds a minimal valid VLESS outbound with the
// given Tag, so RenderJSON accepts it.
func fakeOutbound(tag string) singbox.Outbound {
	return singbox.Outbound{
		Tag:    tag,
		Type:   singbox.OutboundVLESS,
		Server: "h.example.com",
		Port:   443,
		UUID:   "12345678-1234-1234-1234-123456789012",
	}
}

func TestBuildRequiresLANCIDR(t *testing.T) {
	if _, err := Build(Inputs{}); err == nil {
		t.Error("expected error without LANCIDR")
	}
}

func TestBuildEmptyInputsProducesValidConfig(t *testing.T) {
	res, err := Build(Inputs{LANCIDR: "192.168.42.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := res.Config.RenderJSON(); err != nil {
		t.Fatalf("rendered config invalid: %v", err)
	}
	if len(res.Config.Routes) != 1 {
		t.Errorf("expected just LAN bypass route, got %d", len(res.Config.Routes))
	}
}

func TestBuildDirectProfileEmitsNoRule(t *testing.T) {
	res, _ := Build(Inputs{
		LANCIDR: "192.168.42.0/24",
		Devices: []deviceregistry.Device{
			{MAC: "aa:bb:cc:dd:ee:01", IP: "192.168.42.10", ProfileID: "default"},
		},
		Profiles: []profile.Profile{
			{ID: "default", Name: "Default"}, // RouteVia empty → direct
		},
	})
	// Only the LAN bypass should be in routes — no per-device rule
	// for a direct-profile device.
	if len(res.Config.Routes) != 1 {
		t.Errorf("expected 1 route, got %d", len(res.Config.Routes))
	}
	got := res.DeviceRoutes["aa:bb:cc:dd:ee:01"]
	if got.Status != "direct" {
		t.Errorf("status=%q, want direct", got.Status)
	}
}

func TestBuildTunnelProfileEmitsSourceRule(t *testing.T) {
	tag := "myprovider:srv001"
	res, _ := Build(Inputs{
		LANCIDR:   "192.168.42.0/24",
		Outbounds: []singbox.Outbound{fakeOutbound(tag)},
		Devices: []deviceregistry.Device{
			{MAC: "aa:bb:cc:dd:ee:01", IP: "192.168.42.50", ProfileID: "kids"},
		},
		Profiles: []profile.Profile{
			{ID: "kids", Name: "Kids", RouteVia: tag},
		},
	})
	// Expect 2 routes: LAN bypass + the per-device rule.
	if len(res.Config.Routes) != 2 {
		t.Fatalf("got %d routes, want 2", len(res.Config.Routes))
	}
	r := res.Config.Routes[1]
	if r.Outbound != tag {
		t.Errorf("Outbound=%q, want %q", r.Outbound, tag)
	}
	if len(r.SourceIPCIDR) != 1 || r.SourceIPCIDR[0] != "192.168.42.50/32" {
		t.Errorf("SourceIPCIDR=%v", r.SourceIPCIDR)
	}
	if res.DeviceRoutes["aa:bb:cc:dd:ee:01"].Status != "tunnel" {
		t.Errorf("device status=%q", res.DeviceRoutes["aa:bb:cc:dd:ee:01"].Status)
	}

	// And the rendered JSON must round-trip through RenderJSON.
	js, err := res.Config.RenderJSON()
	if err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	if !strings.Contains(string(js), `"192.168.42.50/32"`) {
		t.Errorf("source IP not in rendered config\n%s", js)
	}
}

func TestBuildKillSwitchOnMissingServer(t *testing.T) {
	res, _ := Build(Inputs{
		LANCIDR: "192.168.42.0/24",
		// Note: outbound list is empty — the requested tag is "missing".
		Devices: []deviceregistry.Device{
			{MAC: "aa:bb:cc:dd:ee:01", IP: "192.168.42.50", ProfileID: "kids"},
		},
		Profiles: []profile.Profile{
			{ID: "kids", Name: "Kids", RouteVia: "myprovider:vanished"},
		},
	})
	if len(res.Config.Routes) != 2 {
		t.Fatalf("got %d routes, want 2", len(res.Config.Routes))
	}
	r := res.Config.Routes[1]
	if r.Outbound != "block" {
		t.Errorf("kill-switch should yield outbound=block, got %q", r.Outbound)
	}
	if got := res.DeviceRoutes["aa:bb:cc:dd:ee:01"].Status; got != "kill" {
		t.Errorf("status=%q, want kill", got)
	}
	if len(res.MissingOutbounds) != 1 || res.MissingOutbounds[0] != "myprovider:vanished" {
		t.Errorf("MissingOutbounds=%v", res.MissingOutbounds)
	}
}

func TestBuildSkipsDevicesWithoutLease(t *testing.T) {
	res, _ := Build(Inputs{
		LANCIDR:   "192.168.42.0/24",
		Outbounds: []singbox.Outbound{fakeOutbound("p:s")},
		Devices: []deviceregistry.Device{
			// No IP — known device that hasn't been seen this boot.
			{MAC: "aa:bb:cc:dd:ee:01", ProfileID: "kids"},
		},
		Profiles: []profile.Profile{
			{ID: "kids", Name: "Kids", RouteVia: "p:s"},
		},
	})
	if len(res.Config.Routes) != 1 {
		t.Errorf("device without lease should not produce a rule, routes=%d",
			len(res.Config.Routes))
	}
	if got := res.DeviceRoutes["aa:bb:cc:dd:ee:01"].Status; got != "direct" {
		t.Errorf("no-lease device status=%q, want direct", got)
	}
}

func TestBuildDeterministic(t *testing.T) {
	in := Inputs{
		LANCIDR:   "192.168.42.0/24",
		Outbounds: []singbox.Outbound{fakeOutbound("p:srv1"), fakeOutbound("p:srv2")},
		Devices: []deviceregistry.Device{
			{MAC: "bb:bb:bb:bb:bb:bb", IP: "192.168.42.20", ProfileID: "kids"},
			{MAC: "aa:aa:aa:aa:aa:aa", IP: "192.168.42.10", ProfileID: "kids"},
		},
		Profiles: []profile.Profile{{ID: "kids", Name: "Kids", RouteVia: "p:srv1"}},
	}
	r1, _ := Build(in)
	r2, _ := Build(in)

	js1, err := r1.Config.RenderJSON()
	if err != nil {
		t.Fatal(err)
	}
	js2, err := r2.Config.RenderJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(js1) != string(js2) {
		t.Error("Build is not deterministic across calls with identical input")
	}

	// Specifically: the device with the smaller MAC should appear
	// first in the route list.
	if !strings.Contains(string(js1), `"192.168.42.10/32"`) {
		t.Error("missing first device's CIDR")
	}
	idxA := strings.Index(string(js1), `"192.168.42.10/32"`)
	idxB := strings.Index(string(js1), `"192.168.42.20/32"`)
	if idxA > idxB {
		t.Error("devices not sorted by MAC in rendered config")
	}
}

func TestBuildEnablesTUNWhenDeviceRouted(t *testing.T) {
	// No user outbounds → TUN stays off (no point bringing up an
	// interface to route nothing).
	res, _ := Build(Inputs{LANCIDR: "192.168.42.0/24"})
	if res.Config.TUN != nil {
		t.Error("TUN should not be set when there are no user outbounds")
	}

	// An outbound exists but no device is pinned to it → still off.
	// The TUN only matters once a device actually needs to be routed.
	res, _ = Build(Inputs{
		LANCIDR:   "192.168.42.0/24",
		Outbounds: []singbox.Outbound{fakeOutbound("p:s")},
	})
	if res.Config.TUN != nil {
		t.Error("TUN should stay off when no device is routed, even with a server present")
	}

	// A device pinned to the server → TUN auto-route on.
	res, _ = Build(Inputs{
		LANCIDR:   "192.168.42.0/24",
		Outbounds: []singbox.Outbound{fakeOutbound("p:s")},
		Devices: []deviceregistry.Device{
			{MAC: "aa:bb:cc:dd:ee:01", IP: "192.168.42.5", ProfileID: "kids"},
		},
		Profiles: []profile.Profile{
			{ID: "kids", Name: "Kids", RouteVia: "p:s"},
		},
	})
	if res.Config.TUN == nil {
		t.Fatal("TUN should be set when at least one device is routed")
	}
	if !res.Config.TUN.AutoRoute || !res.Config.TUN.StrictRoute {
		t.Errorf("TUN should default to AutoRoute+StrictRoute, got %+v", res.Config.TUN)
	}
}

func TestBuildDropsUnrenderableOutboundAndKillSwitches(t *testing.T) {
	// A server NEITHER engine can render (here: a bogus transport)
	// must not fail the whole build. It's dropped from both configs,
	// and any device pinned to it is kill-switched (block) and
	// reported in MissingOutbounds — never left leaking via direct.
	bad := singbox.Outbound{
		Tag:       "vpnus:bad",
		Type:      singbox.OutboundVLESS,
		Server:    "example.com",
		Port:      443,
		UUID:      "u",
		Transport: "carrier-pigeon", // unsupported by sing-box AND xray
	}
	res, err := Build(Inputs{
		LANCIDR:   "192.168.42.0/24",
		Outbounds: []singbox.Outbound{bad},
		Devices: []deviceregistry.Device{
			{MAC: "aa:bb:cc:dd:ee:02", IP: "192.168.42.7", ProfileID: "me"},
		},
		Profiles: []profile.Profile{
			{ID: "me", Name: "Me", RouteVia: "vpnus:bad"},
		},
	})
	if err != nil {
		t.Fatalf("Build should not error on an unrenderable outbound: %v", err)
	}
	// The bad outbound is not in the rendered config.
	for _, o := range res.Config.Outbounds {
		if o.Tag == "vpnus:bad" {
			t.Error("unrenderable outbound should have been dropped from the config")
		}
	}
	// The rendered config is actually valid (this is the whole point).
	if _, err := res.Config.RenderJSON(); err != nil {
		t.Fatalf("config with a dropped outbound should still render: %v", err)
	}
	// The device is kill-switched, not direct.
	dr := res.DeviceRoutes["aa:bb:cc:dd:ee:02"]
	if dr.Status != "kill" || dr.Outbound != "block" {
		t.Errorf("device pinned to a dropped server should kill-switch, got %+v", dr)
	}
	if len(res.MissingOutbounds) != 1 || res.MissingOutbounds[0] != "vpnus:bad" {
		t.Errorf("dropped server should be reported missing, got %v", res.MissingOutbounds)
	}
	// And the kill-switch is enforceable: TUN is up so block applies.
	if res.Config.TUN == nil {
		t.Error("TUN should be set so the kill-switch (block) is actually enforced")
	}
}

func TestBuildRoutesXHTTPServerThroughXray(t *testing.T) {
	// An xhttp server sing-box can't speak is hosted by Xray: it
	// shows up as an Xray upstream, sing-box gets a matching loopback
	// SOCKS outbound, and a device pinned to it tunnels (not kills).
	xh := singbox.Outbound{
		Tag:       "vpnus:xh",
		Type:      singbox.OutboundVLESS,
		Server:    "edge.example.com",
		Port:      443,
		UUID:      "11111111-2222-3333-4444-555555555555",
		Transport: "xhttp",
		XHTTPPath: "/d",
		TLS: &singbox.TLSOptions{
			Enabled: true, SNI: "www.microsoft.com",
			REALITY: &singbox.REALITYOptions{Enabled: true, PublicKey: "PBK"},
		},
	}
	res, err := Build(Inputs{
		LANCIDR:   "192.168.42.0/24",
		Outbounds: []singbox.Outbound{xh},
		Devices: []deviceregistry.Device{
			{MAC: "aa:bb:cc:dd:ee:03", IP: "192.168.42.9", ProfileID: "me"},
		},
		Profiles: []profile.Profile{
			{ID: "me", Name: "Me", RouteVia: "vpnus:xh"},
		},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	// Xray hosts it.
	if len(res.XrayConfig.Upstreams) != 1 || res.XrayConfig.Upstreams[0].Tag != "vpnus:xh" {
		t.Fatalf("xhttp server should be an xray upstream, got %+v", res.XrayConfig.Upstreams)
	}
	port := res.XrayConfig.Upstreams[0].SocksPort
	// sing-box has a matching socks outbound pointing at the loopback port.
	var found bool
	for _, o := range res.Config.Outbounds {
		if o.Tag == "vpnus:xh" {
			found = true
			if o.Type != singbox.OutboundSocks || o.Server != "127.0.0.1" || o.Port != port {
				t.Errorf("sing-box socks outbound wrong: %+v (want port %d)", o, port)
			}
		}
	}
	if !found {
		t.Error("sing-box config missing the loopback socks outbound for the xray-hosted server")
	}
	// The device tunnels through it, not kill-switched.
	dr := res.DeviceRoutes["aa:bb:cc:dd:ee:03"]
	if dr.Status != "tunnel" || dr.Outbound != "vpnus:xh" {
		t.Errorf("device should tunnel via xray-hosted server, got %+v", dr)
	}
	if len(res.MissingOutbounds) != 0 {
		t.Errorf("xhttp server is usable via xray, should not be missing: %v", res.MissingOutbounds)
	}
	// Both engine configs render cleanly.
	if _, err := res.Config.RenderJSON(); err != nil {
		t.Errorf("sing-box render: %v", err)
	}
	if _, err := res.XrayConfig.RenderJSON(); err != nil {
		t.Errorf("xray render: %v", err)
	}
}

func TestBuildLANBypassAlwaysFirst(t *testing.T) {
	res, _ := Build(Inputs{
		LANCIDR:   "10.0.0.0/24",
		Outbounds: []singbox.Outbound{fakeOutbound("p:s")},
		Devices: []deviceregistry.Device{
			{MAC: "aa:bb:cc:dd:ee:01", IP: "10.0.0.5", ProfileID: "kids"},
		},
		Profiles: []profile.Profile{
			{ID: "kids", Name: "Kids", RouteVia: "p:s"},
		},
	})
	if res.Config.Routes[0].Outbound != "direct" {
		t.Errorf("LAN bypass not first: %+v", res.Config.Routes[0])
	}
	// Bypass IP list must include 10.0.0.0/24.
	found := false
	for _, c := range res.Config.Routes[0].IPCIDR {
		if c == "10.0.0.0/24" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("LAN CIDR not in bypass: %v", res.Config.Routes[0].IPCIDR)
	}
}
