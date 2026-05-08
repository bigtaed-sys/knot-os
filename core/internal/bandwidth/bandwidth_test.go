package bandwidth

import (
	"testing"
	"time"
)

func TestUpdateProducesSamples(t *testing.T) {
	tr := NewTracker()
	t0 := time.Unix(1_700_000_000, 0)

	// First batch sets baselines; deltas computed from previous
	// (zero) values, so kbps reflects all observed bytes.
	tr.Update(t0, []FlowMetric{
		{MAC: "aa:bb", BytesIn: 1000, BytesOut: 500},
	}, 2.0)
	st, ok := tr.Snapshot("aa:bb")
	if !ok {
		t.Fatal("snapshot missing")
	}
	if len(st.Sparkline) != 1 {
		t.Fatalf("sparkline len = %d, want 1", len(st.Sparkline))
	}

	// Second batch: bytes grew. Delta in 2s = 4000 bytes in =
	// 32_000 bits / 1000 / 2 = 16 Kbps in.
	tr.Update(t0.Add(2*time.Second), []FlowMetric{
		{MAC: "aa:bb", BytesIn: 5000, BytesOut: 1500},
	}, 2.0)
	st, _ = tr.Snapshot("aa:bb")
	if len(st.Sparkline) != 2 {
		t.Fatalf("sparkline len = %d, want 2", len(st.Sparkline))
	}
	last := st.LastSample
	if last.KbpsIn < 15 || last.KbpsIn > 17 {
		t.Errorf("KbpsIn = %.2f, want ~16", last.KbpsIn)
	}
	if last.KbpsOut < 3.5 || last.KbpsOut > 4.5 {
		t.Errorf("KbpsOut = %.2f, want ~4", last.KbpsOut)
	}
	if st.CumIn != 5000 || st.CumOut != 1500 {
		t.Errorf("Cum = %d/%d, want 5000/1500", st.CumIn, st.CumOut)
	}
}

func TestUpdateHandlesCounterReset(t *testing.T) {
	tr := NewTracker()
	t0 := time.Unix(1_700_000_000, 0)

	tr.Update(t0, []FlowMetric{{MAC: "aa", BytesIn: 10000, BytesOut: 5000}}, 2.0)
	// Counter reset (conntrack entry expired and re-created with
	// fresh state). Don't go negative.
	tr.Update(t0.Add(2*time.Second), []FlowMetric{{MAC: "aa", BytesIn: 200, BytesOut: 100}}, 2.0)
	st, _ := tr.Snapshot("aa")
	if st.LastSample.KbpsIn < 0 {
		t.Errorf("KbpsIn went negative on counter reset: %.2f", st.LastSample.KbpsIn)
	}
}

func TestIdleDeviceGetsZeroSample(t *testing.T) {
	tr := NewTracker()
	t0 := time.Unix(1_700_000_000, 0)

	tr.Update(t0, []FlowMetric{{MAC: "aa", BytesIn: 1000}}, 2.0)
	// Second tick: device "aa" not in batch (no traffic this round).
	tr.Update(t0.Add(2*time.Second), []FlowMetric{}, 2.0)

	st, _ := tr.Snapshot("aa")
	if len(st.Sparkline) != 2 {
		t.Fatalf("sparkline len = %d, want 2 (idle should still push)", len(st.Sparkline))
	}
	if st.LastSample.KbpsIn != 0 {
		t.Errorf("idle KbpsIn = %.2f, want 0", st.LastSample.KbpsIn)
	}
}

func TestRingBufferCap(t *testing.T) {
	tr := NewTracker()
	t0 := time.Unix(1_700_000_000, 0)
	for i := 0; i < SamplesKept+50; i++ {
		tr.Update(t0.Add(time.Duration(i)*2*time.Second),
			[]FlowMetric{{MAC: "aa", BytesIn: uint64(i * 1000)}}, 2.0)
	}
	st, _ := tr.Snapshot("aa")
	if len(st.Sparkline) != SamplesKept {
		t.Errorf("sparkline len = %d, want %d (cap)", len(st.Sparkline), SamplesKept)
	}
}

func TestForgetDevice(t *testing.T) {
	tr := NewTracker()
	tr.Update(time.Now(), []FlowMetric{{MAC: "aa"}}, 2.0)
	tr.Forget("aa")
	if _, ok := tr.Snapshot("aa"); ok {
		t.Error("Forget didn't drop the device")
	}
}

func TestSnapshotAllReturnsActiveOnly(t *testing.T) {
	tr := NewTracker()
	t0 := time.Unix(1_700_000_000, 0)
	tr.Update(t0, []FlowMetric{
		{MAC: "aa", BytesIn: 100},
		{MAC: "bb", BytesIn: 200},
	}, 2.0)
	all := tr.SnapshotAll()
	if len(all) != 2 {
		t.Errorf("SnapshotAll len = %d, want 2", len(all))
	}
}

func TestFormatRate(t *testing.T) {
	cases := map[float64]string{
		0:       "—",
		0.4:     "—",
		1:       "1 Kbps",
		800:     "800 Kbps",
		1234:    "1.2 Mbps",
		1024000: "1024.0 Mbps",
	}
	for in, want := range cases {
		if got := FormatRate(in); got != want {
			t.Errorf("FormatRate(%.0f) = %q, want %q", in, got, want)
		}
	}
}
