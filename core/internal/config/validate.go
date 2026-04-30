package config

import (
	"fmt"
	"net"
	"regexp"
)

// hostnameRE is a permissive RFC-1123-ish hostname check. We accept letters,
// digits, hyphens, and underscores up to 63 chars. Strict labels (no leading/
// trailing hyphen) are not enforced — too noisy for first-boot defaults like
// "knot-living-room".
var hostnameRE = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,63}$`)

// countryRE accepts the ISO 3166-1 alpha-2 codes plus "00" (the wireless
// "world" regulatory domain — used as a conservative default before the
// user picks a real country).
var countryRE = regexp.MustCompile(`^([A-Z]{2}|00)$`)

// Validate returns nil if the config is internally consistent, or a
// descriptive error otherwise. Validation is structural — it does not check
// whether referenced Wi-Fi networks actually exist.
func (c Config) Validate() error {
	if !hostnameRE.MatchString(c.Device.Name) {
		return fmt.Errorf("device.name: %q is not a valid hostname", c.Device.Name)
	}
	if !countryRE.MatchString(c.Device.Country) {
		return fmt.Errorf("device.country: %q is not a valid ISO country code", c.Device.Country)
	}

	switch c.Role {
	case RoleSetup:
		// Setup mode does not require uplink/AP/LAN or auth to be set —
		// the wizard fills them in.
	case RoleWiFiExtender:
		if c.Auth.PasswordHash == "" {
			return fmt.Errorf("auth.password_hash: must be set outside of setup mode")
		}
		if err := c.Network.validateExtender(); err != nil {
			return fmt.Errorf("network: %w", err)
		}
	default:
		return fmt.Errorf("role: %q is not a known role", c.Role)
	}
	return nil
}

func (n Network) validateExtender() error {
	if n.Uplink == nil || n.Uplink.SSID == "" {
		return fmt.Errorf("uplink.ssid is required for wifi-extender role")
	}
	if n.AP == nil || n.AP.SSID == "" {
		return fmt.Errorf("ap.ssid is required for wifi-extender role")
	}
	if n.AP.Band != "2.4" && n.AP.Band != "5" {
		return fmt.Errorf("ap.band must be \"2.4\" or \"5\" (got %q)", n.AP.Band)
	}
	if n.LAN == nil {
		return fmt.Errorf("lan is required for wifi-extender role")
	}
	if _, _, err := net.ParseCIDR(n.LAN.CIDR); err != nil {
		return fmt.Errorf("lan.cidr: %w", err)
	}
	if ip := net.ParseIP(n.LAN.DHCP.PoolStart); ip == nil {
		return fmt.Errorf("lan.dhcp.pool_start: invalid IP %q", n.LAN.DHCP.PoolStart)
	}
	if ip := net.ParseIP(n.LAN.DHCP.PoolEnd); ip == nil {
		return fmt.Errorf("lan.dhcp.pool_end: invalid IP %q", n.LAN.DHCP.PoolEnd)
	}
	return nil
}
