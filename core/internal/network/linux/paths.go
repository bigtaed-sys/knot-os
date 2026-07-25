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
// on phy0 at startup. ap_guest is the optional secondary BSS that
// comes up only when a guest session is active.
const (
	IfaceWlan    = "wlan0"
	IfaceAP      = "ap0"
	IfaceAPGuest = "ap_guest"
)

// GuestLANCIDR is the dedicated /24 the guest BSS lives on. Distinct
// from the main LAN so nftables can isolate it cleanly via source-IP
// rules without parsing the BSS interface name on every packet.
const GuestLANCIDR = "192.168.43.0/24"

// GuestLANGateway is the .1 of GuestLANCIDR, served by knotd's
// dnsmasq instance to guest clients via DHCP option 3.
const GuestLANGateway = "192.168.43.1"

// GuestLANPoolStart / GuestLANPoolEnd bracket the guest DHCP pool.
const (
	GuestLANPoolStart = "192.168.43.100"
	GuestLANPoolEnd   = "192.168.43.200"
)

// GuestSessionProvider is the read-only side of guest.Registry that
// the Linux backend cares about. Living in paths.go (no build tag)
// means main.go on any OS can declare an adapter that satisfies it,
// even though the Apply path that actually uses it only compiles on
// Linux.
type GuestSessionProvider interface {
	CurrentGuestSession() ActiveGuestSession
}

// ActiveGuestSession is the per-Apply snapshot the backend reads
// out of guest.Registry through the provider. Empty SSID means
// "no active session — tear down any existing guest BSS".
type ActiveGuestSession struct {
	SSID string
	PSK  string
}
