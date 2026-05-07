// Package singbox renders the JSON configuration that the embedded
// sing-box engine consumes, and wraps its lifecycle as a supervised
// process the way the rest of knotd handles hostapd / dnsmasq.
//
// We deliberately don't try to embed sing-box as a library.
// Embedding would mean pulling in its enormous dependency tree
// (every protocol, every transport, every utls fork) into knotd's
// own binary. The cost in build time, binary size, and CVE
// surface dwarfs the cost of one supervised process. So we ship
// the upstream-built static arm64 binary in the image and feed
// it a config file. Lifecycle is identical to hostapd — start,
// SIGHUP on config change, restart on hard config errors.
//
// What's in v0.5 (M26):
//
//   - Config struct + RenderJSON: turns our knotd-side model
//     into the exact JSON shape sing-box expects.
//   - Outbound types: direct, block, selector (auto-pick), plus
//     stubs for vless / vmess / trojan / shadowsocks / wireguard
//     to be filled by M27 when the subscription parser starts
//     producing them.
//   - Route rules: source_ip_cidr based per-device, defaults to
//     direct.
//   - Health-check endpoint on the clash-API for M28's UI.
//
// What's NOT in v0.5: TUN+TPROXY transparent proxying for the
// LAN. That arrives in M28 alongside the per-device routing
// integration. M26 only stands the engine up with the trivial
// "everything direct" config so the rest of v0.5 can iterate.
package singbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// Version is the sing-box upstream version we test against.
// Stamped at image-build time when the binary is downloaded
// (see image/stage-knot/03-singbox/00-run.sh). knotd reads
// /usr/local/bin/sing-box version at start-up to confirm the
// running binary matches what we render config for.
const Version = "1.10.7"

// ConfigPath is where Manager writes the rendered JSON.
const ConfigPath = "/run/knot/sing-box.json"

// BinPath is where 00-run.sh installs the static binary.
const BinPath = "/usr/local/bin/sing-box"

// LocalAPIPort is sing-box's clash-compatible API. knotd reads
// stats from it (selected outbound, per-server delays, traffic
// counters). Bound to localhost only; not exposed on the LAN.
const LocalAPIPort = 9090

// Config is the high-level model knotd holds about the sing-box
// state. RenderJSON turns it into the exact wire shape the
// upstream consumes.
//
// Only fields we actually use are modelled. Sing-box's full
// config grammar is much larger; embedding all of it would mean
// constantly chasing upstream's evolving schema.
type Config struct {
	// LogLevel is one of "panic" "fatal" "error" "warn" "info"
	// "debug" "trace". "info" by default.
	LogLevel string

	// ClashAPIPort defaults to LocalAPIPort. Set to 0 to disable.
	ClashAPIPort int

	// Outbounds is every server-or-policy the route rules can
	// dispatch to. The "direct" and "block" outbounds are added
	// automatically by RenderJSON if absent — you don't have
	// to specify them.
	Outbounds []Outbound

	// Routes determines which outbound serves which traffic.
	// Evaluated top-to-bottom; the first match wins. The implicit
	// final rule sends unmatched traffic to "direct".
	Routes []RouteRule
}

// RenderJSON turns the Config into the JSON sing-box reads on
// startup. Output is deterministic — outbounds appear in input
// order, routes appear in input order, no map iteration.
func (c Config) RenderJSON() ([]byte, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}

	// Always include direct + block + dns-out built-ins.
	outbounds := make([]map[string]any, 0, len(c.Outbounds)+3)
	hasTag := map[string]bool{}
	for _, o := range c.Outbounds {
		ob, err := o.toJSON()
		if err != nil {
			return nil, fmt.Errorf("outbound %q: %w", o.Tag, err)
		}
		outbounds = append(outbounds, ob)
		hasTag[o.Tag] = true
	}
	if !hasTag["direct"] {
		outbounds = append(outbounds, map[string]any{
			"type": "direct",
			"tag":  "direct",
		})
	}
	if !hasTag["block"] {
		outbounds = append(outbounds, map[string]any{
			"type": "block",
			"tag":  "block",
		})
	}
	if !hasTag["dns-out"] {
		// dns-out is what sing-box's `route.rules` use to pin DNS
		// to the in-process resolver instead of leaking to a real
		// outbound. Required if any route rule references it.
		outbounds = append(outbounds, map[string]any{
			"type": "dns",
			"tag":  "dns-out",
		})
	}

	// Routes.
	rules := make([]map[string]any, 0, len(c.Routes))
	for _, r := range c.Routes {
		rj, err := r.toJSON()
		if err != nil {
			return nil, fmt.Errorf("route rule: %w", err)
		}
		rules = append(rules, rj)
	}

	logLevel := c.LogLevel
	if logLevel == "" {
		logLevel = "info"
	}
	clashPort := c.ClashAPIPort
	if clashPort == 0 {
		clashPort = LocalAPIPort
	}

	doc := map[string]any{
		"log": map[string]any{
			"level":     logLevel,
			"timestamp": true,
		},
		"dns": map[string]any{
			// Sing-box's own DNS layer is intentionally minimal here:
			// our knotd resolver owns DNS for the LAN, sing-box only
			// needs to resolve its own outbound endpoints. Direct
			// upstream to 1.1.1.1 keeps this resolution out of the
			// query log (which is the LAN-side knotd resolver).
			"servers": []map[string]any{
				{"tag": "default", "address": "1.1.1.1", "detour": "direct"},
			},
		},
		"inbounds":  []any{}, // M28 will add the TUN inbound here
		"outbounds": outbounds,
		"route": map[string]any{
			"rules":                 rules,
			"final":                 "direct",
			"auto_detect_interface": true,
		},
	}
	if clashPort > 0 {
		doc["experimental"] = map[string]any{
			"clash_api": map[string]any{
				"external_controller": fmt.Sprintf("127.0.0.1:%d", clashPort),
				// No external UI; we read stats from the clash API
				// over localhost only.
			},
		}
	}

	return jsonEncodeStable(doc)
}

func (c Config) validate() error {
	tags := map[string]bool{}
	for _, o := range c.Outbounds {
		if o.Tag == "" {
			return errors.New("outbound: empty tag")
		}
		if tags[o.Tag] {
			return fmt.Errorf("outbound: duplicate tag %q", o.Tag)
		}
		tags[o.Tag] = true
	}
	for i, r := range c.Routes {
		if r.Outbound == "" {
			return fmt.Errorf("route[%d]: empty outbound tag", i)
		}
	}
	return nil
}

// jsonEncodeStable marshals deterministically — sorted map keys,
// 2-space indent. Unit-test friendly + diff-friendly when the
// rendered file is committed for inspection.
func jsonEncodeStable(v any) ([]byte, error) {
	// json.Marshal already sorts map keys alphabetically; that's
	// stable. Indent for human readability when the rendered
	// config lands on /run/knot/sing-box.json.
	return json.MarshalIndent(v, "", "  ")
}

// Helper used by tests + apply paths to compose the trivial
// "everything direct, no servers yet" config we ship at first
// boot before any subscription is added.
func DefaultsConfig() Config {
	return Config{
		LogLevel:     "info",
		ClashAPIPort: LocalAPIPort,
	}
}

// outboundsByTag is a tiny accessor used by tests + the main
// package's selector validation.
func (c Config) outboundsByTag() map[string]Outbound {
	out := make(map[string]Outbound, len(c.Outbounds))
	for _, o := range c.Outbounds {
		out[o.Tag] = o
	}
	return out
}

// SortedTags returns every outbound tag in stable order. Used by
// the UI when listing servers, and by selector outbounds that
// need a deterministic member list.
func (c Config) SortedTags() []string {
	tags := make([]string, 0, len(c.Outbounds))
	for _, o := range c.Outbounds {
		tags = append(tags, o.Tag)
	}
	sort.Strings(tags)
	return tags
}
