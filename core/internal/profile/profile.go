// Package profile holds the per-device policy model used by v0.2's
// "device profiles" feature. A profile binds together:
//
//   - A set of time windows during which the device is denied
//     internet access (parental-control style schedules).
//   - A set of DNS blocklists to apply to that device's queries
//     (the M11 ad-blocker reads this).
//
// Profiles are persisted at /etc/knot/profiles.yaml. A small set of
// built-in profiles (default, kids, guest) ship with KnotOS and are
// merged with user-defined ones at load time.
package profile

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Profile is a named bundle of policy.
type Profile struct {
	// ID is the stable URL-safe identifier.
	ID string `yaml:"id" json:"id"`
	// Name is the localized display label.
	Name string `yaml:"name" json:"name"`
	// Description is one or two lines shown in the picker.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// BlockWindows enumerate the time slots during which devices on
	// this profile are denied internet access. Empty == no schedule.
	BlockWindows []BlockWindow `yaml:"block_windows,omitempty" json:"block_windows,omitempty"`
	// DNSBlocklists names blocklists to apply; the resolver in M11
	// looks them up against the global blocklist registry. Empty ==
	// no DNS filtering (pass-through).
	DNSBlocklists []string `yaml:"dns_blocklists,omitempty" json:"dns_blocklists,omitempty"`
	// SafeSearch, when true, forces the major search engines and
	// YouTube into their restricted/safe variants for devices on
	// this profile. The resolver CNAME-rewrites queries for
	// google.*, youtube.com, bing.com and duckduckgo.com to the
	// providers' enforcement hostnames. Empty == off.
	SafeSearch bool `yaml:"safe_search,omitempty" json:"safe_search,omitempty"`
	// RouteVia, when non-empty, sends all traffic from devices on
	// this profile through the named outbound. Format mirrors
	// singbox.Outbound.Tag — usually "<sub-id>:<server-id>" for a
	// concrete server, or "auto:<sub-id>" for a urltest selector.
	// Empty == direct (no tunnel). M28+ feature.
	RouteVia string `yaml:"route_via,omitempty" json:"route_via,omitempty"`
	// RouteDomains turns RouteVia into a split tunnel: when non-empty,
	// only traffic to these domains (and their subdomains) goes
	// through the tunnel; everything else from the device stays
	// direct. Empty (with RouteVia set) == the whole device is
	// tunnelled. Ignored when RouteVia is empty/"direct".
	// Entries are domain suffixes, e.g. "youtube.com", "netflix.com".
	RouteDomains []string `yaml:"route_domains,omitempty" json:"route_domains,omitempty"`
	// Builtin marks profiles that ship with KnotOS. The API refuses
	// to delete or rename them; the user can only edit their
	// schedule and blocklists.
	Builtin bool `yaml:"builtin,omitempty" json:"builtin"`
}

// BlockWindow is a recurring weekly time range during which devices
// assigned this profile lose internet.
//
// Times are wall-clock in the device's local timezone (system tz).
// Start > End means the window crosses midnight (e.g. 22:00..07:00).
type BlockWindow struct {
	// Days lists weekdays this window applies to. 0=Sunday..6=Saturday.
	Days []int `yaml:"days" json:"days"`
	// Start is "HH:MM" 24-hour.
	Start string `yaml:"start" json:"start"`
	// End is "HH:MM" 24-hour.
	End string `yaml:"end" json:"end"`
}

// idRE constrains profile IDs to URL-path-safe characters.
var idRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
var hhmmRE = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// domainRE accepts a bare domain suffix like "youtube.com" or
// "media-amazon.com" — lowercase labels separated by dots, no
// scheme, no leading dot, no path.
var domainRE = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,}$`)

// Validate reports an error if the profile is structurally bad.
func (p Profile) Validate() error {
	if !idRE.MatchString(p.ID) {
		return fmt.Errorf("id %q is not a valid identifier (lowercase letters/digits/.-_)", p.ID)
	}
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("name is required")
	}
	for i, w := range p.BlockWindows {
		if err := w.Validate(); err != nil {
			return fmt.Errorf("block_windows[%d]: %w", i, err)
		}
	}
	for i, d := range p.RouteDomains {
		if !domainRE.MatchString(strings.ToLower(strings.TrimSpace(d))) {
			return fmt.Errorf("route_domains[%d]: %q is not a valid domain", i, d)
		}
	}
	return nil
}

// NormalizedRouteDomains returns the route-domain suffixes lowercased
// and trimmed, suitable for handing straight to the sing-box renderer.
func (p Profile) NormalizedRouteDomains() []string {
	if len(p.RouteDomains) == 0 {
		return nil
	}
	out := make([]string, 0, len(p.RouteDomains))
	for _, d := range p.RouteDomains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d != "" {
			out = append(out, d)
		}
	}
	return out
}

// Validate checks a single window's structure.
func (w BlockWindow) Validate() error {
	if len(w.Days) == 0 {
		return errors.New("at least one day must be selected")
	}
	for _, d := range w.Days {
		if d < 0 || d > 6 {
			return fmt.Errorf("day %d out of range (expected 0-6)", d)
		}
	}
	if !hhmmRE.MatchString(w.Start) {
		return fmt.Errorf("start %q must be HH:MM", w.Start)
	}
	if !hhmmRE.MatchString(w.End) {
		return fmt.Errorf("end %q must be HH:MM", w.End)
	}
	if w.Start == w.End {
		return errors.New("start and end must differ")
	}
	return nil
}

// IsActiveAt reports whether the window currently denies internet at
// the given time. Handles windows that cross midnight (start > end).
func (w BlockWindow) IsActiveAt(t time.Time) bool {
	startMin, endMin := mustParseHHMM(w.Start), mustParseHHMM(w.End)
	if startMin < 0 || endMin < 0 {
		// Validate() should have caught this; treat as inactive.
		return false
	}
	wd := int(t.Weekday())
	nowMin := t.Hour()*60 + t.Minute()

	// Windows that cross midnight: e.g. 22:00..07:00 means
	//   - Wed 23:00 active if Wed in Days
	//   - Thu 03:00 active if Wed in Days (the window started yesterday)
	if startMin <= endMin {
		// Same-day window.
		if !inDays(w.Days, wd) {
			return false
		}
		return nowMin >= startMin && nowMin < endMin
	}
	// Cross-midnight window: split into [start..1440) on Days[i] and
	// [0..end) on Days[i]+1.
	if inDays(w.Days, wd) && nowMin >= startMin {
		return true
	}
	prevDay := (wd + 6) % 7 // wd - 1 mod 7
	if inDays(w.Days, prevDay) && nowMin < endMin {
		return true
	}
	return false
}

func inDays(days []int, d int) bool {
	for _, x := range days {
		if x == d {
			return true
		}
	}
	return false
}

func mustParseHHMM(s string) int {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return -1
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil {
		return -1
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil {
		return -1
	}
	return h*60 + m
}

// IsBlockingAt reports whether the profile, taken as a whole, denies
// internet at the given time. True if any window is active.
func (p Profile) IsBlockingAt(t time.Time) bool {
	for _, w := range p.BlockWindows {
		if w.IsActiveAt(t) {
			return true
		}
	}
	return false
}

// --- Registry --------------------------------------------------------------

// Registry is the in-memory profile store. Builtin profiles are
// merged on construction; user-defined profiles are loaded from disk
// and saved back atomically.
type Registry struct {
	mu       sync.RWMutex
	profiles map[string]*Profile
	store    string
	dirty    bool
}

// NewRegistry constructs a registry rooted at storeFile (typically
// /etc/knot/profiles.yaml). Pre-populated with the built-ins.
func NewRegistry(storeFile string) *Registry {
	r := &Registry{
		profiles: make(map[string]*Profile),
		store:    storeFile,
	}
	for _, p := range builtins() {
		pp := p
		r.profiles[pp.ID] = &pp
	}
	return r
}

// builtins returns the read-only seed profiles every KnotOS device
// ships with. Anything user-customisable lives in the YAML store.
func builtins() []Profile {
	return []Profile{
		{
			ID:          "default",
			Name:        "Без ограничений",
			Description: "Свободный доступ, без блокировок.",
			Builtin:     true,
		},
		{
			ID:          "kids",
			Name:        "Дети",
			Description: "Без интернета по будням 22:00-07:00, реклама и трекеры блокируются.",
			BlockWindows: []BlockWindow{
				{
					Days:  []int{0, 1, 2, 3, 4, 5, 6},
					Start: "22:00",
					End:   "07:00",
				},
			},
			DNSBlocklists: []string{"ads", "trackers"},
			Builtin:       true,
		},
		{
			ID:          "guest",
			Name:        "Гости",
			Description: "Только реклама/трекеры блокируются. Расписание не задано.",
			DNSBlocklists: []string{"ads"},
			Builtin:     true,
		},
	}
}

// List returns a stable, ID-sorted snapshot.
func (r *Registry) List() []Profile {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Profile, 0, len(r.profiles))
	for _, p := range r.profiles {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Get fetches a single profile by ID.
func (r *Registry) Get(id string) (Profile, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.profiles[id]
	if !ok {
		return Profile{}, false
	}
	return *p, true
}

// Put inserts or updates a non-builtin profile. Built-in profiles can
// be edited in-place but their Builtin flag and ID are preserved.
func (r *Registry) Put(p Profile) error {
	if err := p.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.profiles[p.ID]; ok && existing.Builtin {
		// Editing a builtin: keep the Builtin flag set.
		p.Builtin = true
	}
	pp := p
	r.profiles[p.ID] = &pp
	r.dirty = true
	return nil
}

// Delete removes a profile. Returns an error if the ID is unknown or
// refers to a built-in.
func (r *Registry) Delete(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.profiles[id]
	if !ok {
		return fmt.Errorf("profile %q not found", id)
	}
	if p.Builtin {
		return fmt.Errorf("profile %q is built-in and cannot be deleted", id)
	}
	delete(r.profiles, id)
	r.dirty = true
	return nil
}
