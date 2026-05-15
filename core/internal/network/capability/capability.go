// Package capability probes the running host for the hardware
// features that gate KnotOS roles: which USB-Ethernet adapters are
// attached (so the wifi-router role can pick a WAN), what Pi model
// is underneath (so we can decide whether a concurrent guest AP on
// a second radio is feasible), and a friendly label for each
// adapter so the wizard can tell the user what it actually saw.
//
// The probe is driver-agnostic — anything that lands as a netdev
// with a USB ancestor counts. r8152, asix, ax88179_178a, cdc_ether
// all just work without an opt-in list. A small lookup table over
// USB IDs feeds the human-readable label, but unknown adapters
// still appear (with a generic "USB Ethernet" name).
//
// Everything sysfs-related goes through SysClassNet/ModelFile in
// Probe so unit tests can point at a fake tree on any OS.
package capability

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Default sysfs paths on a real Linux system. Overridable for tests.
const (
	DefaultSysClassNet = "/sys/class/net"
	DefaultModelFile   = "/proc/device-tree/model"
)

// PiModel is the Raspberry Pi family this probe identifies.
type PiModel string

const (
	PiUnknown PiModel = ""
	PiZero2W  PiModel = "zero-2w"
	Pi3B      PiModel = "3b"
	Pi4       PiModel = "4"
	Pi5       PiModel = "5"
)

// EthAdapter is one USB-attached Ethernet interface.
type EthAdapter struct {
	// Interface is the kernel name, e.g. "eth0". JSON-tagged as
	// "name" to match what the wizard UI expects.
	Interface string `json:"name"`
	// Driver is the kernel module bound to it (e.g. "r8152").
	Driver string `json:"driver,omitempty"`
	// Link is true when /sys/class/net/<iface>/carrier reads "1" —
	// i.e. an Ethernet cable is plugged in and the link is up.
	// Read at probe time, not cached; re-Probe to get a fresh value.
	Link bool `json:"link"`
	// USB reports whether this is a USB-attached adapter (vs
	// onboard PCIe / SoC Ethernet). The setup wizard cares because
	// USB hot-plug is the user-friendly path.
	USB bool `json:"usb"`
	// USBVendor is the lower-case 4-hex-digit vendor ID, e.g. "0bda".
	USBVendor string `json:"usb_vendor,omitempty"`
	// USBProduct is the lower-case 4-hex-digit product ID, e.g. "8152".
	USBProduct string `json:"usb_product,omitempty"`
	// Model is a human-readable description: a known model name
	// from the lookup table, or a generic fallback like
	// "USB Ethernet (r8152)".
	Model string `json:"model"`
}

// Report is the aggregate result of a probe run.
type Report struct {
	Pi             PiModel      `json:"pi"`
	PiModelString  string       `json:"pi_model_string,omitempty"` // raw /proc/device-tree/model content
	Eth            []EthAdapter `json:"eth"`
	GuestAPCapable bool         `json:"guest_ap_capable"`
	// RouterCapable is shorthand for "is len(Eth) > 0?". The wizard
	// gates the role-choice card on this.
	RouterCapable bool `json:"router_capable"`
}

// Probe holds the file-system roots used during a Run. Construct
// directly: zero value uses the production paths.
type Probe struct {
	SysClassNet string
	ModelFile   string
}

// Run walks the host and returns its current capabilities. Errors
// are returned only for unrecoverable conditions; missing files
// (e.g. a non-Linux dev box without /sys) yield an empty Report
// with no error so the caller can still surface the wizard's
// extender-only path.
func (p Probe) Run() (Report, error) {
	if p.SysClassNet == "" {
		p.SysClassNet = DefaultSysClassNet
	}
	if p.ModelFile == "" {
		p.ModelFile = DefaultModelFile
	}
	r := Report{}

	if model, err := readModel(p.ModelFile); err == nil {
		r.PiModelString = model
		r.Pi = classifyPi(model)
	}

	eth, err := scanUSBEth(p.SysClassNet)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return r, fmt.Errorf("capability: %w", err)
	}
	r.Eth = eth
	r.RouterCapable = len(eth) > 0

	// Concurrent AP on a separate radio needs either a second radio
	// (Pi 4/5 with a USB Wi-Fi later) or a chipset that can run
	// concurrent APs. Zero 2W's BCM43436 cannot. Conservative:
	// only Pi 4/5 light up the guest AP path in v0.3.
	r.GuestAPCapable = r.Pi == Pi4 || r.Pi == Pi5

	return r, nil
}

func readModel(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	// /proc/device-tree/model is a NUL-terminated string. Strip
	// trailing NULs and whitespace.
	s := strings.TrimRight(string(b), "\x00\r\n\t ")
	return s, nil
}

// classifyPi maps the /proc/device-tree/model string to a PiModel.
// Examples:
//   - "Raspberry Pi Zero 2 W Rev 1.0"
//   - "Raspberry Pi 3 Model B Rev 1.2"
//   - "Raspberry Pi 4 Model B Rev 1.4"
//   - "Raspberry Pi 5 Model B Rev 1.0"
func classifyPi(model string) PiModel {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "zero 2"):
		return PiZero2W
	case strings.Contains(m, "raspberry pi 5"):
		return Pi5
	case strings.Contains(m, "raspberry pi 4"):
		return Pi4
	case strings.Contains(m, "raspberry pi 3"):
		return Pi3B
	default:
		return PiUnknown
	}
}

// scanUSBEth walks /sys/class/net/* and returns adapters whose
// device path includes a USB ancestor. Output is sorted by interface
// name for stable test assertions.
func scanUSBEth(sysClassNet string) ([]EthAdapter, error) {
	entries, err := os.ReadDir(sysClassNet)
	if err != nil {
		return nil, err
	}

	var out []EthAdapter
	for _, e := range entries {
		ifname := e.Name()
		// Skip loopback and the wlan family up front.
		if ifname == "lo" || strings.HasPrefix(ifname, "wlan") {
			continue
		}
		ad, ok := readAdapter(sysClassNet, ifname)
		if !ok {
			continue
		}
		out = append(out, ad)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Interface < out[j].Interface })
	return out, nil
}

// readAdapter inspects /sys/class/net/<ifname> and returns a populated
// EthAdapter when it is a USB-attached Ethernet interface; returns
// (_, false) for everything else (PCIe NICs, virtual interfaces).
//
// USB recognition uses two independent signals so a quirk in one
// doesn't drop the adapter:
//
//   - The device symlink target contains "usb" anywhere in the path
//     (covers both "/sys/devices/.../soc/3f980000.usb/usb1/..." on
//     a Zero 2W and "/sys/devices/pci.../usb1/..." on a Pi 5).
//   - The uevent file contains a PRODUCT= line, which is the
//     canonical USB-device fingerprint (vendor/product/version).
//
// Either condition is enough; both are robust on Pi OS Lite.
func readAdapter(sysClassNet, ifname string) (EthAdapter, bool) {
	devLink := filepath.Join(sysClassNet, ifname, "device")
	resolved, err := os.Readlink(devLink)
	if err != nil {
		// No device symlink → virtual iface (bridge, tun, veth, …).
		return EthAdapter{}, false
	}

	uevent, _ := os.ReadFile(filepath.Join(devLink, "uevent"))
	driver, vendor, product := parseUevent(string(uevent))

	hasUSBPath := strings.Contains(resolved, "usb")
	hasUSBID := vendor != "" && product != ""
	if !hasUSBPath && !hasUSBID {
		return EthAdapter{}, false
	}

	// Link sense: prefer /sys/class/net/<iface>/carrier — reads "1"
	// when an Ethernet cable is plugged in AND the interface is
	// admin-up AND the PHY has finished negotiation.
	//
	// Fallback: operstate. Some USB-Ethernet drivers populate
	// operstate ("up" / "dormant" / "down") faster than they
	// publish carrier, and operstate="up" is a strong signal that
	// the cable is connected.
	//
	// Anything else → link=false. The setup HTTP handler does
	// `ip link set <iface> up` before invoking us so admin-down
	// is already neutralized.
	link := false
	if data, err := os.ReadFile(filepath.Join(sysClassNet, ifname, "carrier")); err == nil {
		if strings.TrimSpace(string(data)) == "1" {
			link = true
		}
	}
	if !link {
		if data, err := os.ReadFile(filepath.Join(sysClassNet, ifname, "operstate")); err == nil {
			s := strings.TrimSpace(string(data))
			if s == "up" {
				link = true
			}
		}
	}

	return EthAdapter{
		Interface:  ifname,
		Driver:     driver,
		Link:       link,
		USB:        hasUSBPath || hasUSBID,
		USBVendor:  vendor,
		USBProduct: product,
		Model:      describeAdapter(vendor, product, driver),
	}, true
}

// parseUevent extracts driver, USB vendor, and product from a
// /sys/class/net/<iface>/device/uevent body.
//
// Format excerpt:
//
//	DRIVER=r8152
//	PRODUCT=bda/8152/2000
//	INTERFACE=2
//
// PRODUCT is hex-without-leading-zeros; we left-pad to 4 chars so
// "bda/8152" round-trips back to the canonical "0bda:8152".
func parseUevent(body string) (driver, vendor, product string) {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "DRIVER="):
			driver = strings.TrimPrefix(line, "DRIVER=")
		case strings.HasPrefix(line, "PRODUCT="):
			parts := strings.Split(strings.TrimPrefix(line, "PRODUCT="), "/")
			if len(parts) >= 2 {
				vendor = padHex(parts[0])
				product = padHex(parts[1])
			}
		}
	}
	return driver, vendor, product
}

func padHex(s string) string {
	s = strings.ToLower(s)
	for len(s) < 4 {
		s = "0" + s
	}
	return s
}
