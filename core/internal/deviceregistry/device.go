// Package deviceregistry tracks the set of devices that have been
// seen on the LAN, by parsing dnsmasq's lease file and persisting
// user-supplied overrides (display name, assigned profile) into a
// YAML file at /etc/knot/devices.yaml.
//
// A Registry is the single source of truth for "what devices does
// this knotd know about". v0.2's per-device profiles, schedules, and
// per-device DNS filtering all read from here.
package deviceregistry

import (
	"strings"
	"time"
)

// Device is a single LAN host known to KnotOS.
//
// Identity: MAC address. Devices keep their identity across IP
// changes / hostname changes; the user's name override is per-MAC.
type Device struct {
	// MAC is the L2 address in lower-case colon-hex form
	// ("dc:a6:32:11:22:33"). Stable identity.
	MAC string `yaml:"mac" json:"mac"`

	// Hostname is what the device advertised over DHCP. Often empty
	// or "*" for clients that don't supply one (some IoT, randomized
	// privacy MACs that change every connection, etc.).
	Hostname string `yaml:"hostname,omitempty" json:"hostname,omitempty"`

	// DisplayName is the user's preferred label for this device,
	// shown in the UI. Only set when the user picks one — fallback
	// to Hostname or a derived placeholder otherwise.
	DisplayName string `yaml:"display_name,omitempty" json:"display_name,omitempty"`

	// IP is the most recent address dnsmasq leased to this device.
	// Live state, not persisted.
	IP string `yaml:"-" json:"ip,omitempty"`

	// LeaseExpires is the absolute time when the current DHCP lease
	// expires (zero if no current lease). Live state.
	LeaseExpires time.Time `yaml:"-" json:"lease_expires,omitempty"`

	// FirstSeen is the time we first observed this MAC. Persisted so
	// it survives restarts.
	FirstSeen time.Time `yaml:"first_seen" json:"first_seen"`

	// LastSeen is the most recent time we saw this device active
	// (lease event, ARP entry, etc.). Persisted on a debounce.
	LastSeen time.Time `yaml:"last_seen" json:"last_seen"`

	// ProfileID assigns a profile to this device. Empty means
	// "default profile". M10 wires this up to schedules and DNS.
	ProfileID string `yaml:"profile_id,omitempty" json:"profile_id,omitempty"`
}

// Label returns the best human label for the device, in priority:
//
//	user-set DisplayName > DHCP Hostname > "Device <last 4 hex of MAC>"
func (d Device) Label() string {
	if d.DisplayName != "" {
		return d.DisplayName
	}
	if d.Hostname != "" && d.Hostname != "*" {
		return d.Hostname
	}
	clean := strings.ReplaceAll(d.MAC, ":", "")
	if len(clean) >= 4 {
		return "Device " + strings.ToUpper(clean[len(clean)-4:])
	}
	return "Unknown device"
}

// Online reports whether the device's DHCP lease is currently valid.
// Note: this is a coarse signal — a device may have a current lease
// while being physically off the network (lease hasn't expired yet).
// More precise presence detection (iw station dump) is a v0.3 task.
func (d Device) Online(now time.Time) bool {
	return !d.LeaseExpires.IsZero() && d.LeaseExpires.After(now)
}
