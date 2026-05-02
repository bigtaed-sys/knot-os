package deviceregistry

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Registry is the in-memory store of known devices, kept in sync with
// dnsmasq's lease file (via fsnotify in real deployments) and a YAML
// store on disk (/etc/knot/devices.yaml).
//
// Concurrency: all public methods are safe to call from multiple
// goroutines. Reads use an RWMutex; writes flush back to disk on a
// debounce so we don't fsync on every lease event.
type Registry struct {
	mu       sync.RWMutex
	devices  map[string]*Device // by MAC

	storeFile string
	leaseFile string

	// dirty flags whether in-memory state has diverged from storeFile.
	// Cleared after a successful Flush.
	dirty bool

	logger *log.Logger
}

// Options configures NewRegistry.
type Options struct {
	// StoreFile is the YAML file path where user overrides
	// (DisplayName, ProfileID, FirstSeen) are persisted. Defaults
	// to /etc/knot/devices.yaml when empty.
	StoreFile string

	// LeaseFile is dnsmasq's lease file; usually
	// /var/lib/misc/dnsmasq.leases. Empty disables lease-file
	// integration (tests, dev mode).
	LeaseFile string

	Logger *log.Logger
}

// NewRegistry returns an empty Registry. Call Load to read existing
// state from StoreFile, then RefreshFromLeases to merge live lease
// state, then optionally StartLeaseWatcher to react to changes.
func NewRegistry(opts Options) *Registry {
	if opts.StoreFile == "" {
		opts.StoreFile = "/etc/knot/devices.yaml"
	}
	if opts.Logger == nil {
		opts.Logger = log.Default()
	}
	return &Registry{
		devices:   make(map[string]*Device),
		storeFile: opts.StoreFile,
		leaseFile: opts.LeaseFile,
		logger:    opts.Logger,
	}
}

// --- read paths -------------------------------------------------------------

// List returns a stable, MAC-sorted snapshot of every known device.
// Caller is free to mutate the returned slice; the underlying Device
// pointers are copies, not the registry's own.
func (r *Registry) List() []Device {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Device, 0, len(r.devices))
	for _, d := range r.devices {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MAC < out[j].MAC })
	return out
}

// Get returns a single device by MAC, or false if it isn't registered.
// MAC matching is case-insensitive on input.
func (r *Registry) Get(mac string) (Device, bool) {
	mac = normalizeMAC(mac)
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.devices[mac]
	if !ok {
		return Device{}, false
	}
	return *d, true
}

// --- write paths ------------------------------------------------------------

// Update applies a partial mutation to a known device. patch is a
// function that mutates the Device in place. Returns the updated
// Device or an error if the MAC isn't registered.
func (r *Registry) Update(mac string, patch func(*Device)) (Device, error) {
	mac = normalizeMAC(mac)
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.devices[mac]
	if !ok {
		return Device{}, fmt.Errorf("device %s not registered", mac)
	}
	patch(d)
	r.dirty = true
	return *d, nil
}

// SetDisplayName is a convenience for the most common Update.
func (r *Registry) SetDisplayName(mac, name string) (Device, error) {
	return r.Update(mac, func(d *Device) { d.DisplayName = name })
}

// SetProfileID assigns a profile (or clears it when id == "").
func (r *Registry) SetProfileID(mac, id string) (Device, error) {
	return r.Update(mac, func(d *Device) { d.ProfileID = id })
}

// Forget removes a device from the registry. Used by the UI to clean
// up stale entries that won't come back — typically duplicates from
// iOS/Android private MAC randomization, where a single physical
// phone shows up under several MACs over time.
//
// If the device's MAC reappears in a future lease event,
// RefreshFromLeases will re-create the entry from scratch (with a
// fresh FirstSeen). Callers who want the deletion to stick should
// only forget devices whose lease has already expired.
func (r *Registry) Forget(mac string) error {
	mac = normalizeMAC(mac)
	if mac == "" {
		return fmt.Errorf("invalid mac")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.devices[mac]; !ok {
		return fmt.Errorf("device %s not registered", mac)
	}
	delete(r.devices, mac)
	r.dirty = true
	return nil
}

// --- lease integration ------------------------------------------------------

// RefreshFromLeases reads the current dnsmasq lease file and merges
// every entry into the registry. New MACs become new Devices; known
// MACs get their IP / Hostname / LastSeen / LeaseExpires updated.
//
// LeaseFile == "" makes this a no-op.
func (r *Registry) RefreshFromLeases() error {
	if r.leaseFile == "" {
		return nil
	}
	entries, err := parseLeasesFile(r.leaseFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Pre-DHCP-lease state. Not an error.
			return nil
		}
		return fmt.Errorf("parse leases: %w", err)
	}
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	changed := false
	for _, e := range entries {
		d, ok := r.devices[e.MAC]
		if !ok {
			d = &Device{
				MAC:       e.MAC,
				FirstSeen: now,
			}
			r.devices[e.MAC] = d
			changed = true
		}
		if d.IP != e.IP || d.Hostname != e.Hostname {
			d.IP = e.IP
			if e.Hostname != "" {
				d.Hostname = e.Hostname
			}
			changed = true
		}
		d.LeaseExpires = e.Expires
		d.LastSeen = now
	}
	if changed {
		r.dirty = true
	}
	return nil
}

// --- persistence ------------------------------------------------------------

// Load reads the YAML store file. Missing file is not an error — the
// registry just stays at whatever state it was in (typically empty).
func (r *Registry) Load() error {
	data, err := os.ReadFile(r.storeFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var doc storeDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse %s: %w", r.storeFile, err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, d := range doc.Devices {
		mac := normalizeMAC(d.MAC)
		if mac == "" {
			continue
		}
		dd := d
		dd.MAC = mac
		r.devices[mac] = &dd
	}
	r.dirty = false
	return nil
}

// Save writes the registry's persistent fields (display name, profile
// id, first/last seen) back to disk. Atomic via a temp+rename. Live
// fields (IP, LeaseExpires) are not persisted.
func (r *Registry) Save() error {
	r.mu.RLock()
	doc := storeDoc{Devices: make([]Device, 0, len(r.devices))}
	for _, d := range r.devices {
		doc.Devices = append(doc.Devices, Device{
			MAC:         d.MAC,
			Hostname:    d.Hostname,
			DisplayName: d.DisplayName,
			ProfileID:   d.ProfileID,
			FirstSeen:   d.FirstSeen,
			LastSeen:    d.LastSeen,
		})
	}
	r.mu.RUnlock()
	sort.Slice(doc.Devices, func(i, j int) bool { return doc.Devices[i].MAC < doc.Devices[j].MAC })

	data, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	dir := filepath.Dir(r.storeFile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".devices-*.yaml.tmp")
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
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, r.storeFile); err != nil {
		cleanup()
		return err
	}
	r.mu.Lock()
	r.dirty = false
	r.mu.Unlock()
	return nil
}

// FlushIfDirty saves to disk only if there are pending changes.
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
	Devices []Device `yaml:"devices"`
}

// normalizeMAC lower-cases and validates a MAC. Returns empty string
// for clearly malformed input.
func normalizeMAC(s string) string {
	s = stripWhitespace(s)
	s = lower(s)
	// minimal sanity: 17 chars with colons, OR 12 chars no separators.
	if len(s) == 17 || len(s) == 12 {
		return s
	}
	return ""
}

func stripWhitespace(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			continue
		}
		b = append(b, c)
	}
	return string(b)
}

func lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
