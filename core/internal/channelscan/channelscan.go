// Package channelscan turns the raw `iw scan` output that already
// drives the setup wizard's network picker into a "which 2.4 GHz
// channel should my AP use" recommendation.
//
// We don't try to do channel selection in the kernel (DFS, ACS) —
// hostapd has its own auto pick that often falls flat in apartment
// blocks because it doesn't see overlapping APs as competition.
// What this package does is straightforward: weight every visible
// neighbour by signal strength, project that onto the
// non-overlapping 2.4 GHz channels (1, 6, 11), recommend the
// least-loaded.
//
// 5 GHz is intentionally out of scope for v0.4. Pi Zero 2W is
// 2.4-only on its onboard radio anyway, and channel selection on
// 5 GHz is dominated by DFS rules we don't want to second-guess.
package channelscan

import (
	"sort"

	"github.com/knot-os/knot-os/core/internal/network"
)

// ChannelLoad is the per-channel load summary returned to the UI.
type ChannelLoad struct {
	// Channel is the centre channel number (1..14 for 2.4 GHz).
	Channel int `json:"channel"`
	// Networks counts directly-reported APs on this exact channel.
	Networks int `json:"networks"`
	// Score is a weighted sum across this channel and its
	// overlapping neighbours. Higher = more competition. The
	// recommended channel has the minimum score among
	// non-overlapping candidates.
	Score float64 `json:"score"`
	// Recommended is true on the channel we'd suggest the user
	// switch to. Exactly one channel in a Report is marked.
	Recommended bool `json:"recommended,omitempty"`
}

// Report is the full /api/network/channels response shape.
type Report struct {
	// Band is "2.4" today. Hard-coded; 5 GHz support is a v0.5+ task.
	Band string `json:"band"`
	// Channels is the per-channel load array, one entry per
	// non-DFS channel (1..13 in our region defaults).
	Channels []ChannelLoad `json:"channels"`
	// Recommended is the channel number we'd suggest, picked from
	// the non-overlapping candidates {1, 6, 11}.
	Recommended int `json:"recommended"`
	// CurrentChannel, when non-zero, is the channel knotd is
	// currently broadcasting its main AP on. Lets the UI grey out
	// the "switch to N" button when N == current.
	CurrentChannel int `json:"current_channel,omitempty"`
}

// nonOverlapping is the standard {1, 6, 11} set. Other channels
// overlap two of these and are never picked as recommendations.
var nonOverlapping = []int{1, 6, 11}

// Compute walks the scan results and produces a Report. Pure
// function; the API handler is responsible for actually invoking
// backend.Scan and passing the result in.
func Compute(networks []network.ScannedNetwork, currentChannel int) Report {
	r := Report{
		Band:           "2.4",
		CurrentChannel: currentChannel,
	}
	// Index networks by channel for the per-channel "Networks" count.
	byChannel := make(map[int]int)
	weight := make(map[int]float64) // self-channel weight, no overlap spread yet
	for _, n := range networks {
		// Filter: only 2.4 GHz, and only channels we'd actually
		// recommend for (1..13). Channel 14 is JP-only.
		if n.Band != "2.4" {
			continue
		}
		if n.Channel < 1 || n.Channel > 13 {
			continue
		}
		byChannel[n.Channel]++
		weight[n.Channel] += signalWeight(n.RSSIdBm)
	}

	// Spread each AP's weight to its overlapping neighbours.
	// 2.4 GHz channels are 5 MHz apart but each AP is 20-40 MHz wide.
	// Within ±4 channels we count partial overlap; ≥5 channels apart
	// is non-overlapping.
	spread := make(map[int]float64)
	for ch := 1; ch <= 13; ch++ {
		w := weight[ch]
		if w == 0 {
			continue
		}
		for other := 1; other <= 13; other++ {
			diff := ch - other
			if diff < 0 {
				diff = -diff
			}
			if diff >= 5 {
				continue
			}
			// Linear falloff: same channel = 1.0, ±1 = 0.8, ..., ±4 = 0.2.
			factor := 1.0 - 0.2*float64(diff)
			spread[other] += w * factor
		}
	}

	// Build per-channel rows.
	for ch := 1; ch <= 13; ch++ {
		r.Channels = append(r.Channels, ChannelLoad{
			Channel:  ch,
			Networks: byChannel[ch],
			Score:    spread[ch],
		})
	}

	// Pick the recommended channel from {1, 6, 11}.
	r.Recommended = pickBest(spread)
	for i := range r.Channels {
		if r.Channels[i].Channel == r.Recommended {
			r.Channels[i].Recommended = true
		}
	}
	return r
}

// signalWeight maps RSSI (negative dBm) to a "how loud is this AP"
// number on a 0..1 scale. -50 dBm and stronger → 1.0, -90 dBm and
// weaker → ~0.0. Linear in between, clamped.
func signalWeight(rssi int) float64 {
	if rssi >= -50 {
		return 1.0
	}
	if rssi <= -90 {
		return 0.05 // floor: an AP that's barely audible still
		// counts a tiny bit, so a channel with 20 distant APs
		// looks worse than one with 0.
	}
	return 1.0 - (float64(-50-rssi) / 40.0)
}

// pickBest returns the non-overlapping channel with the lowest
// spread weight. Ties broken by preferring lower channel numbers
// (1 < 6 < 11) — same as most consumer routers.
func pickBest(spread map[int]float64) int {
	type cand struct {
		ch    int
		score float64
	}
	cs := make([]cand, 0, len(nonOverlapping))
	for _, ch := range nonOverlapping {
		cs = append(cs, cand{ch: ch, score: spread[ch]})
	}
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].score != cs[j].score {
			return cs[i].score < cs[j].score
		}
		return cs[i].ch < cs[j].ch
	})
	return cs[0].ch
}
