package dns

import (
	"testing"
	"time"
)

func TestRingLogAppendAndSnapshotOrder(t *testing.T) {
	l := NewRingLog(4)
	base := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	for i, name := range []string{"a.example", "b.example", "c.example"} {
		l.Append(QueryEvent{When: base.Add(time.Duration(i) * time.Second), QName: name, QType: "A"})
	}
	got := l.Snapshot(0, time.Time{})
	if len(got) != 3 {
		t.Fatalf("want 3 events, got %d", len(got))
	}
	// Newest first.
	wantOrder := []string{"c.example", "b.example", "a.example"}
	for i, e := range got {
		if e.QName != wantOrder[i] {
			t.Errorf("idx %d: want %q, got %q", i, wantOrder[i], e.QName)
		}
	}
}

func TestRingLogWrapsKeepsNewest(t *testing.T) {
	l := NewRingLog(3)
	base := time.Now()
	for i, name := range []string{"a", "b", "c", "d", "e"} {
		l.Append(QueryEvent{When: base.Add(time.Duration(i) * time.Second), QName: name})
	}
	got := l.Snapshot(0, time.Time{})
	if len(got) != 3 {
		t.Fatalf("want 3, got %d", len(got))
	}
	wantOrder := []string{"e", "d", "c"}
	for i, e := range got {
		if e.QName != wantOrder[i] {
			t.Errorf("idx %d: want %q, got %q", i, wantOrder[i], e.QName)
		}
	}
}

func TestRingLogSnapshotLimitAndSince(t *testing.T) {
	l := NewRingLog(10)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 6; i++ {
		l.Append(QueryEvent{When: base.Add(time.Duration(i) * time.Minute), QName: "x"})
	}
	if got := l.Snapshot(2, time.Time{}); len(got) != 2 {
		t.Errorf("limit=2: got %d", len(got))
	}
	since := base.Add(4 * time.Minute) // include events at min 4 and 5
	got := l.Snapshot(0, since)
	if len(got) != 2 {
		t.Fatalf("since: want 2, got %d", len(got))
	}
}

func TestRingLogStatsAndTopBlocked(t *testing.T) {
	l := NewRingLog(16)
	now := time.Now()
	l.Append(QueryEvent{When: now, QName: "ok.example", QType: "A"})
	l.Append(QueryEvent{When: now, QName: "ads.tracker.com", QType: "A", Blocked: true, BlockedBy: "ads"})
	l.Append(QueryEvent{When: now, QName: "ads.tracker.com", QType: "A", Blocked: true, BlockedBy: "ads"})
	l.Append(QueryEvent{When: now, QName: "doubleclick.net", QType: "A", Blocked: true, BlockedBy: "ads"})

	s := l.Stats(5)
	if s.TotalQueries != 4 {
		t.Errorf("TotalQueries=%d", s.TotalQueries)
	}
	if s.TotalBlocked != 3 {
		t.Errorf("TotalBlocked=%d", s.TotalBlocked)
	}
	if len(s.TopBlocked) != 2 {
		t.Fatalf("TopBlocked len=%d", len(s.TopBlocked))
	}
	if s.TopBlocked[0].Name != "ads.tracker.com" || s.TopBlocked[0].Count != 2 {
		t.Errorf("top[0]: %+v", s.TopBlocked[0])
	}
	if s.BufferSize != 4 || s.BufferCap != 16 {
		t.Errorf("BufferSize=%d BufferCap=%d", s.BufferSize, s.BufferCap)
	}
}
