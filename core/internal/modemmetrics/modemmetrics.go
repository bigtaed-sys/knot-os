// Package modemmetrics accounts cellular-WAN data usage and keeps a
// short signal history, so the UI can show "used this month" against an
// optional cap plus a signal sparkline.
//
// Source: the kernel netdev byte counters at
// /sys/class/net/<iface>/statistics/{rx,tx}_bytes (read by the linux
// backend, fed in via Observe). Those counters are cumulative-since-
// interface-up and reset whenever the modem re-enumerates, so the
// tracker computes deltas and accumulates them into a per-billing-cycle
// total that survives reboots (persisted to disk). Signal comes from
// ModemManager's 0-100 quality, sampled alongside.
//
// Platform-agnostic on purpose (no sysfs here) so the accounting logic
// is unit-testable; the linux backend does the actual counter reads.
package modemmetrics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DefaultStorePath is where the per-cycle usage total is persisted.
const DefaultStorePath = "/var/lib/knot/modem-usage.json"

// SignalKept is how many signal samples the ring holds. 120 × ~30s =
// ~1 hour of history — enough for a dashboard sparkline.
const SignalKept = 120

// SignalSample is one signal-quality reading.
type SignalSample struct {
	At      time.Time `json:"at"`
	Percent int       `json:"percent"`
}

// Usage is the persisted per-billing-cycle byte total.
type Usage struct {
	CycleStart time.Time `json:"cycle_start"`
	RxBytes    uint64    `json:"rx_bytes"`
	TxBytes    uint64    `json:"tx_bytes"`
}

// Snapshot is the read-only view the API/UI consumes.
type Snapshot struct {
	Usage
	// TotalBytes is Rx+Tx, for a single "used this cycle" figure.
	TotalBytes uint64 `json:"total_bytes"`
	// Signal is the recent signal history (oldest first).
	Signal []SignalSample `json:"signal"`
}

// Tracker accumulates data usage and signal history. Safe for one
// sampler goroutine writing (Observe/persist) and many readers
// (Snapshot).
type Tracker struct {
	mu       sync.Mutex
	resetDay int    // billing-cycle reset day of month, 1..28
	path     string // persistence file; "" disables saving

	cycleStart time.Time
	rx, tx     uint64 // cumulative since cycleStart

	haveLast       bool
	lastRx, lastTx uint64
	lastIface      string

	signal []SignalSample
}

// New builds a Tracker. resetDay is the billing-cycle reset day (1..28;
// out-of-range clamps to 1). path is the persistence file ("" = don't
// persist). Call Load to restore a prior total.
func New(resetDay int, path string) *Tracker {
	return &Tracker{resetDay: clampDay(resetDay), path: path}
}

// SetResetDay updates the billing-cycle reset day. The change takes
// effect on the next Observe (which may roll the cycle).
func (t *Tracker) SetResetDay(day int) {
	t.mu.Lock()
	t.resetDay = clampDay(day)
	t.mu.Unlock()
}

// Observe folds one counter reading into the total and records the
// signal. rawRx/rawTx are the interface's cumulative byte counters;
// iface is the current data interface (a change means the counters
// reset, so that tick's delta is skipped). signal < 0 means "unknown"
// and isn't recorded.
func (t *Tracker) Observe(at time.Time, iface string, rawRx, rawTx uint64, signal int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Roll the billing cycle when `at` crosses the reset boundary. Reset
	// the delta base too, so the boundary-spanning interval (~one sample)
	// isn't misattributed to the new cycle — the new cycle counts cleanly
	// from its first post-roll sample.
	cs := cycleStartFor(at, t.resetDay)
	if t.cycleStart.IsZero() || !cs.Equal(t.cycleStart) {
		t.cycleStart = cs
		t.rx, t.tx = 0, 0
		t.haveLast = false
	}

	// A new interface (modem re-enumerated) means the raw counters
	// restarted from a fresh base — skip the delta this tick.
	if iface != t.lastIface {
		t.haveLast = false
		t.lastIface = iface
	}
	if t.haveLast {
		t.rx += safeDelta(rawRx, t.lastRx)
		t.tx += safeDelta(rawTx, t.lastTx)
	}
	t.lastRx, t.lastTx = rawRx, rawTx
	t.haveLast = true

	if signal >= 0 {
		t.signal = append(t.signal, SignalSample{At: at, Percent: signal})
		if len(t.signal) > SignalKept {
			t.signal = t.signal[len(t.signal)-SignalKept:]
		}
	}
}

// Snapshot returns the current usage + signal history (independent copy).
func (t *Tracker) Snapshot() Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	return Snapshot{
		Usage:      Usage{CycleStart: t.cycleStart, RxBytes: t.rx, TxBytes: t.tx},
		TotalBytes: t.rx + t.tx,
		Signal:     append([]SignalSample(nil), t.signal...),
	}
}

// Save persists the current cycle total atomically. Signal history is
// ephemeral and not persisted. No-op when path is "".
func (t *Tracker) Save() error {
	t.mu.Lock()
	u := Usage{CycleStart: t.cycleStart, RxBytes: t.rx, TxBytes: t.tx}
	path := t.path
	t.mu.Unlock()
	if path == "" {
		return nil
	}
	data, err := json.MarshalIndent(u, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Load restores a persisted total. A total written in a now-past billing
// cycle is dropped (the next Observe would reset it anyway). Missing file
// is not an error. No-op when path is "".
func (t *Tracker) Load() error {
	if t.path == "" {
		return nil
	}
	data, err := os.ReadFile(t.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var u Usage
	if err := json.Unmarshal(data, &u); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	// Only adopt the persisted total if it belongs to the current cycle.
	if u.CycleStart.Equal(cycleStartFor(time.Now(), t.resetDay)) {
		t.cycleStart = u.CycleStart
		t.rx, t.tx = u.RxBytes, u.TxBytes
	}
	return nil
}

// cycleStartFor returns the most recent billing-cycle boundary (local
// midnight of resetDay) at or before now.
func cycleStartFor(now time.Time, resetDay int) time.Time {
	resetDay = clampDay(resetDay)
	y, m, _ := now.Date()
	candidate := time.Date(y, m, resetDay, 0, 0, 0, 0, now.Location())
	if now.Before(candidate) {
		candidate = candidate.AddDate(0, -1, 0)
	}
	return candidate
}

func clampDay(d int) int {
	if d < 1 {
		return 1
	}
	if d > 28 {
		return 28
	}
	return d
}

// safeDelta returns curr-prev, or curr when curr<prev (counter reset).
func safeDelta(curr, prev uint64) uint64 {
	if curr >= prev {
		return curr - prev
	}
	return curr
}
