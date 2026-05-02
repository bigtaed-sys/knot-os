package dns

import (
	"testing"
	"time"

	mdns "github.com/miekg/dns"
)

func mockNow(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func newReq(name string, qtype uint16) *mdns.Msg {
	m := new(mdns.Msg)
	m.SetQuestion(mdns.Fqdn(name), qtype)
	m.Id = mdns.Id()
	return m
}

func newARespFor(req *mdns.Msg, ttl uint32, ip string) *mdns.Msg {
	resp := new(mdns.Msg)
	resp.SetReply(req)
	rr := &mdns.A{
		Hdr: mdns.RR_Header{
			Name:   req.Question[0].Name,
			Rrtype: mdns.TypeA,
			Class:  mdns.ClassINET,
			Ttl:    ttl,
		},
	}
	rr.A = parseIP4(ip)
	resp.Answer = []mdns.RR{rr}
	return resp
}

func parseIP4(s string) []byte {
	// minimal — assumes well-formed IPv4
	out := make([]byte, 4)
	parts := []byte{0, 0, 0, 0}
	idx := 0
	cur := 0
	for _, c := range s {
		if c == '.' {
			parts[idx] = byte(cur)
			idx++
			cur = 0
			continue
		}
		cur = cur*10 + int(c-'0')
	}
	parts[idx] = byte(cur)
	copy(out, parts)
	return out
}

func TestCacheStoresAndReturnsCopy(t *testing.T) {
	now := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	c := NewCache(CacheOptions{Now: mockNow(now)})

	req := newReq("example.com", mdns.TypeA)
	resp := newARespFor(req, 300, "1.2.3.4")
	c.Set(req, resp)

	req2 := newReq("example.com", mdns.TypeA)
	got, ok := c.Get(req2)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.Id != req2.Id {
		t.Errorf("ID not rewritten: %d vs %d", got.Id, req2.Id)
	}
	if len(got.Answer) != 1 {
		t.Fatalf("answer count: %d", len(got.Answer))
	}
}

func TestCacheExpires(t *testing.T) {
	now := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	clock := now
	c := NewCache(CacheOptions{Now: func() time.Time { return clock }})

	req := newReq("expire.example", mdns.TypeA)
	resp := newARespFor(req, 5, "1.2.3.4")
	c.Set(req, resp)

	clock = now.Add(10 * time.Second)
	if _, ok := c.Get(req); ok {
		t.Error("expected miss after TTL expiry")
	}
}

func TestCacheNXDOMAINUsesMinNegTTL(t *testing.T) {
	now := time.Now()
	clock := now
	c := NewCache(CacheOptions{
		MinNegTTL: 30 * time.Second,
		Now:       func() time.Time { return clock },
	})
	req := newReq("nope.example", mdns.TypeA)
	resp := new(mdns.Msg)
	resp.SetRcode(req, mdns.RcodeNameError)

	c.Set(req, resp)
	if _, ok := c.Get(req); !ok {
		t.Fatal("expected NXDOMAIN to be cached")
	}
	clock = now.Add(45 * time.Second)
	if _, ok := c.Get(req); ok {
		t.Error("expected NXDOMAIN to expire after MinNegTTL")
	}
}

func TestCacheSkipsServFail(t *testing.T) {
	c := NewCache(CacheOptions{})
	req := newReq("fail.example", mdns.TypeA)
	resp := new(mdns.Msg)
	resp.SetRcode(req, mdns.RcodeServerFailure)
	c.Set(req, resp)
	if _, ok := c.Get(req); ok {
		t.Error("SERVFAIL should not be cached")
	}
}

func TestCacheNilSafe(t *testing.T) {
	var c *Cache
	req := newReq("anything", mdns.TypeA)
	if _, ok := c.Get(req); ok {
		t.Error("nil cache should always miss")
	}
	c.Set(req, newARespFor(req, 60, "1.1.1.1")) // must not panic
}
