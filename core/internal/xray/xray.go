// Package xray renders the JSON configuration for an Xray-core
// process and manages its lifecycle, mirroring core/internal/singbox.
//
// Why a second engine: sing-box owns the TUN + per-device routing
// front end, but it deliberately does not implement some transports
// the Russian-market 2026 ecosystem leans on — chiefly "xhttp"
// (formerly "splithttp"). Rather than fork sing-box, KnotOS runs
// Xray-core alongside it as a pure upstream: for every server
// sing-box can't speak, Xray exposes a localhost SOCKS inbound that
// fronts the real outbound, and sing-box dials that SOCKS port.
// sing-box keeps doing TUN/auto_route + per-device source-IP rules;
// Xray just does the protocol heavy lifting for the servers handed
// to it.
//
// Config rendering is pure (no syscalls, no disk) so it's unit
// tested on any host; the process supervision lives behind the
// Runner interface, implemented for real only on Linux.
package xray

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/knot-os/knot-os/core/internal/singbox"
)

// Version is the pinned Xray-core release. image/build.sh reads it
// (via cmd/print-xray-version) to fetch + stage the matching binary,
// so this constant is the single source of truth.
//
// Must be a real Xray-core tag that ships XHTTP (the transport that
// justifies this engine's existence). Verified working on-device at
// 26.3.27; bump here and the image fetcher (image/build.sh, asset
// Xray-linux-arm64-v8a.zip) picks it up on the next build.
const Version = "26.3.27"

// BinPath is where the image stage installs the static binary.
const BinPath = "/usr/local/bin/xray"

// ConfigPath is where Manager writes the rendered JSON. Lives in the
// tmpfs runtime dir so it's wiped on reboot.
const ConfigPath = "/run/knot/xray.json"

// SocksBasePort is the first localhost port handed to an Xray SOCKS
// inbound. The Nth upstream listens on SocksBasePort+N. Bound to
// 127.0.0.1 only, never exposed on the LAN.
const SocksBasePort = 10800

// Upstream is one server Xray hosts for sing-box: a loopback SOCKS5
// inbound on SocksPort fronting the real Outbound.
type Upstream struct {
	// Tag is the sing-box outbound tag ("<sub-id>:<server-id>"); the
	// sing-box side renders a socks outbound with this same tag
	// pointing at 127.0.0.1:SocksPort.
	Tag string
	// SocksPort is the localhost port the SOCKS inbound listens on.
	SocksPort int
	// Outbound is the real server Xray dials out to.
	Outbound singbox.Outbound
}

// Config is the high-level model: the set of upstreams Xray serves.
// An empty Upstreams slice renders a valid do-nothing config and the
// Manager keeps the process stopped.
type Config struct {
	Upstreams []Upstream
	// LogLevel is Xray's loglevel ("debug" "info" "warning" "error"
	// "none"); "warning" by default.
	LogLevel string
}

// RenderJSON turns the Config into the JSON Xray-core reads on
// startup. Deterministic: upstreams are emitted in Tag order so a
// no-op apply leaves the file byte-identical (mtime-stable, which
// the Manager relies on for reload-on-change).
func (c Config) RenderJSON() ([]byte, error) {
	ups := append([]Upstream(nil), c.Upstreams...)
	sort.Slice(ups, func(i, j int) bool { return ups[i].Tag < ups[j].Tag })

	logLevel := c.LogLevel
	if logLevel == "" {
		logLevel = "warning"
	}

	inbounds := make([]any, 0, len(ups))
	outbounds := make([]any, 0, len(ups)+1)
	rules := make([]any, 0, len(ups))
	seenPort := map[int]bool{}
	seenTag := map[string]bool{}

	for _, u := range ups {
		if u.Tag == "" {
			return nil, fmt.Errorf("xray: upstream with empty tag")
		}
		if seenTag[u.Tag] {
			return nil, fmt.Errorf("xray: duplicate upstream tag %q", u.Tag)
		}
		seenTag[u.Tag] = true
		if u.SocksPort <= 0 {
			return nil, fmt.Errorf("xray: upstream %q has no socks port", u.Tag)
		}
		if seenPort[u.SocksPort] {
			return nil, fmt.Errorf("xray: duplicate socks port %d", u.SocksPort)
		}
		seenPort[u.SocksPort] = true

		inTag := "in-" + u.Tag
		outTag := "out-" + u.Tag

		inbounds = append(inbounds, map[string]any{
			"tag":      inTag,
			"listen":   "127.0.0.1",
			"port":     u.SocksPort,
			"protocol": "socks",
			"settings": map[string]any{
				"auth": "noauth",
				"udp":  true,
			},
		})

		ob, err := renderOutbound(u.Outbound, outTag)
		if err != nil {
			return nil, err
		}
		outbounds = append(outbounds, ob)

		rules = append(rules, map[string]any{
			"type":        "field",
			"inboundTag":  []string{inTag},
			"outboundTag": outTag,
		})
	}

	// A freedom outbound is the catch-all; nothing should hit it
	// (every inbound is pinned by a routing rule) but Xray wants a
	// default present.
	outbounds = append(outbounds, map[string]any{
		"protocol": "freedom",
		"tag":      "direct",
	})

	doc := map[string]any{
		"log":       map[string]any{"loglevel": logLevel},
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"routing": map[string]any{
			"domainStrategy": "AsIs",
			"rules":          rules,
		},
	}
	return json.MarshalIndent(doc, "", "  ")
}

// HasUpstreams reports whether the config has any server to host.
func (c Config) HasUpstreams() bool { return len(c.Upstreams) > 0 }
