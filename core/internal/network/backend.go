// Package network defines the abstraction between knotd's config-application
// engine and the Linux networking stack.
//
// In M2 this package contains only the Backend interface and a MockBackend
// suitable for dev-mode runs and unit tests. The LinuxBackend (hostapd /
// wpa_supplicant / dnsmasq / nftables / netlink) lands in M5.
package network

import (
	"context"

	"github.com/knot-os/knot-os/core/internal/config"
)

// Backend is the system-side surface that applies a Config to the host's
// networking stack. Implementations must be safe to call from a single
// goroutine; the config-apply loop in knotd serializes calls.
type Backend interface {
	// Apply transitions the system to match cfg. Apply is expected to be
	// idempotent: calling it twice with the same config is a no-op the
	// second time. The implementation may take seconds (Wi-Fi associates,
	// hostapd restarts) and must respect ctx cancellation.
	Apply(ctx context.Context, cfg config.Config) error

	// Status returns a runtime view of the current network state. It does
	// not consult the on-disk config; it reads the live state of the
	// system (or the simulated state, for the mock).
	Status(ctx context.Context) (Status, error)

	// Scan returns nearby Wi-Fi networks visible to wlan0. Used by the
	// first-run wizard to populate the uplink selector.
	//
	// On Linux this requires temporarily disabling ap0 because the
	// BCM43436 cannot scan and broadcast on different channels. The
	// implementation is expected to handle that transparently.
	Scan(ctx context.Context) ([]ScannedNetwork, error)

	// Name identifies the implementation for logging and the /api/status
	// endpoint ("mock", "linux", ...).
	Name() string
}

// ScannedNetwork is a single Wi-Fi network discovered by Scan.
type ScannedNetwork struct {
	SSID string `json:"ssid"`
	// BSSID is the AP's MAC address — used to disambiguate networks
	// with the same SSID (e.g. mesh systems).
	BSSID string `json:"bssid,omitempty"`
	// Channel is the 2.4/5 GHz channel number.
	Channel int `json:"channel"`
	// Band is "2.4" or "5".
	Band string `json:"band"`
	// RSSIdBm is the signal strength (negative; closer to 0 = stronger).
	RSSIdBm int `json:"rssi_dbm"`
	// Secured reports whether the network requires a passphrase.
	Secured bool `json:"secured"`
}

// Status is the runtime view of the network. The fields are deliberately
// flat for easy JSON encoding to /api/status.
type Status struct {
	// Backend identifies the active backend ("mock", "linux").
	Backend string `json:"backend"`
	// Role is the role the backend believes it is currently in.
	Role config.Role `json:"role"`
	// Uplink reports the Wi-Fi STA state. Nil if no uplink is configured.
	Uplink *UplinkStatus `json:"uplink,omitempty"`
	// AP reports the broadcasted Wi-Fi state. Nil if no AP is configured.
	AP *APStatus `json:"ap,omitempty"`
}

// UplinkStatus is the runtime state of the wlan0 STA connection.
type UplinkStatus struct {
	SSID      string `json:"ssid"`
	Connected bool   `json:"connected"`
	// RSSIdBm is the received signal strength in dBm (negative numbers,
	// closer to zero = stronger). Zero means "unknown".
	RSSIdBm int `json:"rssi_dbm,omitempty"`
}

// APStatus is the runtime state of the broadcasted Wi-Fi.
type APStatus struct {
	SSID    string `json:"ssid"`
	Up      bool   `json:"up"`
	Clients int    `json:"clients"`
}
