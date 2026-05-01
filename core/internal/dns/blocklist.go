// Package dns implements KnotOS's DNS resolver: a lightweight forwarder
// with per-device-profile blocklist support, used by the ad-block /
// parental-controls feature in v0.2.
//
// Design:
//   - One UDP/TCP listener on the LAN gateway IP, port 53.
//   - Each query is matched to the source's device profile (via the
//     deviceregistry lease table) to figure out which blocklists apply.
//   - If the queried name (or any parent domain) is on a blocklist
//     active for that profile, return NXDOMAIN.
//   - Otherwise forward to a configured upstream (1.1.1.1 / 8.8.8.8 by
//     default) and cache the result for its TTL.
package dns

import (
	"strings"
	"sync"
)

// Blocklist is a set of domain names (lowercased, no trailing dot)
// that should be NXDOMAIN'd. Subdomains of any listed domain are
// also blocked: blocklist {"example.com"} blocks "ads.example.com".
//
// Implementation: a plain hash set is fast enough for ~200k entries
// (~10 MB on Zero 2W) and gives us O(1) parent-walk membership tests.
// A bloom filter would save memory but adds false-positive risk we
// don't want for a DNS layer.
type Blocklist struct {
	name    string
	domains map[string]struct{}
}

// NewBlocklist returns an empty blocklist with the given identifier.
func NewBlocklist(name string) *Blocklist {
	return &Blocklist{name: name, domains: make(map[string]struct{})}
}

// Name returns the blocklist's identifier (e.g. "ads", "trackers").
func (b *Blocklist) Name() string { return b.name }

// Size returns the number of distinct domains.
func (b *Blocklist) Size() int { return len(b.domains) }

// Add inserts a domain. Lowercased, trailing dots stripped. Duplicates
// are silently ignored.
func (b *Blocklist) Add(domain string) {
	domain = normalizeDomain(domain)
	if domain == "" {
		return
	}
	b.domains[domain] = struct{}{}
}

// Contains reports whether the queried name (or any parent) is on
// the blocklist. Walks up the dotted name from most-specific to
// least: "ads.tracking.example.com" tests in turn
//
//	ads.tracking.example.com
//	    tracking.example.com
//	             example.com
//	                 com
func (b *Blocklist) Contains(qname string) bool {
	qname = normalizeDomain(qname)
	for qname != "" {
		if _, ok := b.domains[qname]; ok {
			return true
		}
		i := strings.IndexByte(qname, '.')
		if i < 0 {
			return false
		}
		qname = qname[i+1:]
	}
	return false
}

// normalizeDomain lower-cases and trims a domain so equality works.
func normalizeDomain(s string) string {
	s = strings.TrimSuffix(s, ".")
	return strings.ToLower(strings.TrimSpace(s))
}

// --- Registry ---------------------------------------------------------------

// Registry is a thread-safe map of blocklist name -> *Blocklist. The
// resolver looks up the blocklists named in a device's profile and
// asks each whether to block the query.
type Registry struct {
	mu    sync.RWMutex
	lists map[string]*Blocklist
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{lists: make(map[string]*Blocklist)}
}

// Set replaces (or inserts) a blocklist by name. Used by the
// downloader after a successful refresh.
func (r *Registry) Set(name string, list *Blocklist) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lists[name] = list
}

// Get fetches a blocklist by name.
func (r *Registry) Get(name string) (*Blocklist, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	l, ok := r.lists[name]
	return l, ok
}

// AnyContains returns true if any of the named blocklists contains
// qname. Names that don't exist in the registry are silently skipped.
func (r *Registry) AnyContains(names []string, qname string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, name := range names {
		l, ok := r.lists[name]
		if !ok {
			continue
		}
		if l.Contains(qname) {
			return true
		}
	}
	return false
}

// Sizes returns a snapshot of {name -> count} for stats display.
func (r *Registry) Sizes() map[string]int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]int, len(r.lists))
	for n, l := range r.lists {
		out[n] = l.Size()
	}
	return out
}
