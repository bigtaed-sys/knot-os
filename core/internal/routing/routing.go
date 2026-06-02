// Package routing assembles a sing-box config from the live state
// of (devices, profiles, subscriptions). The output is a single
// singbox.Config the supervisor renders to disk and reloads.
//
// This package is the bridge between v0.5's "user pasted a
// subscription, then assigned the kids profile to route through
// Tokyo" state and the actual JSON that sing-box runs on. It is
// pure — no syscalls, no disk, no clock — so it lives outside
// network/linux and is exercised by table tests on every host.
//
// Routing model:
//
//   * Each Profile may set RouteVia = "<sub-id>:<server-id>". A
//     device assigned to that profile sees its source-IP /32 turned
//     into a sing-box route rule that pins it to the matching
//     outbound.
//   * Profiles with no RouteVia (or RouteVia="direct") generate no
//     rule — those devices fall through to the default "direct"
//     outbound.
//   * The pre-LAN bypass rule is always prepended so RFC1918
//     traffic never accidentally goes through a tunnel.
//   * The kill switch is realized by setting `final` to "block"
//     instead of "direct" for any profile that asks for it (M28b
//     hooks this through). Today RouteVia=foo without a matching
//     server in the registry produces a per-rule outbound of
//     "block" — i.e. the device gets no internet rather than
//     leaking via the LAN gateway.
package routing

import (
	"fmt"
	"sort"

	"github.com/knot-os/knot-os/core/internal/deviceregistry"
	"github.com/knot-os/knot-os/core/internal/profile"
	"github.com/knot-os/knot-os/core/internal/singbox"
	"github.com/knot-os/knot-os/core/internal/subscription"
	"github.com/knot-os/knot-os/core/internal/xray"
)

// Inputs bundles the live state Build needs. Every field is
// optional — missing inputs produce a config with no user
// outbounds (and therefore Manager.Apply skips starting sing-box).
type Inputs struct {
	// Outbounds is the flat list from Registry.AllOutbounds(). Each
	// already has a Tag of the form "<sub-id>:<server-id>".
	Outbounds []singbox.Outbound

	// Devices is the snapshot from deviceregistry.Registry.List().
	Devices []deviceregistry.Device

	// Profiles is the snapshot from profile.Registry.All().
	Profiles []profile.Profile

	// LANCIDR is the LAN's network ("192.168.42.0/24"). Used by
	// PreLANBypass so the tunnel never swallows local traffic.
	LANCIDR string
}

// Result is what Build returns: the rendered config plus per-device
// diagnostics for the UI.
type Result struct {
	// Config is ready to hand to singbox.Manager.Apply.
	Config singbox.Config

	// XrayConfig holds the upstreams sing-box can't speak natively
	// (e.g. xhttp servers). Each is fronted by a loopback SOCKS
	// inbound in Xray; the sing-box Config above carries a matching
	// "socks" outbound so traffic flows sing-box → Xray → server.
	// Hand this to xray.Manager.Apply alongside Config.
	XrayConfig xray.Config

	// DeviceRoutes maps device MAC → (outbound tag, status). status
	// is "direct", "tunnel", or "kill" — the UI uses this for the
	// Маршрутизация page badges.
	DeviceRoutes map[string]DeviceRoute

	// MissingOutbounds lists outbound tags requested by some
	// profile but not present in Outbounds. The UI surfaces these
	// as "the server is gone — re-fetch the subscription or pick
	// another".
	MissingOutbounds []string
}

// DeviceRoute is the per-device routing diagnostic.
type DeviceRoute struct {
	// Outbound tag, "direct", or "block" (kill-switched).
	Outbound string
	// Status is one of "direct", "tunnel", "kill".
	Status string
	// ProfileID is the profile that drove the decision.
	ProfileID string
}

// Build is the pure routing-config render. Returns an empty
// (but valid) Config when no profile has a RouteVia set — the
// supervisor will then keep sing-box stopped.
func Build(in Inputs) (Result, error) {
	if in.LANCIDR == "" {
		return Result{}, fmt.Errorf("routing: LANCIDR required")
	}

	// Index profiles by ID for O(1) lookup.
	profByID := make(map[string]profile.Profile, len(in.Profiles))
	for _, p := range in.Profiles {
		profByID[p.ID] = p
	}

	// Partition outbounds across the two engines:
	//
	//   * sing-box renders it natively      → kept as-is.
	//   * sing-box can't, Xray can (xhttp…) → hosted by a local Xray
	//                                         instance; sing-box gets
	//                                         a loopback SOCKS
	//                                         outbound pointing at it.
	//   * neither engine                    → dropped, so a device
	//                                         pinned to it kill-switches
	//                                         (it's not "known" below)
	//                                         instead of leaking direct.
	//
	// Built-ins (direct, block) are auto-injected by the renderers.
	singboxOutbounds := make([]singbox.Outbound, 0, len(in.Outbounds))
	knownOutbound := make(map[string]bool, len(in.Outbounds))
	xrayByTag := make(map[string]singbox.Outbound)
	xrayTags := make([]string, 0)
	for _, o := range in.Outbounds {
		if o.Tag == "" {
			continue
		}
		if o.Renderable() == nil {
			singboxOutbounds = append(singboxOutbounds, o)
			knownOutbound[o.Tag] = true
			continue
		}
		if xray.CanRender(o) == nil {
			xrayByTag[o.Tag] = o
			xrayTags = append(xrayTags, o.Tag)
			knownOutbound[o.Tag] = true
			continue
		}
		// Neither engine can host it → leave it unknown.
	}

	// Deterministic SOCKS-port assignment: sort the Xray-hosted tags
	// and hand out SocksBasePort+index so a no-op rebuild produces an
	// identical config (stable across reloads, diff-friendly).
	sort.Strings(xrayTags)
	var xrayUpstreams []xray.Upstream
	for i, tag := range xrayTags {
		port := xray.SocksBasePort + i
		xrayUpstreams = append(xrayUpstreams, xray.Upstream{
			Tag:       tag,
			SocksPort: port,
			Outbound:  xrayByTag[tag],
		})
		// sing-box reaches this server through the loopback SOCKS hop.
		singboxOutbounds = append(singboxOutbounds, singbox.Outbound{
			Tag:    tag,
			Type:   singbox.OutboundSocks,
			Server: "127.0.0.1",
			Port:   port,
		})
	}

	// Walk devices in MAC order so the rendered config is stable
	// across runs (test-friendly, diff-friendly).
	devs := append([]deviceregistry.Device(nil), in.Devices...)
	sort.Slice(devs, func(i, j int) bool { return devs[i].MAC < devs[j].MAC })

	cfg := singbox.Config{
		Outbounds: singboxOutbounds,
	}
	// Always start with the LAN bypass — no per-device rule should
	// be able to swallow LAN traffic.
	cfg.Routes = []singbox.RouteRule{singbox.PreLANBypass(in.LANCIDR)}

	deviceRoutes := make(map[string]DeviceRoute, len(devs))
	missingSet := make(map[string]struct{})
	// anyRouted is set once at least one device is pinned to a tunnel
	// or kill-switched. It gates the TUN inbound below: there's no
	// reason to stand up the virtual interface (and have sing-box
	// capture forwarded traffic) until some device actually needs it.
	anyRouted := false

	for _, d := range devs {
		if d.IP == "" {
			// No live lease — can't write a /32 rule. The device
			// will hit the default route (direct) when it joins.
			deviceRoutes[d.MAC] = DeviceRoute{
				Outbound: "direct", Status: "direct", ProfileID: d.ProfileID,
			}
			continue
		}
		p, ok := profByID[d.ProfileID]
		if !ok || p.RouteVia == "" || p.RouteVia == "direct" {
			deviceRoutes[d.MAC] = DeviceRoute{
				Outbound: "direct", Status: "direct", ProfileID: d.ProfileID,
			}
			continue
		}

		// Profile wants a tunnel. If the requested outbound is
		// missing (subscription server vanished after a re-fetch),
		// kill-switch the device — never silently leak via direct.
		out := p.RouteVia
		status := "tunnel"
		if !knownOutbound[out] {
			missingSet[out] = struct{}{}
			out = "block"
			status = "kill"
		}

		// Split tunnel: when the profile lists route domains, only
		// those domains (matched by sniffed SNI) take the tunnel; the
		// rest of the device's traffic falls through to direct. An
		// empty list keeps the whole-device behaviour.
		domains := p.NormalizedRouteDomains()
		rule := singbox.RouteRule{
			Outbound:     out,
			SourceIPCIDR: []string{d.IP + "/32"},
		}
		if len(domains) > 0 {
			rule.DomainSuffix = domains
			if status == "tunnel" {
				status = "split"
			}
		}
		cfg.Routes = append(cfg.Routes, rule)
		deviceRoutes[d.MAC] = DeviceRoute{
			Outbound: out, Status: status, ProfileID: d.ProfileID,
		}
		anyRouted = true
	}

	// Enable the TUN inbound whenever at least one device is routed
	// (tunnel or kill). AutoRoute=true delegates iproute2 + nftables
	// to sing-box, which is the path upstream tests against; mirroring
	// those rules in apply_router.go is a recipe for drift.
	// StrictRoute=true drops any packet that escapes the TUN —
	// belt-and-braces against accidental DNS leaks. Keying this off
	// "a device is routed" (not "a server exists") means the
	// kill-switch is actually enforced even when every subscribed
	// server is unusable: sing-box still comes up with TUN + block so
	// the pinned device is blocked rather than leaking via direct.
	if anyRouted {
		cfg.TUN = &singbox.TUNInbound{
			AutoRoute:   true,
			StrictRoute: true,
		}
	}

	missing := make([]string, 0, len(missingSet))
	for k := range missingSet {
		missing = append(missing, k)
	}
	sort.Strings(missing)

	return Result{
		Config:           cfg,
		XrayConfig:       xray.Config{Upstreams: xrayUpstreams},
		DeviceRoutes:     deviceRoutes,
		MissingOutbounds: missing,
	}, nil
}

// FromRegistries is a convenience wrapper for callers that already
// hold the three live registries — pulls the snapshots and calls
// Build. The dependency on subscription.Registry is concrete here
// because the routing package is the single integration point.
func FromRegistries(
	subs *subscription.Registry,
	devices *deviceregistry.Registry,
	profiles *profile.Registry,
	lanCIDR string,
) (Result, error) {
	in := Inputs{LANCIDR: lanCIDR}
	if subs != nil {
		in.Outbounds = subs.AllOutbounds()
	}
	if devices != nil {
		in.Devices = devices.List()
	}
	if profiles != nil {
		in.Profiles = profiles.List()
	}
	return Build(in)
}
