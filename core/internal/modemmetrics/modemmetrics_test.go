package modemmetrics

import (
	"path/filepath"
	"testing"
	"time"
)

func TestObserve_AccumulatesDeltas(t *testing.T) {
	tr := New(1, "")
	at := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	tr.Observe(at, "wwan0", 1000, 200, 70)                     // first: sets base, no delta
	tr.Observe(at.Add(30*time.Second), "wwan0", 1500, 500, 68) // +500 rx, +300 tx

	s := tr.Snapshot()
	if s.RxBytes != 500 || s.TxBytes != 300 {
		t.Errorf("rx/tx = %d/%d, want 500/300", s.RxBytes, s.TxBytes)
	}
	if s.TotalBytes != 800 {
		t.Errorf("total = %d, want 800", s.TotalBytes)
	}
}

func TestObserve_HandlesCounterReset(t *testing.T) {
	tr := New(1, "")
	at := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	tr.Observe(at, "wwan0", 10_000, 5_000, 70)
	// Counter reset (same iface, but value dropped): treat new value as delta.
	tr.Observe(at.Add(30*time.Second), "wwan0", 300, 100, 70)
	s := tr.Snapshot()
	if s.RxBytes != 300 || s.TxBytes != 100 {
		t.Errorf("after reset rx/tx = %d/%d, want 300/100", s.RxBytes, s.TxBytes)
	}
}

func TestObserve_IfaceChangeSkipsDelta(t *testing.T) {
	tr := New(1, "")
	at := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	tr.Observe(at, "wwan0", 10_000, 5_000, 70)
	// New iface: its counters start fresh; don't fold a bogus delta.
	tr.Observe(at.Add(30*time.Second), "wwan1", 8_000, 4_000, 70)
	if s := tr.Snapshot(); s.RxBytes != 0 || s.TxBytes != 0 {
		t.Errorf("iface change should skip delta, got rx/tx = %d/%d", s.RxBytes, s.TxBytes)
	}
	// Next reading on the new iface accumulates normally.
	tr.Observe(at.Add(60*time.Second), "wwan1", 8_500, 4_200, 70)
	if s := tr.Snapshot(); s.RxBytes != 500 || s.TxBytes != 200 {
		t.Errorf("rx/tx = %d/%d, want 500/200", s.RxBytes, s.TxBytes)
	}
}

func TestObserve_RollsBillingCycle(t *testing.T) {
	tr := New(1, "") // reset on the 1st
	jul := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	tr.Observe(jul, "wwan0", 1000, 1000, 70)
	tr.Observe(jul.Add(30*time.Second), "wwan0", 2000, 2000, 70) // +1000/+1000
	if s := tr.Snapshot(); s.RxBytes != 1000 {
		t.Fatalf("pre-roll rx = %d, want 1000", s.RxBytes)
	}
	// Cross into August → cycle resets.
	aug := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	tr.Observe(aug, "wwan0", 3000, 3000, 70) // reset + set base
	tr.Observe(aug.Add(30*time.Second), "wwan0", 3400, 3300, 70)
	s := tr.Snapshot()
	if s.RxBytes != 400 || s.TxBytes != 300 {
		t.Errorf("post-roll rx/tx = %d/%d, want 400/300", s.RxBytes, s.TxBytes)
	}
	if !s.CycleStart.Equal(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("cycleStart = %v, want 2026-08-01", s.CycleStart)
	}
}

func TestSignalRing(t *testing.T) {
	tr := New(1, "")
	base := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	for i := 0; i < SignalKept+50; i++ {
		tr.Observe(base.Add(time.Duration(i)*time.Second), "wwan0", uint64(i), 0, i%100)
	}
	// A negative signal is "unknown" and must not be recorded.
	tr.Observe(base.Add(time.Hour), "wwan0", 99999, 0, -1)
	s := tr.Snapshot()
	if len(s.Signal) != SignalKept {
		t.Errorf("signal ring = %d, want %d", len(s.Signal), SignalKept)
	}
}

func TestSaveLoad_RoundTripsWithinCycle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.json")
	tr := New(1, path)
	// Force a cycle in the current period so Load adopts it.
	now := time.Now()
	tr.Observe(now, "wwan0", 0, 0, 70)
	tr.Observe(now.Add(time.Second), "wwan0", 4096, 1024, 70)
	if err := tr.Save(); err != nil {
		t.Fatal(err)
	}

	loaded := New(1, path)
	if err := loaded.Load(); err != nil {
		t.Fatal(err)
	}
	s := loaded.Snapshot()
	if s.RxBytes != 4096 || s.TxBytes != 1024 {
		t.Errorf("loaded rx/tx = %d/%d, want 4096/1024", s.RxBytes, s.TxBytes)
	}
}

func TestLoad_DropsStaleCycle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.json")
	// Hand-write a usage file from a clearly-past cycle.
	stale := New(1, path)
	stale.mu.Lock()
	stale.cycleStart = time.Date(2020, 1, 1, 0, 0, 0, 0, time.Local)
	stale.rx, stale.tx = 9_000_000, 3_000_000
	stale.mu.Unlock()
	if err := stale.Save(); err != nil {
		t.Fatal(err)
	}

	loaded := New(1, path)
	if err := loaded.Load(); err != nil {
		t.Fatal(err)
	}
	if s := loaded.Snapshot(); s.RxBytes != 0 || s.TxBytes != 0 {
		t.Errorf("stale cycle should be dropped, got rx/tx = %d/%d", s.RxBytes, s.TxBytes)
	}
}

func TestCycleStartFor(t *testing.T) {
	// resetDay 10; now on the 15th → cycle started the 10th this month.
	now := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)
	if got := cycleStartFor(now, 10); !got.Equal(time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("mid-cycle: got %v", got)
	}
	// now on the 5th, before the 10th → cycle started the 10th of June.
	now = time.Date(2026, 7, 5, 8, 0, 0, 0, time.UTC)
	if got := cycleStartFor(now, 10); !got.Equal(time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("pre-cycle: got %v", got)
	}
	// Clamp out-of-range day.
	if got := cycleStartFor(now, 99); got.Day() != 28 {
		t.Errorf("clamp: day = %d, want 28", got.Day())
	}
}
