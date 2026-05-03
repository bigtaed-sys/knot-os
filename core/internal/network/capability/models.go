package capability

// knownAdapter is one entry in the USB-ID lookup table.
type knownAdapter struct {
	Vendor, Product string
	Label           string
}

// knownUSBEth is a small, hand-curated table of "this is exactly
// what model" mappings. NOT a gating list — anything that lands as
// a netdev still works. This table just feeds friendlier text to
// the wizard ("Detected: TP-Link UE300 (Gigabit)") instead of the
// generic "USB Ethernet (r8153)".
//
// Add to this table when a user reports an adapter we don't have
// nice copy for yet. Keep entries short — this is a label, not a
// driver matrix.
var knownUSBEth = []knownAdapter{
	// Realtek RTL8152 — the very common $5–10 USB 2.0 Fast
	// Ethernet stick. Found in countless no-name dongles.
	{Vendor: "0bda", Product: "8152", Label: "RTL8152 (USB 2.0 Fast Ethernet)"},
	// Realtek RTL8153 — TP-Link UE300, generic USB 3.0 Gigabit.
	{Vendor: "0bda", Product: "8153", Label: "RTL8153 (USB 3.0 Gigabit)"},
	// TP-Link UE300 (alt PID).
	{Vendor: "2357", Product: "0601", Label: "TP-Link UE300 (Gigabit)"},
	// ASIX AX88179 (USB 3.0 Gigabit, found in Plugable USB-Eth).
	{Vendor: "0b95", Product: "1790", Label: "ASIX AX88179 (Gigabit)"},
	// ASIX AX88772 family (older Fast Ethernet).
	{Vendor: "0b95", Product: "7720", Label: "ASIX AX88772 (USB 2.0 Fast Ethernet)"},
}

// describeAdapter returns the display label for a USB-Ethernet
// adapter, preferring the known-models table; falling back to
// "USB Ethernet (<driver>)" when the driver is known, or the
// terse "USB Ethernet" when nothing is.
func describeAdapter(vendor, product, driver string) string {
	for _, k := range knownUSBEth {
		if k.Vendor == vendor && k.Product == product {
			return k.Label
		}
	}
	if driver != "" {
		return "USB Ethernet (" + driver + ")"
	}
	return "USB Ethernet"
}
