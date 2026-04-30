//go:build linux

package linux

import "testing"

// Realistic excerpt from `iw dev wlan0 scan`, edited down to the
// lines our parser cares about, plus enough surrounding chatter to
// catch off-by-one parsing bugs.
const sampleScan = `BSS aa:bb:cc:dd:ee:01(on wlan0)
	last seen: 5 ms ago
	freq: 2437
	signal: -45.00 dBm
	SSID: HomeWiFi
	capability: ESS Privacy ShortPreamble (0x1431)
	RSN:	 * Version: 1
		 * Group cipher: CCMP
BSS aa:bb:cc:dd:ee:02(on wlan0)
	freq: 2462
	signal: -68.00 dBm
	SSID: Neighbor
	capability: ESS Privacy (0x0411)
BSS aa:bb:cc:dd:ee:03(on wlan0)
	freq: 2412
	signal: -78.50 dBm
	SSID: FreeCafe
	capability: ESS ShortPreamble (0x0421)
BSS aa:bb:cc:dd:ee:04(on wlan0)
	freq: 5180
	signal: -55.00 dBm
	SSID: HomeWiFi-5G
	capability: ESS Privacy (0x0411)
	RSN:	 * Version: 1
`

func TestParseIWScan(t *testing.T) {
	nets := parseIWScan(sampleScan)
	if len(nets) != 4 {
		t.Fatalf("expected 4 networks, got %d", len(nets))
	}
	checkBy := func(ssid string, want network) {
		t.Helper()
		var found *network
		for i := range nets {
			if nets[i].SSID == ssid {
				n := network{
					BSSID: nets[i].BSSID, Channel: nets[i].Channel,
					Band: nets[i].Band, RSSI: nets[i].RSSIdBm, Secured: nets[i].Secured,
				}
				found = &n
				break
			}
		}
		if found == nil {
			t.Errorf("ssid %q missing from results", ssid)
			return
		}
		if *found != want {
			t.Errorf("ssid %q: got %+v, want %+v", ssid, *found, want)
		}
	}
	checkBy("HomeWiFi", network{"aa:bb:cc:dd:ee:01", 6, "2.4", -45, true})
	checkBy("Neighbor", network{"aa:bb:cc:dd:ee:02", 11, "2.4", -68, true})
	checkBy("FreeCafe", network{"aa:bb:cc:dd:ee:03", 1, "2.4", -78, false})
	checkBy("HomeWiFi-5G", network{"aa:bb:cc:dd:ee:04", 36, "5", -55, true})
}

type network struct {
	BSSID   string
	Channel int
	Band    string
	RSSI    int
	Secured bool
}

func TestFreqToChannel(t *testing.T) {
	cases := map[int]int{
		2412: 1,
		2437: 6,
		2472: 13,
		2484: 14,
		5180: 36,
		5825: 165,
		1234: 0, // outside the plan
	}
	for mhz, want := range cases {
		if got := freqToChannel(mhz); got != want {
			t.Errorf("freqToChannel(%d) = %d, want %d", mhz, got, want)
		}
	}
}
