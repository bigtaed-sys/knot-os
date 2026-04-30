// Package config defines the KnotOS configuration schema and provides
// loading, saving, and validation. The schema is intentionally minimal in
// M2 — it grows in M3 (snapshots), M4 (auth), and M5 (network roles).
package config

// Role identifies what KnotOS is currently doing on the network. The set of
// available roles depends on detected hardware capabilities (see internal/role).
type Role string

const (
	// RoleSetup is a transient role used for the first-run wizard: an open
	// Wi-Fi AP without uplink, only for onboarding.
	RoleSetup Role = "setup"

	// RoleWiFiExtender connects wlan0 to an upstream Wi-Fi network and
	// re-broadcasts a separate SSID via ap0, NATting between them. This is
	// the primary v0.1 role on a Pi Zero 2W with no Ethernet dongle.
	RoleWiFiExtender Role = "wifi-extender"
)

// Config is the root configuration document, persisted as YAML at
// /etc/knot/config.yaml.
type Config struct {
	Device  Device                  `yaml:"device"            json:"device"`
	Role    Role                    `yaml:"role"              json:"role"`
	Auth    Auth                    `yaml:"auth"              json:"auth"`
	Network Network                 `yaml:"network"           json:"network"`
	Plugins map[string]PluginConfig `yaml:"plugins,omitempty" json:"plugins,omitempty"`
}

// Auth holds credentials for the local admin account.
//
// PasswordHash is the bcrypt hash of the admin password. It is stored
// in YAML (never the plaintext) and exposed over the API only as a
// boolean "configured" flag — never the hash itself.
type Auth struct {
	PasswordHash string `yaml:"password_hash,omitempty" json:"-"`
}

// Device holds top-level identity and locale settings.
type Device struct {
	// Name is the human-readable hostname (also used for mDNS).
	Name string `yaml:"name" json:"name"`
	// Country is the ISO 3166-1 alpha-2 regulatory domain (e.g. "RU", "US").
	// Required for Wi-Fi to come up at full power on legal channels.
	Country string `yaml:"country" json:"country"`
}

// Network groups all networking-related settings.
type Network struct {
	// Uplink describes the Wi-Fi STA connection (wlan0). Empty when the
	// role is "setup".
	Uplink *WiFiUplink `yaml:"uplink,omitempty" json:"uplink,omitempty"`
	// AP describes the broadcasted Wi-Fi network (ap0). Empty when the
	// role does not broadcast a network.
	AP *WiFiAP `yaml:"ap,omitempty" json:"ap,omitempty"`
	// LAN describes the IPv4 subnet served to AP clients.
	LAN *LAN `yaml:"lan,omitempty" json:"lan,omitempty"`
}

// WiFiUplink is the configuration of the upstream Wi-Fi connection.
type WiFiUplink struct {
	SSID string `yaml:"ssid" json:"ssid"`
	// PSKEncrypted is the WPA2 passphrase, encrypted at rest with the
	// device key. Plaintext is never persisted. Empty for open networks.
	PSKEncrypted string `yaml:"psk_encrypted,omitempty" json:"psk_encrypted,omitempty"`
}

// WiFiAP is the configuration of the broadcasted Wi-Fi network.
//
// The channel is intentionally not configurable on Zero 2W: when AP and STA
// share a single radio (BCM43436), the kernel forces them onto the same
// channel as the uplink. The UI surfaces this constraint to the user.
type WiFiAP struct {
	SSID         string `yaml:"ssid"                    json:"ssid"`
	PSKEncrypted string `yaml:"psk_encrypted,omitempty" json:"psk_encrypted,omitempty"`
	// Band is "2.4" or "5". On Zero 2W only 2.4 is supported reliably for ap0.
	Band string `yaml:"band" json:"band"`
}

// LAN is the IPv4 subnet config for the AP side.
type LAN struct {
	// CIDR is the network in CIDR form (e.g. "192.168.42.0/24").
	CIDR string `yaml:"cidr" json:"cidr"`
	DHCP DHCP   `yaml:"dhcp" json:"dhcp"`
}

// DHCP holds the address pool for the LAN's DHCP server.
type DHCP struct {
	PoolStart string `yaml:"pool_start" json:"pool_start"`
	PoolEnd   string `yaml:"pool_end"   json:"pool_end"`
}

// PluginConfig is per-plugin runtime state stored in the central config.
// Plugin-specific fields live in the plugin's own datastore — only the
// enable flag is part of the global config.
type PluginConfig struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
}

// Default returns the configuration knotd uses on first boot, before the
// setup wizard has run. The role is "setup", which surfaces an open AP and
// the captive-portal wizard.
func Default() Config {
	return Config{
		Device: Device{
			Name:    "knot",
			Country: "00", // 00 = global, conservative regdomain
		},
		Role: RoleSetup,
		Network: Network{
			LAN: &LAN{
				CIDR: "192.168.42.0/24",
				DHCP: DHCP{
					PoolStart: "192.168.42.100",
					PoolEnd:   "192.168.42.200",
				},
			},
		},
	}
}
