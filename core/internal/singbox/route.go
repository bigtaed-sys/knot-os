package singbox

import (
	"errors"
)

// RouteRule is one entry in the `route.rules` array. Matching is
// AND-of-fields: a rule fires when every populated field matches
// the inbound packet. Empty fields are wildcards.
//
// We only model the matchers our v0.5 routing layer needs (M28
// will hook these up):
//
//   SourceIPCIDR  — per-device routing keyed off DHCP-leased IPs
//   SourcePort    — rare, exposed for completeness
//   IPCIDR        — destination-IP filters (e.g. "always direct
//                   for the LAN gateway itself")
//   Protocol      — "dns", "http", ... — used to pin DNS to the
//                   built-in resolver
//   Inbound       — match on the inbound tag (TUN, SOCKS5, etc.)
//   Network       — "tcp" or "udp"
//
// Skipped: SNI / domain matching (we want destination-blind
// per-device routing in v0.5), GeoIP (adds 100 MB of data), DNS
// rule fields (we don't run sing-box's DNS layer for the LAN).
type RouteRule struct {
	// Outbound is the tag of the outbound to dispatch to. Required.
	Outbound string

	// Inbound, when non-empty, restricts the match to traffic
	// arriving on the given inbound tag.
	Inbound []string

	// Network filters by L4 type ("tcp" / "udp").
	Network string

	// Protocol filters by sing-box's protocol sniffer ("dns",
	// "http", "tls", "quic").
	Protocol string

	// SourceIPCIDR is a list of CIDRs the source IP must be in
	// for this rule to match. THIS is the per-device hook —
	// each LAN device's static-leased /32 lands here.
	SourceIPCIDR []string

	// DomainSuffix matches the sniffed destination domain (SNI for
	// TLS, Host for HTTP, QUIC ALPN). Combined with SourceIPCIDR it
	// expresses split-tunnelling: "for this device, only these
	// domains go via the tunnel". Requires sniff on the inbound,
	// which the TUN inbound always enables.
	DomainSuffix []string

	// SourcePort matches the L4 source port. Rare in our use case
	// (mostly for DNS pinning when source is :53).
	SourcePort []int

	// IPCIDR matches the destination IP. Used to keep LAN traffic
	// off the tunnel ("always direct for 192.168.42.0/24").
	IPCIDR []string

	// Port matches the L4 destination port.
	Port []int
}

func (r RouteRule) toJSON() (map[string]any, error) {
	if r.Outbound == "" {
		return nil, errors.New("route rule: empty outbound")
	}
	out := map[string]any{
		"outbound": r.Outbound,
	}
	if len(r.Inbound) > 0 {
		out["inbound"] = r.Inbound
	}
	if r.Network != "" {
		out["network"] = r.Network
	}
	if r.Protocol != "" {
		out["protocol"] = r.Protocol
	}
	if len(r.SourceIPCIDR) > 0 {
		out["source_ip_cidr"] = r.SourceIPCIDR
	}
	if len(r.DomainSuffix) > 0 {
		out["domain_suffix"] = r.DomainSuffix
	}
	if len(r.SourcePort) > 0 {
		out["source_port"] = r.SourcePort
	}
	if len(r.IPCIDR) > 0 {
		out["ip_cidr"] = r.IPCIDR
	}
	if len(r.Port) > 0 {
		out["port"] = r.Port
	}
	return out, nil
}

// PreLANBypass is the standard rule we always want at the top of
// the rules list when sing-box is wired into the LAN: anything
// destined for the LAN itself or RFC1918 private space goes
// "direct" (out the regular routing table), no point taking it
// through the tunnel. M28 prepends this to its render.
func PreLANBypass(lanCIDR string) RouteRule {
	return RouteRule{
		Outbound: "direct",
		IPCIDR: []string{
			"127.0.0.0/8",
			"10.0.0.0/8",
			"172.16.0.0/12",
			"192.168.0.0/16",
			"169.254.0.0/16",
			lanCIDR, // explicit, in case the LAN sits inside one of the above
		},
	}
}
