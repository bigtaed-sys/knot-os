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
	arpFile   string

	// quarantine, when true, denies internet to any device that isn't
	// Approved. A network-wide access-control switch. Persisted in the
	// store doc.
	quarantine bool

	// blockLanding, when true, makes blocked devices (paused, awaiting
	// approval, or schedule-blocked) land on an explanatory page: their
	// DNS is captive-redirected to the router, which serves a "blocked"
	// / "awaiting approval" page that any site triggers. Persisted.
	blockLanding bool

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

	// ARPFile is /proc/net/arp on Linux. Empty disables ARP-based
	// liveness (tests, dev mode); Online() then falls back to
	// "lease valid".
	ARPFile string

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
		arpFile:   opts.ARPFile,
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

// MACForIP looks up which device currently holds the given IP, by
// scanning the live-state IP fields. Used by the bandwidth sampler
// to map conntrack source addresses back to a stable MAC identity.
//
// Returns ("", false) if no device with that IP is known. O(N) over
// the device list — fine at LAN scale (10-50 devices).
func (r *Registry) MACForIP(ip string) (string, bool) {
	if ip == "" {
		return "", false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for mac, d := range r.devices {
		if d.IP == ip {
			return mac, true
		}
	}
	return "", false
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

// Pause blocks the device's internet until `until`. Use
// PauseIndefinite for a no-timer pause.
func (r *Registry) Pause(mac string, until time.Time) (Device, error) {
	return r.Update(mac, func(d *Device) { d.PauseUntil = until })
}

// Resume lifts a manual pause.
func (r *Registry) Resume(mac string) (Device, error) {
	return r.Update(mac, func(d *Device) { d.PauseUntil = time.Time{} })
}

// Approve marks the device as allowed under quarantine.
func (r *Registry) Approve(mac string) (Device, error) {
	return r.Update(mac, func(d *Device) { d.Approved = true })
}

// Quarantine reports whether new-device quarantine is on.
func (r *Registry) Quarantine() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.quarantine
}

// SetQuarantine toggles new-device quarantine. Turning it ON approves
// every device currently known, so the switch only affects devices
// that appear afterwards — flipping it on never strands the household.
func (r *Registry) SetQuarantine(on bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.quarantine = on
	if on {
		for _, d := range r.devices {
			d.Approved = true
		}
	}
	r.dirty = true
}

// BlockLanding reports whether the blocked-device landing page is on.
func (r *Registry) BlockLanding() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.blockLanding
}

// SetBlockLanding toggles the blocked-device landing page.
func (r *Registry) SetBlockLanding(on bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.blockLanding = on
	r.dirty = true
}

// Reset wipes the entire registry — both the in-memory map and
// the on-disk YAML store. Used at setup-completion time: every
// device the registry saw before the wizard finished was a
// transient client that briefly joined `KnotOS-setup-XXXX` to
// hit the captive portal. Most modern phones use a fresh
// randomized MAC for that ephemeral SSID and another for the
// final broadcast SSID, so those setup-time entries are stale
// duplicates by definition.
//
// On-disk failure is non-fatal: the in-memory state is wiped
// regardless, the next periodic flush re-creates a clean file,
// and the user simply doesn't see ghost devices in the UI.
func (r *Registry) Reset() error {
	r.mu.Lock()
	r.devices = make(map[string]*Device)
	r.dirty = true
	r.mu.Unlock()
	if err := os.Remove(r.storeFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", r.storeFile, err)
	}
	return nil
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
	r.quarantine = doc.QuarantineNewDevices
	r.blockLanding = doc.BlockLandingPage
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
	doc := storeDoc{QuarantineNewDevices: r.quarantine, BlockLandingPage: r.blockLanding, Devices: make([]Device, 0, len(r.devices))}
	for _, d := range r.devices {
		doc.Devices = append(doc.Devices, Device{
			MAC:         d.MAC,
			Hostname:    d.Hostname,
			DisplayName: d.DisplayName,
			ProfileID:   d.ProfileID,
			FirstSeen:   d.FirstSeen,
			LastSeen:    d.LastSeen,
			PauseUntil:  d.PauseUntil,
			Approved:    d.Approved,
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
	QuarantineNewDevices bool     `yaml:"quarantine_new_devices,omitempty"`
	BlockLandingPage     bool     `yaml:"block_landing_page,omitempty"`
	Devices              []Device `yaml:"devices"`
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
