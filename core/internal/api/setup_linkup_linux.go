//go:build linux

package api

import (
	"bytes"
	"log"
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
// The function is verbose by design: every step logs to the journal
// so a misbehaving USB adapter can be diagnosed without instrumenting
// the source. Logs are prefixed with "setup/linkup:" — grep journal
// for that on production to see what was tried + what came back.
func bringUSBEthUp() {
	const sysClassNet = "/sys/class/net"
	entries, err := os.ReadDir(sysClassNet)
	if err != nil {
		log.Printf("setup/linkup: cannot read %s: %v", sysClassNet, err)
		return
	}
	candidates := []string{}
	for _, e := range entries {
		name := e.Name()
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
		candidates = append(candidates, name)
	}
	if len(candidates) == 0 {
		log.Printf("setup/linkup: no USB-Ethernet candidates in /sys/class/net")
		return
	}
	log.Printf("setup/linkup: candidates=%v", candidates)

	for _, name := range candidates {
		// Use absolute path so we're not at the mercy of PATH being
		// trimmed by systemd. Both /sbin/ip and /usr/sbin/ip are
		// historically valid on Pi OS; try both.
		ran := false
		for _, bin := range []string{"/sbin/ip", "/usr/sbin/ip", "ip"} {
			var stderr bytes.Buffer
			cmd := exec.Command(bin, "link", "set", name, "up")
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				log.Printf("setup/linkup: %s link set %s up: %v (%s)",
					bin, name, err, strings.TrimSpace(stderr.String()))
				continue
			}
			log.Printf("setup/linkup: %s link set %s up: OK", bin, name)
			ran = true
			break
		}
		if !ran {
			log.Printf("setup/linkup: WARN — every `ip` candidate failed for %s; link state will likely read down", name)
		}
	}

	// PHY negotiation isn't instant — 800ms covers RTL8152 + AX88179
	// in our hardware tests. Gigabit adapters in cold-plug can take
	// up to ~1.5s; the wizard's 3s poll catches those on the next
	// hit. We don't want to block the API call longer than this.
	time.Sleep(800 * time.Millisecond)

	// Diagnostic dump — what does the kernel report for each
	// candidate after the up + sleep? If carrier is 0 here, the
	// problem is upstream (cable / adapter / kernel driver), not us.
	for _, name := range candidates {
		carrier := readSysfsTrim(filepath.Join(sysClassNet, name, "carrier"))
		operstate := readSysfsTrim(filepath.Join(sysClassNet, name, "operstate"))
		flags := readSysfsTrim(filepath.Join(sysClassNet, name, "flags"))
		speed := readSysfsTrim(filepath.Join(sysClassNet, name, "speed"))
		log.Printf("setup/linkup: %s: carrier=%q operstate=%q flags=%s speed=%q",
			name, carrier, operstate, flags, speed)
	}
}

// readSysfsTrim returns the trimmed contents of a sysfs file, or
// the literal string "<err:...>" if the read failed. Useful in
// diagnostic logging where we want one log line per file regardless
// of failure mode.
func readSysfsTrim(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "<err:" + err.Error() + ">"
	}
	return strings.TrimSpace(string(data))
}
