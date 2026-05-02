package dns

import (
	"sort"
	"sync"
	"time"
)

// RingLog is a fixed-capacity in-memory ring buffer of QueryEvents
// plus a small set of running aggregates. Implements the QueryLog
// interface and is what the API surface exposes through
// GET /api/dns/queries and GET /api/dns/stats.
//
// Memory bound is intentional: at the default 2048-event capacity
// the buffer occupies on the order of 1 MB on Zero 2W, even with
// long domain names. Older events are overwritten silently — the
// log is a debugging / "what just happened" view, not an audit trail.
//
// All methods are safe for concurrent use.
type RingLog struct {
	mu     sync.Mutex
	buf    []QueryEvent
	pos    int  // next slot to write
	full   bool // has the buffer wrapped at least once?
	cap    int

	// Aggregates accumulated since process start. They are NOT
	// derived from the ring contents — that would lose data once
	// the buffer wraps. Cheap to maintain on the hot path.
	totalQueries  uint64
	totalBlocked  uint64
	blockedCounts map[string]uint64 // qname -> count, blocked queries only
}

// DefaultRingCapacity is the buffer size used when 0 is passed.
const DefaultRingCapacity = 2048

// NewRingLog constructs a RingLog with the given capacity. capacity
// <= 0 falls back to DefaultRingCapacity.
func NewRingLog(capacity int) *RingLog {
	if capacity <= 0 {
		capacity = DefaultRingCapacity
	}
	return &RingLog{
		buf:           make([]QueryEvent, capacity),
		cap:           capacity,
		blockedCounts: make(map[string]uint64),
	}
}

// Append records a query event. Implements QueryLog.
func (l *RingLog) Append(e QueryEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf[l.pos] = e
	l.pos++
	if l.pos == l.cap {
		l.pos = 0
		l.full = true
	}
	l.totalQueries++
	if e.Blocked {
		l.totalBlocked++
		l.blockedCounts[e.QName]++
	}
}

// Snapshot returns up to limit most-recent events whose When is at
// or after `since`. limit <= 0 means "all in the ring". since.IsZero()
// means "no time floor".
//
// Events are returned newest-first. The returned slice is a fresh
// copy; callers may mutate it.
func (l *RingLog) Snapshot(limit int, since time.Time) []QueryEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := l.cap
	if !l.full {
		n = l.pos
	}
	if n == 0 {
		return nil
	}
	if limit <= 0 || limit > n {
		limit = n
	}
	out := make([]QueryEvent, 0, limit)
	// Walk newest-first: starting one before pos, wrapping backward.
	for i, idx := 0, l.prev(l.pos); i < n && len(out) < limit; i, idx = i+1, l.prev(idx) {
		ev := l.buf[idx]
		if !since.IsZero() && ev.When.Before(since) {
			break
		}
		out = append(out, ev)
	}
	return out
}

// prev returns the buffer index immediately before idx.
func (l *RingLog) prev(idx int) int {
	if idx == 0 {
		return l.cap - 1
	}
	return idx - 1
}

// Stats is a snapshot of the cumulative counters and the top blocked
// domains. Used by the GET /api/dns/stats handler.
type Stats struct {
	TotalQueries uint64       `json:"total_queries"`
	TotalBlocked uint64       `json:"total_blocked"`
	TopBlocked   []TopBlocked `json:"top_blocked"`
	BufferSize   int          `json:"buffer_size"`
	BufferCap    int          `json:"buffer_cap"`
}

// TopBlocked is one entry of the top-N blocked-domain list.
type TopBlocked struct {
	Name  string `json:"name"`
	Count uint64 `json:"count"`
}

// Stats returns a copy of the running aggregates plus the top-N
// blocked domains by count. n <= 0 returns no top list.
func (l *RingLog) Stats(topN int) Stats {
	l.mu.Lock()
	defer l.mu.Unlock()
	bufSize := l.cap
	if !l.full {
		bufSize = l.pos
	}
	s := Stats{
		TotalQueries: l.totalQueries,
		TotalBlocked: l.totalBlocked,
		BufferSize:   bufSize,
		BufferCap:    l.cap,
	}
	if topN > 0 && len(l.blockedCounts) > 0 {
		all := make([]TopBlocked, 0, len(l.blockedCounts))
		for name, c := range l.blockedCounts {
			all = append(all, TopBlocked{Name: name, Count: c})
		}
		sort.Slice(all, func(i, j int) bool {
			if all[i].Count != all[j].Count {
				return all[i].Count > all[j].Count
			}
			return all[i].Name < all[j].Name
		})
		if len(all) > topN {
			all = all[:topN]
		}
		s.TopBlocked = all
	}
	return s
}
