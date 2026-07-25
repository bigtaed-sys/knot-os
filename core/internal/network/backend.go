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
	// WAN reports the wired upstream state in wifi-router mode. Nil for
	// every other role.
	WAN *WANStatus `json:"wan,omitempty"`
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

// ModemStatus is the runtime state of a USB cellular WAN, as reported
// by ModemManager. All fields are best-effort — a missing/!present
// modem yields the zero value with Present=false.
type ModemStatus struct {
	// Present is true when ModemManager sees a modem at all.
	Present bool `json:"present"`
	// State is ModemManager's state ("connected", "registered",
	// "searching", "disabled", "locked", ...).
	State string `json:"state,omitempty"`
	// Operator is the registered carrier name.
	Operator string `json:"operator,omitempty"`
	// Tech is the access technology ("lte", "5gnr", "umts", ...).
	Tech string `json:"tech,omitempty"`
	// SignalPercent is ModemManager's 0-100 signal quality.
	SignalPercent int `json:"signal_percent"`
	// Interface is the data netdev once connected (e.g. "wwan0").
	Interface string `json:"interface,omitempty"`
	// Model / Manufacturer identify the hardware for the UI.
	Manufacturer string `json:"manufacturer,omitempty"`
	Model        string `json:"model,omitempty"`
	// LockRequired is the SIM lock state ("sim-pin", "sim-puk", or
	// "" when unlocked) — the UI prompts for a PIN when it's sim-pin.
	LockRequired string `json:"lock_required,omitempty"`
	// SIMSlots is the number of SIM slots the modem exposes. >1 means the
	// active SIM can be switched (--set-primary-sim-slot); the UI shows a
	// slot selector only then. 0/1 = single-slot modem, no switching.
	SIMSlots int `json:"sim_slots,omitempty"`
	// PrimarySlot is the currently-active SIM slot (1-based), meaningful
	// only when SIMSlots > 1.
	PrimarySlot int `json:"primary_slot,omitempty"`
	// LastError is the human-readable reason the most recent connect
	// attempt failed (mmcli stderr + the modem's state-failed-reason).
	// Empty when the modem is connected or has never failed. The apply
	// path deliberately doesn't abort on a modem failure — so the AP
	// stays up — which means this is the ONLY place the real reason
	// (bad APN, sim-pin, no registration) surfaces to the user.
	LastError string `json:"last_error,omitempty"`
}

// SMS is one stored short message (received or sent).
type SMS struct {
	// ID is the ModemManager SMS index, used to delete it.
	ID string `json:"id"`
	// Number is the peer (sender for received, recipient for sent).
	Number string `json:"number"`
	// Text is the message body.
	Text string `json:"text"`
	// Timestamp is the carrier-provided time (received messages).
	Timestamp string `json:"timestamp,omitempty"`
	// Sent is true for outgoing messages (pdu-type "submit").
	Sent bool `json:"sent"`
}

// ModemNetwork describes the modem's access-tech / band capabilities and
// current selection, for the "prefer 4G / lock band" controls.
type ModemNetwork struct {
	// SupportedModes is the set of individual techs the modem supports
	// ("2g","3g","4g","5g"), derived from ModemManager's mode combos.
	SupportedModes []string `json:"supported_modes"`
	// CurrentModes is the currently-allowed tech list. Empty/all-supported
	// means "auto".
	CurrentModes []string `json:"current_modes"`
	// SupportedBands / CurrentBands are ModemManager band identifiers
	// (e.g. "eutran-1", "eutran-3"). CurrentBands is ["any"] when unlocked.
	SupportedBands []string `json:"supported_bands"`
	CurrentBands   []string `json:"current_bands"`
}

// WANStatus is the runtime state of the Ethernet WAN interface in
// wifi-router mode.
type WANStatus struct {
	// Interface is the kernel netdev name (e.g. "eth0").
	Interface string `json:"interface"`
	// Mode is the configured WAN mode ("dhcp" today; statics later).
	Mode string `json:"mode,omitempty"`
	// Up reports whether the link is carrier-up.
	Up bool `json:"up"`
	// IP is the address the DHCP client picked up. Empty when no
	// lease is held.
	IP string `json:"ip,omitempty"`
}
