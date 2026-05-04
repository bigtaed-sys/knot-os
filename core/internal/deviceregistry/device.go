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

	// LastARPSeen is the most recent time the kernel's ARP table
	// reported a complete neighbour entry for this MAC. Used to
	// drive a more honest Online() than "lease still valid": a
	// device that physically left the LAN but whose 12-hour lease
	// hasn't expired yet should NOT show as online. In-memory
	// only — persisting it across reboots would defeat the point
	// (the kernel restarts with an empty ARP table anyway).
	LastARPSeen time.Time `yaml:"-" json:"last_arp_seen,omitempty"`

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

// ARPLivenessWindow is how long a device is considered "alive" after
// the last complete ARP entry. The kernel evicts ARP entries when a
// device hasn't responded in a few minutes, but our poll cadence is
// 30s, so we add some grace for poll-cycle alignment.
const ARPLivenessWindow = 5 * time.Minute

// StaleAfter marks devices whose ARP table hasn't seen them for
// 30+ days as candidates for cleanup. Surfaced as a "stale" badge in
// the UI; doesn't block any functionality on its own.
const StaleAfter = 30 * 24 * time.Hour

// Online reports whether the device is currently usable on the LAN.
//
// Two-signal:
//
//   - DHCP lease must not have expired. Floor: a device whose lease
//     ran out is offline regardless of anything else.
//   - If we have ever observed the device in the ARP table, the most
//     recent observation must be within ARPLivenessWindow. A device
//     that physically left but whose 12h lease is still nominally
//     valid will flip to offline within ~5 minutes of its ARP entry
//     ageing out.
//
// LastARPSeen.IsZero() means we've never seen ARP for this MAC —
// either we just booted (kernel ARP table is fresh) or the device
// hasn't been talked to yet. In that case we trust the lease alone,
// to avoid biasing the UI against devices that are connected but
// silent.
func (d Device) Online(now time.Time) bool {
	if d.LeaseExpires.IsZero() || !d.LeaseExpires.After(now) {
		return false
	}
	if d.LastARPSeen.IsZero() {
		return true
	}
	return now.Sub(d.LastARPSeen) < ARPLivenessWindow
}

// Stale reports whether the device hasn't been observed in a long
// time and is a good candidate for the "Forget" UI button — the
// usual case is a phone whose private MAC rotation made the entry
// effectively dead but it still hangs around. A LastSeen of zero
// (legacy v0.1 records) is considered stale only after a much
// longer fallback so we don't pre-emptively flag every entry on
// upgrade day.
func (d Device) Stale(now time.Time) bool {
	if d.Online(now) {
		return false
	}
	if d.LastSeen.IsZero() {
		// No data → use FirstSeen as the floor. If even FirstSeen
		// is zero (truly nothing recorded), treat as stale.
		if d.FirstSeen.IsZero() {
			return true
		}
		return now.Sub(d.FirstSeen) > StaleAfter
	}
	return now.Sub(d.LastSeen) > StaleAfter
}
