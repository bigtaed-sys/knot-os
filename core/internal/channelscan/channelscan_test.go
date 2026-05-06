package channelscan

import (
	"testing"

	"github.com/knot-os/knot-os/core/internal/network"
)

func TestComputeEmpty(t *testing.T) {
	r := Compute(nil, 0)
	if r.Band != "2.4" || len(r.Channels) != 13 {
		t.Errorf("empty: %+v", r)
	}
	// With no neighbours every {1,6,11} candidate has score 0; tie
	// breaks toward 1.
	if r.Recommended != 1 {
		t.Errorf("recommended on empty: %d", r.Recommended)
	}
}

func TestComputePicksLeastLoaded(t *testing.T) {
	// Crowded channel 1, empty 6, mildly busy 11 → expect 6.
	scan := []network.ScannedNetwork{
		{SSID: "a", Channel: 1, Band: "2.4", RSSIdBm: -45},
		{SSID: "b", Channel: 1, Band: "2.4", RSSIdBm: -50},
		{SSID: "c", Channel: 1, Band: "2.4", RSSIdBm: -55},
		{SSID: "d", Channel: 11, Band: "2.4", RSSIdBm: -85},
	}
	r := Compute(scan, 0)
	if r.Recommended != 6 {
		t.Errorf("recommended = %d, want 6", r.Recommended)
	}
	// And the per-channel marker should be set on exactly the
	// recommended row.
	count := 0
	for _, c := range r.Channels {
		if c.Recommended {
			count++
			if c.Channel != 6 {
				t.Errorf("recommended marker on wrong channel: %d", c.Channel)
			}
		}
	}
	if count != 1 {
		t.Errorf("recommended-marker count: %d", count)
	}
}

func TestComputeOverlapSpread(t *testing.T) {
	// AP on channel 4 should pollute channels 1 and 6 (within ±5
	// of each other). 11 should be the unaffected best pick.
	scan := []network.ScannedNetwork{
		{SSID: "noise", Channel: 4, Band: "2.4", RSSIdBm: -40},
	}
	r := Compute(scan, 0)
	if r.Recommended != 11 {
		t.Errorf("recommended = %d, want 11 (overlap spread)", r.Recommended)
	}

	// Channel 4's own row should be the loudest.
	scoreByChan := map[int]float64{}
	for _, c := range r.Channels {
		scoreByChan[c.Channel] = c.Score
	}
	if scoreByChan[4] <= scoreByChan[6] {
		t.Errorf("channel 4 score should exceed 6's (own > overlap), got 4=%.2f 6=%.2f",
			scoreByChan[4], scoreByChan[6])
	}
}

func TestSignalWeightFloors(t *testing.T) {
	// Strong / medium / weak / floor sanity.
	if signalWeight(-30) != 1.0 {
		t.Error("strong signal not pegged at 1.0")
	}
	if signalWeight(-100) > 0.1 {
		t.Error("weakest signal should be near floor")
	}
	mid := signalWeight(-70)
	if mid < 0.4 || mid > 0.6 {
		t.Errorf("mid signal weight = %.2f, want ~0.5", mid)
	}
}

func TestComputeIgnores5GHz(t *testing.T) {
	scan := []network.ScannedNetwork{
		{SSID: "5g", Channel: 36, Band: "5", RSSIdBm: -40},
	}
	r := Compute(scan, 0)
	for _, c := range r.Channels {
		if c.Score != 0 {
			t.Errorf("5GHz scan leaked into 2.4GHz score: %+v", c)
		}
	}
}
