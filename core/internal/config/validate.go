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
	case RoleWiFiRouter:
		if c.Auth.PasswordHash == "" {
			return fmt.Errorf("auth.password_hash: must be set outside of setup mode")
		}
		if err := c.Network.validateRouter(); err != nil {
			return fmt.Errorf("network: %w", err)
		}
	default:
		return fmt.Errorf("role: %q is not a known role", c.Role)
	}
	if err := c.Network.validatePortForwards(); err != nil {
		return fmt.Errorf("network: %w", err)
	}
	return nil
}

// pfIDRE constrains port-forward IDs to URL-path-safe characters.
var pfIDRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// validatePortForwards checks the inbound DNAT rules for structural
// sanity and detects duplicate (proto, wan_port) pairs that would
// produce ambiguous nftables rules.
func (n Network) validatePortForwards() error {
	seen := map[string]bool{}
	for i, pf := range n.PortForwards {
		if !pfIDRE.MatchString(pf.ID) {
			return fmt.Errorf("port_forwards[%d].id %q is not valid", i, pf.ID)
		}
		switch pf.Proto {
		case "tcp", "udp", "tcp/udp":
		default:
			return fmt.Errorf("port_forwards[%d].proto must be tcp, udp, or tcp/udp (got %q)", i, pf.Proto)
		}
		if pf.WANPort < 1 || pf.WANPort > 65535 {
			return fmt.Errorf("port_forwards[%d].wan_port %d out of range 1..65535", i, pf.WANPort)
		}
		if pf.DestPort < 0 || pf.DestPort > 65535 {
			return fmt.Errorf("port_forwards[%d].dest_port %d out of range 0..65535", i, pf.DestPort)
		}
		if ip := net.ParseIP(pf.DestIP); ip == nil || ip.To4() == nil {
			return fmt.Errorf("port_forwards[%d].dest_ip %q is not a valid IPv4 address", i, pf.DestIP)
		}
		// A (proto, wan_port) pair can only DNAT to one place.
		for _, proto := range protoParts(pf.Proto) {
			key := proto + ":" + fmt.Sprint(pf.WANPort)
			if seen[key] {
				return fmt.Errorf("port_forwards[%d]: duplicate %s port %d", i, proto, pf.WANPort)
			}
			seen[key] = true
		}
	}
	return nil
}

// protoParts expands "tcp/udp" into both, or returns the single proto.
func protoParts(proto string) []string {
	if proto == "tcp/udp" {
		return []string{"tcp", "udp"}
	}
	return []string{proto}
}

// validateRouter shares the AP/LAN constraints with the extender
// path but additionally requires WAN and the AP.Channel that the
// user picked (since router AP runs alone on the radio with no
// upstream STA to align with). Channel 0 ("auto") is allowed and
// hostapd will pick.
func (n Network) validateRouter() error {
	if n.WAN == nil {
		return fmt.Errorf("wan is required for wifi-router role")
	}
	switch n.WAN.Mode {
	case "", "dhcp":
		// Ethernet WAN: a concrete interface is required.
		if n.WAN.Interface == "" {
			return fmt.Errorf("wan.interface is required for wifi-router role")
		}
	case "modem":
		// Cellular WAN: the data interface is discovered at apply time,
		// so wan.interface may be empty. Nothing else is mandatory —
		// APN/PIN are carrier-specific and often auto-provisioned.
		if n.WAN.Modem != nil {
			if n.WAN.Modem.SIMSlot < 0 || n.WAN.Modem.SIMSlot > 8 {
				return fmt.Errorf("wan.modem.sim_slot: %d outside 0..8", n.WAN.Modem.SIMSlot)
			}
			if n.WAN.Modem.DataLimitMB < 0 {
				return fmt.Errorf("wan.modem.data_limit_mb: must be >= 0")
			}
			if n.WAN.Modem.CycleResetDay < 0 || n.WAN.Modem.CycleResetDay > 28 {
				return fmt.Errorf("wan.modem.cycle_reset_day: %d outside 0..28", n.WAN.Modem.CycleResetDay)
			}
		}
	default:
		return fmt.Errorf("wan.mode: must be \"dhcp\" or \"modem\" (got %q)", n.WAN.Mode)
	}
	if n.AP == nil || n.AP.SSID == "" {
		return fmt.Errorf("ap.ssid is required for wifi-router role")
	}
	if n.AP.Band != "2.4" && n.AP.Band != "5" {
		return fmt.Errorf("ap.band must be \"2.4\" or \"5\" (got %q)", n.AP.Band)
	}
	if n.AP.Channel < 0 || n.AP.Channel > 165 {
		return fmt.Errorf("ap.channel: %d outside 0..165", n.AP.Channel)
	}
	if n.LAN == nil {
		return fmt.Errorf("lan is required for wifi-router role")
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
