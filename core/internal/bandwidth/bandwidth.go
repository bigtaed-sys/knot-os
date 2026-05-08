// Package bandwidth tracks per-device traffic in bytes and rate.
//
// Source: /proc/net/nf_conntrack (Linux). Each connection-tracker
// entry has cumulative byte counters in both directions; aggregating
// by source IP gives a per-LAN-device view of how much traffic that
// device has sent + received since the conntrack entry was created.
//
// We sample every 2 seconds and compute delta-vs-last-sample to get
// a current Kbps. Per-MAC samples are kept in a ring buffer for the
// last 30 minutes (900 samples), enough to power a UI sparkline.
// Cumulative per-day / per-week totals are kept separately and
// persisted every 5 minutes.
//
// Why conntrack and not nftables counters: dynamic per-device nft
// counters require us to mutate the ruleset every DHCP renewal,
// keying off ever-changing IPs. Conntrack already does that work
// in the kernel — we just read the byte fields. Tradeoff: only
// connection-oriented protocols (TCP / UDP / ICMP) are counted.
// In practice that covers >99% of LAN traffic; raw IP / multicast
// traffic isn't visible, but those aren't what users care about
// when looking at "how much did the kid's laptop use today".
package bandwidth

import (
	"sync"
	"time"
)

// SampleInterval is how often the sampler reads conntrack and
// updates per-device rates. 2s gives a smooth-enough sparkline
// without thrashing the conntrack hashtable.
const SampleInterval = 2 * time.Second

// SamplesKept is how many samples per device the ring buffer holds.
// 900 samples × 2s = 30 minutes — enough for a UI sparkline at
// 1-min resolution averaged over 30 min.
const SamplesKept = 900

// Sample is one moment-in-time bandwidth reading for a device.
type Sample struct {
	// At is the wall-clock time the sample was taken.
	At time.Time `json:"at"`
	// KbpsIn / KbpsOut are the rate over the previous SampleInterval.
	// Computed as (bytes_now - bytes_last) * 8 / 1024 / interval_sec.
	KbpsIn  float64 `json:"kbps_in"`
	KbpsOut float64 `json:"kbps_out"`
}

// Stats is the externally-visible summary for one device.
type Stats struct {
	// MAC identifies the device.
	MAC string `json:"mac"`
	// LastSample is the most recent rate snapshot.
	LastSample Sample `json:"last_sample"`
	// Sparkline is the last N samples (oldest first), suitable for
	// a 30-minute mini-graph in the UI.
	Sparkline []Sample `json:"sparkline"`
	// CumIn / CumOut are total bytes in/out since the device was
	// first observed in this knotd run. Resets on daemon restart.
	CumIn  uint64 `json:"cum_in"`
	CumOut uint64 `json:"cum_out"`
}

// deviceState is the per-MAC accounting we maintain in memory.
type deviceState struct {
	// Last raw byte counts; used to compute deltas on next sample.
	lastBytesIn  uint64
	lastBytesOut uint64
	// Cumulative since process start.
	cumIn  uint64
	cumOut uint64
	// Ring buffer of recent samples (oldest first).
	samples []Sample
}

// Tracker is the in-memory state of the bandwidth subsystem.
//
// Concurrency: a single goroutine runs the sampler; readers (API
// handlers, Telegram bot, scheduler) call snapshot methods which
// take a read lock and copy. Writes happen only from the sampler.
type Tracker struct {
	mu      sync.RWMutex
	devices map[string]*deviceState // by MAC
}

// NewTracker builds an empty tracker. Wire a Sampler into it via
// Run to actually populate it.
func NewTracker() *Tracker {
	return &Tracker{
		devices: make(map[string]*deviceState),
	}
}

// FlowMetric is what the platform-specific Sampler hands the
// Tracker — the cumulative byte count for one (mac, direction).
// "in" = traffic received by the device, "out" = sent by it.
type FlowMetric struct {
	MAC      string
	BytesIn  uint64
	BytesOut uint64
}

// Update merges a fresh batch of metrics into the tracker, computes
// per-device deltas, and pushes a new Sample into each ring. Called
// by the sampler goroutine on every tick.
//
// Each Update represents the world at time `at`. MACs not present
// in the batch get a zero sample (they're idle), MACs not in any
// previous batch get added.
func (t *Tracker) Update(at time.Time, batch []FlowMetric, intervalSec float64) {
	if intervalSec <= 0 {
		intervalSec = SampleInterval.Seconds()
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	seen := make(map[string]bool, len(batch))
	for _, m := range batch {
		seen[m.MAC] = true
		st, ok := t.devices[m.MAC]
		if !ok {
			st = &deviceState{}
			t.devices[m.MAC] = st
		}
		// Conntrack byte counters can reset (entry expired and
		// re-created). If the new value is LESS than the previous,
		// treat the delta as the new value — close enough for sparkline.
		dIn := safeDelta(m.BytesIn, st.lastBytesIn)
		dOut := safeDelta(m.BytesOut, st.lastBytesOut)
		st.lastBytesIn = m.BytesIn
		st.lastBytesOut = m.BytesOut
		st.cumIn += dIn
		st.cumOut += dOut
		st.samples = append(st.samples, Sample{
			At:      at,
			KbpsIn:  bytesToKbps(dIn, intervalSec),
			KbpsOut: bytesToKbps(dOut, intervalSec),
		})
		if len(st.samples) > SamplesKept {
			st.samples = st.samples[len(st.samples)-SamplesKept:]
		}
	}
	// Idle devices: push a zero sample so the sparkline stays
	// continuous and decays visibly when traffic stops.
	for mac, st := range t.devices {
		if seen[mac] {
			continue
		}
		st.samples = append(st.samples, Sample{At: at})
		if len(st.samples) > SamplesKept {
			st.samples = st.samples[len(st.samples)-SamplesKept:]
		}
	}
}

// Snapshot returns the current Stats for a single device, or zero
// value + false if no samples for that MAC.
func (t *Tracker) Snapshot(mac string) (Stats, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	st, ok := t.devices[mac]
	if !ok || len(st.samples) == 0 {
		return Stats{}, false
	}
	return Stats{
		MAC:        mac,
		LastSample: st.samples[len(st.samples)-1],
		Sparkline:  append([]Sample(nil), st.samples...),
		CumIn:      st.cumIn,
		CumOut:     st.cumOut,
	}, true
}

// SnapshotAll returns Stats for every tracked device. Concurrency-
// safe; the slices in each Stats are independent copies.
func (t *Tracker) SnapshotAll() []Stats {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]Stats, 0, len(t.devices))
	for mac, st := range t.devices {
		if len(st.samples) == 0 {
			continue
		}
		out = append(out, Stats{
			MAC:        mac,
			LastSample: st.samples[len(st.samples)-1],
			Sparkline:  append([]Sample(nil), st.samples...),
			CumIn:      st.cumIn,
			CumOut:     st.cumOut,
		})
	}
	return out
}

// Forget drops a device from the tracker. Used when a MAC is
// removed from the device registry — without this, gone devices
// linger forever.
func (t *Tracker) Forget(mac string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.devices, mac)
}

// safeDelta returns curr-prev, or curr if curr<prev (counter reset).
func safeDelta(curr, prev uint64) uint64 {
	if curr >= prev {
		return curr - prev
	}
	return curr
}

// bytesToKbps converts a byte delta over the given seconds into
// kilobits-per-second. We divide by 1000 (decimal Kb), matching
// what every speedtest UI shows.
func bytesToKbps(bytes uint64, sec float64) float64 {
	if sec <= 0 {
		return 0
	}
	return float64(bytes*8) / 1000.0 / sec
}
