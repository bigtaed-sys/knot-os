//go:build linux

package linux

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/knot-os/knot-os/core/internal/network"
)

// Scan returns nearby Wi-Fi networks visible to wlan0.
//
// On the BCM43436 the radio cannot scan and broadcast on different
// channels at the same time. The wizard runs scans during setup mode
// when ap0 has not been brought up yet, so this is fine in practice;
// once we are in extender mode, calling Scan briefly disrupts ap0
// because `iw dev wlan0 scan` forces a passive scan across channels.
//
// We require wlan0 to be up. wpa_supplicant being attached to it is
// fine and even necessary in extender mode to keep the link alive.
func (b *LinuxBackend) Scan(ctx context.Context) ([]network.ScannedNetwork, error) {
	if err := b.linkUp(ctx, IfaceWlan); err != nil {
		return nil, fmt.Errorf("scan: bring %s up: %w", IfaceWlan, err)
	}
	out, err := b.r.run(ctx, "iw", "dev", IfaceWlan, "scan")
	if err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}
	return parseIWScan(out), nil
}

// parseIWScan extracts the SSID, BSSID, channel/band, RSSI, and
// security flags from `iw dev wlan0 scan` output. The format is
// fragile but stable across recent iw releases: each BSS starts with
// a "BSS aa:bb:..." line, then indented key:value lines until the
// next BSS or EOF.
func parseIWScan(out string) []network.ScannedNetwork {
	var (
		results []network.ScannedNetwork
		cur     network.ScannedNetwork
		started bool
	)

	flush := func() {
		if started && cur.SSID != "" {
			results = append(results, cur)
		}
		cur = network.ScannedNetwork{}
		started = false
	}

	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "BSS ") {
			flush()
			started = true
			cur.BSSID = parseBSSID(line)
			continue
		}
		if !started {
			continue
		}
		trim := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trim, "SSID:"):
			cur.SSID = strings.TrimSpace(strings.TrimPrefix(trim, "SSID:"))
		case strings.HasPrefix(trim, "signal:"):
			// "signal: -45.00 dBm"
			fields := strings.Fields(trim)
			if len(fields) >= 2 {
				if v, err := strconv.ParseFloat(fields[1], 64); err == nil {
					cur.RSSIdBm = int(v)
				}
			}
		case strings.HasPrefix(trim, "freq:"):
			// "freq: 2437"
			fields := strings.Fields(trim)
			if len(fields) >= 2 {
				if mhz, err := strconv.Atoi(fields[1]); err == nil {
					cur.Channel = freqToChannel(mhz)
					cur.Band = freqToBand(mhz)
				}
			}
		case strings.HasPrefix(trim, "DS Parameter set:") || strings.HasPrefix(trim, "primary channel:"):
			// "DS Parameter set: channel 6" or "* primary channel: 6"
			cur.Channel = extractChannel(trim, cur.Channel)
		case strings.HasPrefix(trim, "RSN:") || strings.HasPrefix(trim, "WPA:"):
			cur.Secured = true
		case strings.HasPrefix(trim, "capability:"):
			// "capability: ESS Privacy ShortPreamble (0x1431)"
			// Privacy bit also indicates encryption (covers WEP-only
			// networks too, which are rare but worth flagging).
			if strings.Contains(trim, "Privacy") {
				cur.Secured = true
			}
		}
	}
	flush()
	return results
}

func parseBSSID(line string) string {
	// "BSS aa:bb:cc:dd:ee:ff(on wlan0)" — take the second word, strip
	// any trailing parenthesis annotation.
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return ""
	}
	bssid := parts[1]
	if i := strings.Index(bssid, "("); i > 0 {
		bssid = bssid[:i]
	}
	return bssid
}

func extractChannel(line string, fallback int) int {
	for _, tok := range strings.Fields(line) {
		if ch, err := strconv.Atoi(tok); err == nil && ch > 0 && ch < 200 {
			return ch
		}
	}
	return fallback
}

// freqToChannel maps a 2.4/5 GHz frequency in MHz to its channel.
// Returns 0 for frequencies outside the standard plan.
func freqToChannel(mhz int) int {
	switch {
	case mhz == 2484:
		return 14
	case mhz >= 2412 && mhz <= 2472:
		return (mhz - 2407) / 5
	case mhz >= 5180 && mhz <= 5825:
		return (mhz - 5000) / 5
	}
	return 0
}

func freqToBand(mhz int) string {
	switch {
	case mhz >= 2400 && mhz < 2500:
		return "2.4"
	case mhz >= 5000 && mhz < 6000:
		return "5"
	}
	return ""
}
