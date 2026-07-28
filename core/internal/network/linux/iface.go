//go:build linux

package linux

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// findPhyForInterface returns the phy name (e.g. "phy0") backing the
// given netdev (e.g. "wlan0"). On Pi Zero 2W there is exactly one phy.
func findPhyForInterface(iface string) (string, error) {
	// /sys/class/net/<iface>/phy80211/name contains the phy name.
	path := fmt.Sprintf("/sys/class/net/%s/phy80211/name", iface)
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read phy for %s: %w", iface, err)
	}
	return strings.TrimSpace(string(b)), nil
}

// readMAC returns the MAC address of an interface as colon-separated hex.
func readMAC(iface string) (string, error) {
	b, err := os.ReadFile(fmt.Sprintf("/sys/class/net/%s/address", iface))
	if err != nil {
		return "", fmt.Errorf("read MAC for %s: %w", iface, err)
	}
	return strings.TrimSpace(string(b)), nil
}

// interfaceExists returns whether the given interface is present.
func interfaceExists(iface string) bool {
	_, err := os.Stat(fmt.Sprintf("/sys/class/net/%s", iface))
	return err == nil
}

// ensureAP creates the ap0 virtual interface on the phy backing wlan0
// if it does not already exist. The MAC is set to wlan0's MAC with the
// locally-administered bit flipped so the AP and STA appear as
// distinct devices on the network.
//
// Calling ensureAP when ap0 already exists is a no-op — it is safe to
// run on every Apply.
func (b *LinuxBackend) ensureAP(ctx context.Context) error {
	if interfaceExists(IfaceAP) {
		return nil
	}
	phy, err := findPhyForInterface(IfaceWlan)
	if err != nil {
		return fmt.Errorf("ensureAP: %w", err)
	}

	if err := b.r.runOK(ctx, "iw", "phy", phy, "interface", "add", IfaceAP, "type", "__ap"); err != nil {
		return fmt.Errorf("ensureAP: iw add: %w", err)
	}

	if mac, err := readMAC(IfaceWlan); err == nil {
		apMAC, ok := flipLocalBit(mac)
		if ok {
			if err := b.r.runOK(ctx, "ip", "link", "set", IfaceAP, "address", apMAC); err != nil {
				// Non-fatal: ap0 still works, just shares MAC with wlan0.
				b.logger.Printf("warn: setting %s MAC failed: %v", IfaceAP, err)
			}
		}
	}
	return nil
}

// removeAP destroys the ap0 virtual interface. Best-effort — used during
// shutdown or role transition.
func (b *LinuxBackend) removeAP(ctx context.Context) {
	if !interfaceExists(IfaceAP) {
		return
	}
	b.r.runIgnoreError(ctx, "iw", "dev", IfaceAP, "del")
}

// ensureAPGuest creates the ap_guest virtual interface for the
// guest BSS. Lives on the same phy as the main AP (single-radio
// constraint on Zero 2W; cohabitation is fine on Pi 4/5 too).
//
// MAC is derived from wlan0's MAC by flipping the LAA bit AND
// bumping the second-to-last octet, so ap0 and ap_guest end up
// with distinct unicast addresses. brcmfmac rejects duplicate
// MACs across interfaces it manages.
func (b *LinuxBackend) ensureAPGuest(ctx context.Context) error {
	if interfaceExists(IfaceAPGuest) {
		return nil
	}
	phy, err := findPhyForInterface(IfaceWlan)
	if err != nil {
		return fmt.Errorf("ensureAPGuest: %w", err)
	}
	if err := b.r.runOK(ctx, "iw", "phy", phy, "interface", "add", IfaceAPGuest, "type", "__ap"); err != nil {
		return fmt.Errorf("ensureAPGuest: iw add: %w", err)
	}
	if mac, err := readMAC(IfaceWlan); err == nil {
		if guestMAC, ok := guestMACFromBase(mac); ok {
			if err := b.r.runOK(ctx, "ip", "link", "set", IfaceAPGuest, "address", guestMAC); err != nil {
				b.logger.Printf("warn: setting %s MAC failed: %v", IfaceAPGuest, err)
			}
		}
	}
	return nil
}

// removeAPGuest tears the guest BSS interface down. Best-effort.
func (b *LinuxBackend) removeAPGuest(ctx context.Context) {
	if !interfaceExists(IfaceAPGuest) {
		return
	}
	b.r.runIgnoreError(ctx, "iw", "dev", IfaceAPGuest, "del")
}

// guestMACFromBase derives a MAC for ap_guest from wlan0's MAC.
// First octet has the LAA bit flipped (same as ap0) AND the
// second-to-last octet incremented by 1 — yields a distinct value
// from the ap0 MAC (which only flips LAA). Returns ok=false if the
// input doesn't parse.
func guestMACFromBase(mac string) (string, bool) {
	parts := strings.Split(mac, ":")
	if len(parts) != 6 {
		return "", false
	}
	var first, fifth byte
	if _, err := fmt.Sscanf(parts[0], "%x", &first); err != nil {
		return "", false
	}
	if _, err := fmt.Sscanf(parts[4], "%x", &fifth); err != nil {
		return "", false
	}
	first ^= 0x02
	fifth ^= 0x01
	parts[0] = fmt.Sprintf("%02x", first)
	parts[4] = fmt.Sprintf("%02x", fifth)
	return strings.Join(parts, ":"), true
}

// linkUp brings an interface up.
func (b *LinuxBackend) linkUp(ctx context.Context, iface string) error {
	return b.r.runOK(ctx, "ip", "link", "set", iface, "up")
}

// ensureBridge creates the LAN bridge if it isn't present and brings
// it up. Idempotent — safe on every Apply. hostapd is what enslaves
// the Wi-Fi AP into the bridge (via its `bridge=` directive); the
// wired ports are enslaved by enslaveToBridge below.
func (b *LinuxBackend) ensureBridge(ctx context.Context, br string) error {
	if !interfaceExists(br) {
		if err := b.r.runOK(ctx, "ip", "link", "add", "name", br, "type", "bridge"); err != nil {
			return fmt.Errorf("ensureBridge: create %s: %w", br, err)
		}
	}
	return b.linkUp(ctx, br)
}

// removeBridge tears the LAN bridge down. Best-effort — used when a
// re-apply drops back to a Wi-Fi-only LAN (no wired ports). Any ports
// still enslaved are released implicitly when the bridge is deleted.
func (b *LinuxBackend) removeBridge(ctx context.Context, br string) {
	if !interfaceExists(br) {
		return
	}
	b.linkDown(ctx, br)
	b.r.runIgnoreError(ctx, "ip", "link", "del", br)
}

// enslaveToBridge attaches a wired port to the bridge and brings it
// up. The port must carry no IP of its own (bridging is pure L2), so
// we flush it first — a stray DHCP lease from a previous role would
// otherwise shadow the bridge's gateway address.
func (b *LinuxBackend) enslaveToBridge(ctx context.Context, br, port string) error {
	if !interfaceExists(port) {
		return fmt.Errorf("enslaveToBridge: port %q not present", port)
	}
	b.addrFlush(ctx, port)
	if err := b.r.runOK(ctx, "ip", "link", "set", port, "master", br); err != nil {
		return fmt.Errorf("enslaveToBridge: %s master %s: %w", port, br, err)
	}
	return b.linkUp(ctx, port)
}

// releaseFromBridge detaches a port previously enslaved to a bridge.
// Best-effort; used when the user removes a port from the LAN set.
func (b *LinuxBackend) releaseFromBridge(ctx context.Context, port string) {
	if !interfaceExists(port) {
		return
	}
	b.r.runIgnoreError(ctx, "ip", "link", "set", port, "nomaster")
}

// linkDown takes an interface down (best-effort).
func (b *LinuxBackend) linkDown(ctx context.Context, iface string) {
	b.r.runIgnoreError(ctx, "ip", "link", "set", iface, "down")
}

// addrFlush removes all IPv4 addresses from an interface.
func (b *LinuxBackend) addrFlush(ctx context.Context, iface string) {
	b.r.runIgnoreError(ctx, "ip", "addr", "flush", "dev", iface)
}

// addrAdd assigns an IPv4 address (in CIDR form) to an interface.
func (b *LinuxBackend) addrAdd(ctx context.Context, iface, cidr string) error {
	return b.r.runOK(ctx, "ip", "addr", "add", cidr, "dev", iface)
}

// effectiveCountry returns the ISO country code to actually use for
// the kernel regulator and hostapd's country_code. Substitutes a
// permissive default ("DE") when no real country has been picked
// (setup mode default of "00" / empty), because country=00 caps Wi-Fi
// tx power so low that hostapd refuses to bring up an AP.
//
// Callers should use this value EVERYWHERE - the kernel regulator,
// the hostapd config, and the wpa_supplicant config - to avoid the
// mismatch where one component thinks "DE" and another thinks "00".
func effectiveCountry(c string) string {
	if c == "" || c == "00" {
		return "DE"
	}
	return c
}

// unblockAndRegulate frees the Wi-Fi radio from rfkill soft-block and
// sets the kernel's regulatory domain.
//
// Both are needed on a stock Pi OS Lite install:
//
//   - rfkill is left blocked at boot until something (raspi-config, a
//     wpa_supplicant connect, NetworkManager) accepts a regdomain.
//     We mask all of those, so it stays blocked unless we unblock.
//   - The kernel regulator defaults to "00" (world domain), which
//     caps tx power so low that hostapd won't bring up an AP.
//
// All errors are ignored — these calls are best-effort and the
// downstream hostapd start will produce a clearer failure if the
// radio is genuinely unusable.
func (b *LinuxBackend) unblockAndRegulate(ctx context.Context, country string) {
	b.r.runIgnoreError(ctx, "rfkill", "unblock", "wifi")
	b.r.runIgnoreError(ctx, "iw", "reg", "set", effectiveCountry(country))
}

// readOperstate returns the carrier state of an interface from
// /sys/class/net/<iface>/operstate. Common values: "up", "down",
// "dormant", "unknown", "notpresent".
func readOperstate(iface string) (string, error) {
	b, err := os.ReadFile(fmt.Sprintf("/sys/class/net/%s/operstate", iface))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// readPrimaryIPv4 returns the first IPv4 address bound to iface, or
// an empty string if none. Uses `ip -4 addr show dev <iface>` and
// scans for the first inet line; doing this without parsing JSON
// keeps us out of any "ip" version differences.
func readPrimaryIPv4(iface string) (string, error) {
	// We deliberately spawn ip rather than poking netlink directly:
	// keeps the dependency surface tiny and matches how the rest of
	// the backend interacts with the network stack.
	out, err := exec_run("ip", "-4", "-o", "addr", "show", "dev", iface)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		// Format: "<idx>: <iface> inet <addr>/<prefix> ..."
		for i, f := range fields {
			if f == "inet" && i+1 < len(fields) {
				addr := fields[i+1]
				if slash := strings.IndexByte(addr, '/'); slash > 0 {
					addr = addr[:slash]
				}
				return addr, nil
			}
		}
	}
	return "", nil
}

// exec_run runs a short command and returns combined output. Kept
// here as a small private helper so iface.go has no dependency on
// the runner machinery in exec.go (which is geared toward long-
// running supervised processes).
func exec_run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// flipLocalBit toggles the locally-administered bit (0x02) of the
// first octet of a MAC address. Returns the new MAC and ok=true on
// success; ok=false if the input is not a parseable MAC.
//
// Wi-Fi drivers expect the secondary virtual interface to use a
// locally-administered MAC; reusing wlan0's "real" MAC works on
// brcmfmac but is fragile and can confuse some clients.
func flipLocalBit(mac string) (string, bool) {
	parts := strings.Split(mac, ":")
	if len(parts) != 6 {
		return "", false
	}
	var first byte
	if _, err := fmt.Sscanf(parts[0], "%x", &first); err != nil {
		return "", false
	}
	first ^= 0x02
	parts[0] = fmt.Sprintf("%02x", first)
	return strings.Join(parts, ":"), true
}
