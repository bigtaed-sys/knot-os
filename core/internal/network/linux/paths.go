// Package linux contains the Linux-only KnotOS network backend.
//
// Files in this package use the //go:build linux constraint so they
// compile out on Windows/macOS dev hosts. A small stub in linux_other.go
// keeps the package importable everywhere; it returns a clear error
// when called on a non-Linux host.
package linux

// Filesystem layout the backend uses on the running device.
//
// Generated runtime configs live under /run/knot (tmpfs) so they are
// regenerated cleanly on every boot. State that needs to survive a
// reboot lives under /var/lib/knot.
const (
	// RuntimeDir is the tmpfs directory for generated configs.
	RuntimeDir = "/run/knot"

	// HostapdConfPath is where the generated hostapd config goes.
	HostapdConfPath = RuntimeDir + "/hostapd.conf"

	// WpaSupplicantConfPath is where the generated wpa_supplicant
	// config goes. Used only when the role has an uplink.
	WpaSupplicantConfPath = RuntimeDir + "/wpa_supplicant-wlan0.conf"

	// DnsmasqConfPath is where the generated dnsmasq config goes.
	DnsmasqConfPath = RuntimeDir + "/dnsmasq.conf"

	// NftablesRulesetPath is where the generated nftables ruleset goes.
	NftablesRulesetPath = RuntimeDir + "/knot.nft"
)

// Interface names. wlan0 is provided by the OS; ap0 is created by knotd
// on phy0 at startup.
const (
	IfaceWlan = "wlan0"
	IfaceAP   = "ap0"
)
