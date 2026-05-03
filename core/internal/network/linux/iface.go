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

// linkUp brings an interface up.
func (b *LinuxBackend) linkUp(ctx context.Context, iface string) error {
	return b.r.runOK(ctx, "ip", "link", "set", iface, "up")
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
