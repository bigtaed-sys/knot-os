// Package tgproxy integrates d0mhate's tg-ws-proxy — a local
// SOCKS5/MTProto proxy that tunnels Telegram traffic to the Telegram
// data centres over WebSocket+TLS, so Telegram keeps working where it's
// throttled/blocked (Russia). Telegram *client apps* on the LAN point
// their proxy setting at the router; the app then reaches Telegram
// through the bypass.
//
// This mirrors the zapret integration: the proxy is a separate static
// binary (arm64, CGO-free) that knotd supervises. It ships either
// image-staged or downloaded on demand (sha256-pinned), never built from
// source here (the upstream module needs a newer Go toolchain than ours).
//
// Off by default; a bad proxy can't affect NAT/forwarding because it's
// just a userspace listener on a LAN port.
package tgproxy

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
)

const (
	// Version is the pinned d0mhate/tg-ws-proxy release.
	Version = "1.4.1"
	// BinSHA256 is the sha256 of the arm64 (openwrt-aarch64, static)
	// binary — verified before the downloaded binary is ever executed.
	BinSHA256 = "eba7170c7bf15237dc79a4954fe44c73eaea5614d731491e6481bf6cfc18ce78"
	// DownloadURL is the pinned static arm64 binary. openwrt-aarch64 is a
	// CGO-free static ELF, so it runs on Raspberry Pi OS (glibc) too.
	DownloadURL = "https://github.com/d0mhate/-tg-ws-proxy-Manager-go/releases/download/v1.4.1/tg-ws-proxy-openwrt-aarch64"

	// RuntimeDir is the writable root for the downloaded binary.
	RuntimeDir = "/var/lib/knot/tgproxy"
	// ImageBinPath is where image/build.sh may stage the binary. Preferred
	// over a downloaded copy when present.
	ImageBinPath = "/usr/local/bin/tg-ws-proxy"

	// DefaultPort is the LAN listen port for the proxy.
	DefaultPort = 8443
)

// Settings is the live proxy configuration the Manager acts on. The proxy
// always runs in MTProto mode (the tg://proxy link kind Telegram apps
// expect) — SOCKS5 was dropped as unused.
type Settings struct {
	// Enabled turns the proxy process on.
	Enabled bool
	// Port is the LAN listen port.
	Port int
	// Secret is the 32-hex MTProto secret. Empty → the Manager generates
	// one on first enable.
	Secret string
	// LinkIP is the address to embed in the tg:// link — the router's
	// LAN gateway IP, or a public IP for remote use. Empty omits it.
	LinkIP string
}

// BuildArgs renders the tg-ws-proxy CLI arguments for s (MTProto mode).
func BuildArgs(s Settings) []string {
	port := s.Port
	if port == 0 {
		port = DefaultPort
	}
	args := []string{
		"--host", "0.0.0.0",
		"--port", strconv.Itoa(port),
		"--mode", "mtproto",
		// Route via Cloudflare using the binary's built-in domains
		// (--cf-proxy with no --cf-domain falls back to the embedded
		// defaults). This is what makes it work out of the box where the
		// direct Telegram WebSocket route is DPI-blocked; --cf-proxy-first
		// skips the blocked direct attempt so clients don't hang.
		"--cf-proxy",
		"--cf-proxy-first",
		"--secret", s.Secret,
	}
	if s.LinkIP != "" {
		args = append(args, "--link-ip", s.LinkIP)
	}
	return args
}

// TGLink builds the tg://proxy link a Telegram user taps to add the
// router as an MTProto proxy. Empty when incomplete.
func TGLink(s Settings) string {
	if s.Secret == "" || s.LinkIP == "" {
		return ""
	}
	port := s.Port
	if port == 0 {
		port = DefaultPort
	}
	q := url.Values{}
	q.Set("server", s.LinkIP)
	q.Set("port", strconv.Itoa(port))
	q.Set("secret", s.Secret)
	return "tg://proxy?" + q.Encode()
}

// GenerateSecret returns a fresh 32-hex-character MTProto secret.
func GenerateSecret() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate secret: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// ValidSecret reports whether s is a plain 32-hex MTProto secret.
func ValidSecret(s string) bool {
	if len(s) != 32 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
