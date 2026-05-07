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

func TestBuildEnablesTUNWhenOutboundsPresent(t *testing.T) {
	// No user outbounds → TUN stays off (no point bringing up an
	// interface to route nothing).
	res, _ := Build(Inputs{LANCIDR: "192.168.42.0/24"})
	if res.Config.TUN != nil {
		t.Error("TUN should not be set when there are no user outbounds")
	}

	// Outbound present → TUN auto-route on.
	res, _ = Build(Inputs{
		LANCIDR:   "192.168.42.0/24",
		Outbounds: []singbox.Outbound{fakeOutbound("p:s")},
	})
	if res.Config.TUN == nil {
		t.Fatal("TUN should be set when at least one user outbound exists")
	}
	if !res.Config.TUN.AutoRoute || !res.Config.TUN.StrictRoute {
		t.Errorf("TUN should default to AutoRoute+StrictRoute, got %+v", res.Config.TUN)
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
