package vpn

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// Peer is one WireGuard client authorised to connect.
type Peer struct {
	// ID is a short identifier safe to put in URLs. 8 hex chars.
	ID string `yaml:"id" json:"id"`
	// Name is the user-friendly label ("Phone", "Laptop") shown in
	// the UI alongside the AllowedIP.
	Name string `yaml:"name" json:"name"`
	// PublicKey is the peer's WireGuard public key. Base64-encoded
	// on the wire / on disk.
	PublicKey Key `yaml:"public_key" json:"public_key"`
	// AllowedIP is the /32 inside the server's /24 that traffic
	// from this peer must use as its source address. Assigned by
	// the server at peer-create time; never changes.
	AllowedIP string `yaml:"allowed_ip" json:"allowed_ip"`
	// ProfileID, if set, ties this peer to a profile from the same
	// ProfileRegistry that drives LAN devices. The peer's traffic
	// is then ad-blocked / scheduled by the same code path —
	// "kid's phone gets the kids profile in a cafe too".
	ProfileID string `yaml:"profile_id,omitempty" json:"profile_id,omitempty"`
	// CreatedAt is when the user added the peer in the UI.
	CreatedAt time.Time `yaml:"created_at" json:"created_at"`
	// LastHandshake is live state pulled from the kernel via
	// `wg show wg0 latest-handshakes`. Not persisted.
	LastHandshake time.Time `yaml:"-" json:"last_handshake,omitempty"`
}

// FingerprintPub returns a short, human-friendly identifier for
// the peer's public key — first 8 hex chars of the SHA-256, in
// two 4-char groups. Same shape we use elsewhere for fingerprints.
// Mostly for the UI; never used as a security check.
func (p *Peer) FingerprintPub() string {
	return shortFingerprint(p.PublicKey)
}

// shortFingerprint exists so config.go and the UI handler share a
// rendering. crypto/sha256 import sits in qr.go which is already
// in the package; keep this file dep-free of crypto.
func shortFingerprint(k Key) string {
	hexed := hex.EncodeToString(k[:4])
	return strings.ToUpper(hexed[:4] + " " + hexed[4:8])
}

// AllocateAllowedIP picks the next unused /32 inside cidr for a
// new peer. Skips the server's own address (the .1 of the subnet)
// and any IPs already in use by existing peers.
//
// Returns "10.20.30.5/32"-style strings. Caller stores it on the
// Peer and emits it in the client config; from then on it's
// stable for that peer's lifetime.
func AllocateAllowedIP(serverCIDR string, used []string) (string, error) {
	ip, ipnet, err := net.ParseCIDR(serverCIDR)
	if err != nil {
		return "", fmt.Errorf("vpn: parse server CIDR: %w", err)
	}
	if ip.To4() == nil {
		return "", errors.New("vpn: only IPv4 server subnets are supported")
	}
	mask, _ := ipnet.Mask.Size()
	if mask > 30 {
		return "", fmt.Errorf("vpn: subnet /%d too small", mask)
	}

	// Server takes the .1 of the network. Peers come after.
	taken := make(map[string]bool, len(used)+2)
	for _, u := range used {
		taken[strings.TrimSuffix(u, "/32")] = true
	}
	base := ipnet.IP.To4()
	serverIP := net.IPv4(base[0], base[1], base[2], base[3]+1).To4()
	taken[serverIP.String()] = true
	taken[net.IPv4(base[0], base[1], base[2], 0).String()] = true                                       // network
	taken[net.IPv4(base[0], base[1], base[2], 255).To4().String()] = true                              // broadcast

	for last := byte(2); last < 255; last++ {
		candidate := net.IPv4(base[0], base[1], base[2], last).To4()
		if !taken[candidate.String()] {
			return candidate.String() + "/32", nil
		}
	}
	return "", errors.New("vpn: subnet full")
}

// ValidatePeerName accepts a generous set: letters / digits /
// space / hyphen / underscore / dot, 1..40 chars. Used by the UI
// to keep someone from naming a peer with control characters that
// then leak into the config file.
func ValidatePeerName(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return errors.New("name is required")
	}
	if len(s) > 40 {
		return errors.New("name too long (max 40 chars)")
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.' || r == ' ':
		default:
			return fmt.Errorf("name contains invalid character %q", r)
		}
	}
	return nil
}

// NewPeerID generates an 8-hex-char ID (4 random bytes). Plenty of
// entropy for a single-router peer set; URL-safe.
func NewPeerID() (string, error) {
	var b [4]byte
	if err := readRand(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
