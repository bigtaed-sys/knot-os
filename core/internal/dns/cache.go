package dns

import (
	"sync"
	"time"

	mdns "github.com/miekg/dns"
)

// Cache is a tiny TTL-bound DNS response cache, sitting in front of
// the upstream forwarder. It dedupes the burst of duplicate queries
// you get from "every device on the LAN refreshes its feed at once",
// and keeps Zero 2W's outbound DNS chatter friendly to the upstream.
//
// Design choices:
//   - Key on (qname, qtype, qclass). Different qclasses get separate
//     entries (the IN class is dominant but CHAOS / others exist).
//   - Per-entry expiry derived from the smallest TTL in the answer
//     section, capped at MaxTTL. RFC 2181 says don't re-serve past
//     the original TTL; we honor it.
//   - No negative caching beyond MinNegTTL: NXDOMAIN gets a short
//     fixed TTL (60s) so a fixed typo doesn't permanently shadow a
//     domain that comes back online. Blocklist NXDOMAINs are NOT
//     written to the cache — those are recomputed per-query because
//     the answer depends on the source's profile.
//   - Capacity-bounded: a soft eviction sweep runs lazily once the
//     map exceeds Capacity, dropping anything past expiry.
//
// All methods are safe for concurrent use.
type Cache struct {
	mu       sync.Mutex
	entries  map[cacheKey]cacheEntry
	cap      int
	maxTTL   time.Duration
	minNegTTL time.Duration
	now      func() time.Time // overridable in tests
}

type cacheKey struct {
	name   string
	qtype  uint16
	qclass uint16
}

type cacheEntry struct {
	msg     *mdns.Msg
	expires time.Time
}

// CacheOptions configures NewCache.
type CacheOptions struct {
	// Capacity is the soft upper bound on entries. Past this, a
	// lazy sweep evicts expired entries on the next Set.
	Capacity int
	// MaxTTL caps the longest TTL any entry is held for, regardless
	// of upstream TTL. 0 => 1h.
	MaxTTL time.Duration
	// MinNegTTL is the TTL applied to NXDOMAIN responses. 0 => 60s.
	MinNegTTL time.Duration
	// Now lets tests inject a clock. Defaults to time.Now.
	Now func() time.Time
}

// NewCache builds a Cache with sensible defaults.
func NewCache(opts CacheOptions) *Cache {
	if opts.Capacity <= 0 {
		opts.Capacity = 4096
	}
	if opts.MaxTTL <= 0 {
		opts.MaxTTL = time.Hour
	}
	if opts.MinNegTTL <= 0 {
		opts.MinNegTTL = 60 * time.Second
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Cache{
		entries:   make(map[cacheKey]cacheEntry),
		cap:       opts.Capacity,
		maxTTL:    opts.MaxTTL,
		minNegTTL: opts.MinNegTTL,
		now:       opts.Now,
	}
}

// Get returns a cached response copy if one exists and hasn't
// expired. The returned message has its ID rewritten to match req.
func (c *Cache) Get(req *mdns.Msg) (*mdns.Msg, bool) {
	if c == nil || req == nil || len(req.Question) == 0 {
		return nil, false
	}
	q := req.Question[0]
	key := cacheKey{
		name:   normalizeDomain(q.Name),
		qtype:  q.Qtype,
		qclass: q.Qclass,
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	now := c.now()
	if !now.Before(entry.expires) {
		delete(c.entries, key)
		return nil, false
	}
	resp := entry.msg.Copy()
	resp.Id = req.Id
	// Reduce the TTLs of the cached records by the time elapsed so
	// downstream resolvers see a shrinking TTL, not a frozen one.
	elapsed := uint32(c.maxTTL.Seconds()) // sane fallback
	if remain := entry.expires.Sub(now); remain > 0 {
		elapsed = uint32(remain.Seconds())
	}
	for _, rr := range resp.Answer {
		if rr.Header().Ttl > elapsed {
			rr.Header().Ttl = elapsed
		}
	}
	return resp, true
}

// Set caches an upstream response. NXDOMAIN gets MinNegTTL; otherwise
// the smallest answer TTL (clamped to MaxTTL). Skipped entirely when
// the response has no answers and isn't NXDOMAIN — there's nothing
// reusable.
func (c *Cache) Set(req, resp *mdns.Msg) {
	if c == nil || req == nil || resp == nil || len(req.Question) == 0 {
		return
	}
	q := req.Question[0]
	ttl := c.ttlFor(resp)
	if ttl <= 0 {
		return
	}
	key := cacheKey{
		name:   normalizeDomain(q.Name),
		qtype:  q.Qtype,
		qclass: q.Qclass,
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= c.cap {
		c.sweepLocked()
	}
	c.entries[key] = cacheEntry{
		msg:     resp.Copy(),
		expires: c.now().Add(ttl),
	}
}

// Len returns the entry count (including not-yet-swept expired ones).
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// ttlFor returns the duration to cache resp for, or <=0 to skip.
func (c *Cache) ttlFor(resp *mdns.Msg) time.Duration {
	switch resp.Rcode {
	case mdns.RcodeNameError:
		return c.minNegTTL
	case mdns.RcodeSuccess:
		// Pick the smallest TTL in the answer section. If there's no
		// answer at all, don't cache (NODATA / referral cases).
		if len(resp.Answer) == 0 {
			return 0
		}
		min := uint32(c.maxTTL.Seconds())
		for _, rr := range resp.Answer {
			if rr.Header().Ttl < min {
				min = rr.Header().Ttl
			}
		}
		if min == 0 {
			// TTL=0 means "do not cache" per RFC.
			return 0
		}
		return time.Duration(min) * time.Second
	default:
		// SERVFAIL / REFUSED / etc. — not safe to remember.
		return 0
	}
}

// sweepLocked removes expired entries. Called with c.mu held.
func (c *Cache) sweepLocked() {
	now := c.now()
	for k, e := range c.entries {
		if !now.Before(e.expires) {
			delete(c.entries, k)
		}
	}
}
