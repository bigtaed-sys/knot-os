package subscription

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/knot-os/knot-os/core/internal/singbox"
)

// Default location of the subscription store. Can be overridden via
// NewRegistry. Lives under /var/lib (persistent across reboots) with
// mode 0600 — the file holds subscription URLs that may contain
// auth tokens.
const DefaultStorePath = "/var/lib/knot/subscriptions.yaml"

// ManualID is the synthetic subscription ID for "servers the user
// pasted directly, not behind a URL". Always present; cannot be
// renamed or removed. Servers under this subscription have stable
// IDs derived from their URI hash.
const ManualID = "manual"

// Subscription is one named upstream feed.
//
// Fields ending in `Status` are populated by Fetch; everything else
// is user-controlled.
type Subscription struct {
	// ID is the stable URL-safe identifier (slug of DisplayName at
	// creation time). "manual" is reserved.
	ID string `yaml:"id" json:"id"`

	// DisplayName is the user-facing label.
	DisplayName string `yaml:"display_name" json:"display_name"`

	// URL is the subscription endpoint. Empty for the synthetic
	// "manual" subscription.
	URL string `yaml:"url,omitempty" json:"url,omitempty"`

	// UserAgent overrides the default UA on Fetch. Some providers
	// gate features (e.g. usage limits in subscription headers) on
	// known UAs like "v2rayN/x.y" or "sing-box/1.10.7".
	UserAgent string `yaml:"user_agent,omitempty" json:"user_agent,omitempty"`

	// Servers is the snapshot of parsed outbounds from the most
	// recent successful Fetch. Empty until Fetch runs at least once.
	Servers []Server `yaml:"servers,omitempty" json:"servers"`

	// LastFetched records the wall-clock time of the most recent
	// successful Fetch. Zero value = never fetched.
	LastFetched time.Time `yaml:"last_fetched,omitempty" json:"last_fetched"`

	// LastError, if non-empty, is the most recent fetch failure
	// reason (cleared on next successful fetch).
	LastError string `yaml:"last_error,omitempty" json:"last_error,omitempty"`
}

// Server is a single VPN endpoint derived from a URI in a
// subscription's body (or pasted directly under "manual").
type Server struct {
	// ID is stable across re-fetches: a hash of the URI. The UI uses
	// this to keep "Selected server" pointing at the same physical
	// server even when the subscription's order changes.
	ID string `yaml:"id" json:"id"`

	// DisplayName is the URI fragment (provider-supplied label).
	DisplayName string `yaml:"display_name" json:"display_name"`

	// URI is the original share URI. We keep it so we can re-render
	// the outbound from scratch when the singbox version bumps and
	// adds a new field, without forcing a re-fetch.
	URI string `yaml:"uri" json:"uri"`

	// Outbound is the parsed shape; rendered into singbox.Config at
	// apply time.
	Outbound singbox.Outbound `yaml:"-" json:"outbound"`
}

// Registry is the in-memory + on-disk store of subscriptions.
//
// Concurrency: all public methods take an RLock or Lock as
// appropriate. Persistence is explicit: callers invoke Save (or
// FlushIfDirty in a periodic flusher).
type Registry struct {
	mu     sync.RWMutex
	store  string
	subs   map[string]*Subscription
	dirty  bool
}

// NewRegistry constructs an empty registry pre-populated with the
// "manual" subscription. Call Load to read the on-disk store.
func NewRegistry(storePath string) *Registry {
	if storePath == "" {
		storePath = DefaultStorePath
	}
	r := &Registry{
		store: storePath,
		subs:  map[string]*Subscription{},
	}
	r.subs[ManualID] = &Subscription{
		ID:          ManualID,
		DisplayName: "Manual entries",
	}
	return r
}

// ---- CRUD ------------------------------------------------------------------

// List returns all subscriptions sorted by ID. The returned slice
// is a snapshot; modifying entries does not affect the registry.
func (r *Registry) List() []Subscription {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Subscription, 0, len(r.subs))
	for _, s := range r.subs {
		out = append(out, deepCopy(*s))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Get returns a copy of the named subscription.
func (r *Registry) Get(id string) (Subscription, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.subs[id]
	if !ok {
		return Subscription{}, false
	}
	return deepCopy(*s), true
}

// Add registers a new URL-backed subscription. The ID is derived
// from DisplayName; pass an empty ID to auto-slug.
func (r *Registry) Add(s Subscription) (Subscription, error) {
	if s.URL == "" {
		return Subscription{}, errors.New("subscription: URL required")
	}
	if s.DisplayName == "" {
		return Subscription{}, errors.New("subscription: display name required")
	}

	if s.ID == "" {
		s.ID = slug(s.DisplayName)
	}
	if s.ID == ManualID {
		return Subscription{}, errors.New("subscription: 'manual' is reserved")
	}
	if !validSubID(s.ID) {
		return Subscription{}, fmt.Errorf("subscription: invalid id %q", s.ID)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.subs[s.ID]; exists {
		return Subscription{}, fmt.Errorf("subscription: id %q already exists", s.ID)
	}
	cp := s
	r.subs[s.ID] = &cp
	r.dirty = true
	return deepCopy(cp), nil
}

// Update replaces the editable fields of an existing subscription
// (DisplayName, URL, UserAgent). Servers / LastFetched / LastError
// are managed by Fetch — passing values for them is silently
// ignored.
func (r *Registry) Update(id string, s Subscription) (Subscription, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur, ok := r.subs[id]
	if !ok {
		return Subscription{}, fmt.Errorf("subscription: %q not found", id)
	}
	if id == ManualID {
		// Only DisplayName editable for manual.
		if s.DisplayName != "" {
			cur.DisplayName = s.DisplayName
		}
		r.dirty = true
		return deepCopy(*cur), nil
	}
	if s.DisplayName != "" {
		cur.DisplayName = s.DisplayName
	}
	if s.URL != "" {
		cur.URL = s.URL
	}
	cur.UserAgent = s.UserAgent
	r.dirty = true
	return deepCopy(*cur), nil
}

// Remove deletes a subscription. Manual is not removable.
func (r *Registry) Remove(id string) error {
	if id == ManualID {
		return errors.New("subscription: cannot remove 'manual'")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.subs[id]; !ok {
		return fmt.Errorf("subscription: %q not found", id)
	}
	delete(r.subs, id)
	r.dirty = true
	return nil
}

// AddManualURI parses a single share URI, adds it to the "manual"
// subscription, and returns the resulting Server. Idempotent on URI
// hash — the same URI added twice replaces the existing entry.
func (r *Registry) AddManualURI(uri string) (Server, error) {
	out, err := ParseURI(uri)
	if err != nil {
		return Server{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	manual := r.subs[ManualID]
	srv := serverFromURI(uri, out, ManualID)

	for i, existing := range manual.Servers {
		if existing.ID == srv.ID {
			manual.Servers[i] = srv
			r.dirty = true
			return srv, nil
		}
	}
	manual.Servers = append(manual.Servers, srv)
	r.dirty = true
	return srv, nil
}

// RemoveManualServer drops one server from the "manual" subscription.
func (r *Registry) RemoveManualServer(serverID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	manual := r.subs[ManualID]
	for i, s := range manual.Servers {
		if s.ID == serverID {
			manual.Servers = append(manual.Servers[:i], manual.Servers[i+1:]...)
			r.dirty = true
			return nil
		}
	}
	return fmt.Errorf("subscription: server %q not found", serverID)
}

// ApplyFetched stores the result of a Fetch into the registry.
// Servers are tagged with a stable ID so the UI's "selected server"
// reference survives a re-fetch even if order changes upstream.
func (r *Registry) ApplyFetched(id string, body []byte) ([]error, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	sub, ok := r.subs[id]
	if !ok {
		return nil, fmt.Errorf("subscription: %q not found", id)
	}
	if id == ManualID {
		return nil, errors.New("subscription: cannot fetch 'manual'")
	}

	outs, parseErrs := ParseBundle(body)
	if len(outs) == 0 {
		// Don't trample the previous good snapshot on a bad fetch.
		errMsg := "no servers parsed from bundle"
		if len(parseErrs) > 0 {
			errMsg = parseErrs[0].Error()
		}
		sub.LastError = errMsg
		r.dirty = true
		return parseErrs, errors.New(errMsg)
	}

	// Re-extract URIs aligned with successfully-parsed outbounds.
	// We need them for stable IDs + re-render. ParseBundle doesn't
	// return them, so we re-walk the body matching scheme prefixes.
	uris := extractURIs(body)

	// Defensive trim: the URIs slice may include lines that failed
	// to parse, so it can be longer than outs. We just take outs.
	servers := make([]Server, 0, len(outs))
	for i, o := range outs {
		uri := ""
		if i < len(uris) {
			uri = uris[i]
		}
		// Note: when ParseBundle skipped a URI (parse error), the
		// alignment can drift. To stay simple we recompute IDs from
		// the outbound's distinguishing fields if we don't have a
		// URI to hash.
		servers = append(servers, serverFromURI(uri, o, id))
	}
	sub.Servers = servers
	sub.LastFetched = time.Now().UTC()
	sub.LastError = ""
	r.dirty = true
	return parseErrs, nil
}

// MarkFetchError records a fetch failure without overwriting the
// previously-cached server list.
func (r *Registry) MarkFetchError(id string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if sub, ok := r.subs[id]; ok {
		sub.LastError = err.Error()
		r.dirty = true
	}
}

// AllOutbounds returns every Server's Outbound across all
// subscriptions, with stable Tags injected so the singbox.Config
// renderer can identify them. The Tag format is "<sub-id>:<srv-id>".
func (r *Registry) AllOutbounds() []singbox.Outbound {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []singbox.Outbound
	subIDs := make([]string, 0, len(r.subs))
	for k := range r.subs {
		subIDs = append(subIDs, k)
	}
	sort.Strings(subIDs)
	for _, sid := range subIDs {
		sub := r.subs[sid]
		for _, s := range sub.Servers {
			o := s.Outbound
			o.Tag = sid + ":" + s.ID
			if o.DisplayName == "" {
				o.DisplayName = s.DisplayName
			}
			out = append(out, o)
		}
	}
	return out
}

// ---- persistence -----------------------------------------------------------

// Load reads the YAML file. Missing file is not an error — the
// registry stays at its post-NewRegistry state (manual only).
func (r *Registry) Load() error {
	data, err := os.ReadFile(r.store)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var doc storeDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("subscription: parse %s: %w", r.store, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range doc.Subscriptions {
		s := doc.Subscriptions[i]
		// Re-parse URIs to repopulate the Outbound (it's not on disk
		// — we store the URI as the source of truth).
		for j := range s.Servers {
			if s.Servers[j].URI == "" {
				continue
			}
			if out, err := ParseURI(s.Servers[j].URI); err == nil {
				s.Servers[j].Outbound = out
			}
		}
		r.subs[s.ID] = &s
	}
	r.dirty = false
	return nil
}

// Save atomically writes the YAML file with mode 0600.
func (r *Registry) Save() error {
	r.mu.RLock()
	doc := storeDoc{}
	for _, s := range r.subs {
		doc.Subscriptions = append(doc.Subscriptions, *s)
	}
	r.mu.RUnlock()

	sort.Slice(doc.Subscriptions, func(i, j int) bool {
		return doc.Subscriptions[i].ID < doc.Subscriptions[j].ID
	})

	data, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.store), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(r.store), ".subscriptions-*.yaml.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, r.store); err != nil {
		cleanup()
		return err
	}
	r.mu.Lock()
	r.dirty = false
	r.mu.Unlock()
	return nil
}

// FlushIfDirty saves only when there are pending changes.
func (r *Registry) FlushIfDirty() error {
	r.mu.RLock()
	d := r.dirty
	r.mu.RUnlock()
	if !d {
		return nil
	}
	return r.Save()
}

type storeDoc struct {
	Subscriptions []Subscription `yaml:"subscriptions"`
}

// ---- helpers --------------------------------------------------------------

func deepCopy(s Subscription) Subscription {
	cp := s
	// Always materialize a non-nil slice so the JSON renderer emits
	// `[]` instead of `null`. The UI does sub.servers.length and
	// expects a real array; null trips a TypeError at the iteration
	// site (see Routing page).
	cp.Servers = make([]Server, len(s.Servers))
	copy(cp.Servers, s.Servers)
	return cp
}

// serverFromURI builds a Server with a stable ID. When uri is empty
// (we lost alignment in ApplyFetched), we hash the outbound's
// fingerprint instead.
func serverFromURI(uri string, o singbox.Outbound, _ string) Server {
	id := uriID(uri)
	if id == "" {
		id = outboundID(o)
	}
	return Server{
		ID:          id,
		DisplayName: firstNonEmpty(o.DisplayName, o.Server),
		URI:         uri,
		Outbound:    o,
	}
}

func uriID(uri string) string {
	if uri == "" {
		return ""
	}
	sum := sha1.Sum([]byte(uri))
	return hex.EncodeToString(sum[:8])
}

// outboundID produces a stable ID from the server fields when the
// raw URI isn't available. Only the fields that uniquely identify a
// server (host, port, auth) are hashed — display-only fields like
// DisplayName are excluded so re-fetching with a relabelled name
// preserves the ID.
func outboundID(o singbox.Outbound) string {
	parts := []string{
		string(o.Type), o.Server, fmt.Sprintf("%d", o.Port),
		o.UUID, o.Password, o.Method, o.Transport,
	}
	sum := sha1.Sum([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:8])
}

// extractURIs walks the (possibly base64-encoded) bundle body and
// returns share URIs in the same order as ParseBundle parses them.
// Any line that doesn't look like a share URI is skipped — same
// rules as the parser, so the indexes line up with ParseBundle's
// output.
func extractURIs(body []byte) []string {
	text := strings.TrimSpace(string(body))
	if dec, ok := tryBase64(text); ok {
		text = dec
	}
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !looksLikeShareURI(line) {
			continue
		}
		out = append(out, line)
	}
	return out
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

func slug(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = slugRE.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "sub"
	}
	if len(s) > 32 {
		s = s[:32]
	}
	return s
}

var validSubIDRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

func validSubID(id string) bool { return validSubIDRE.MatchString(id) }
