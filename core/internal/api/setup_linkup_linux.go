//go:build linux

package api

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// bringUSBEthUp brings every USB-attached Ethernet interface admin-UP
// before the capability probe runs.
//
// Why: /sys/class/net/<iface>/carrier reads "0" whenever the interface
// is admin-down — which is the default state for a freshly-enumerated
// USB-Ethernet adapter in setup mode, because we mask NetworkManager
// + dhcpcd + ifupdown. So even with a cable plugged in, the wizard's
// «cable in?» check would read 0 forever until the user explicitly
// transitions to wifi-router (which UPs the link as part of apply).
//
// We walk /sys/class/net, pick interfaces whose `device` symlink
// resolves through a `usb` path component, and `ip link set <iface>
// up` them. Best-effort: a failure on one adapter doesn't block the
// rest. Idempotent — bringing an already-UP interface UP is a no-op.
//
// We give the PHY 300 ms to negotiate before returning, so the
// subsequent carrier read sees the post-negotiation state. That's
// long enough for Gigabit USB adapters in practice; faster ones come
// back immediately and the wait is harmless.
func bringUSBEthUp() {
	const sysClassNet = "/sys/class/net"
	entries, err := os.ReadDir(sysClassNet)
	if err != nil {
		return
	}
	upped := 0
	for _, e := range entries {
		name := e.Name()
		// Skip obvious non-Ethernet candidates.
		if name == "lo" || strings.HasPrefix(name, "wlan") ||
			strings.HasPrefix(name, "ap") || strings.HasPrefix(name, "br") {
			continue
		}
		dev := filepath.Join(sysClassNet, name, "device")
		resolved, err := os.Readlink(dev)
		if err != nil {
			continue
		}
		if !strings.Contains(resolved, "usb") {
			continue
		}
		// `ip link set <iface> up` — runs in <50ms when the iface
		// is already up, ~150-300ms on fresh adapters where the
		// PHY needs to negotiate.
		_ = exec.Command("ip", "link", "set", name, "up").Run()
		upped++
	}
	if upped > 0 {
		// Give the kernel a moment to publish carrier state after the
		// link comes up. Empirically 300ms covers RTL8152 / AX88179
		// negotiation; some Gigabit adapters can take up to a second
		// on cold plug but the wizard's 3s polling will catch that.
		time.Sleep(300 * time.Millisecond)
	}
}
