// Package scheduler enforces profile schedules. Every tick (default
// 30s) it walks every known device, asks "is this device's profile
// currently in a block window?", and updates a single nftables
// ether-set so the kernel drops the right packets.
//
// Errors talking to nftables are logged but never fatal — KnotOS
// keeps serving requests; only the schedule enforcement is missed.
package scheduler

import (
	"context"
	"log"
	"sort"
	"strings"
	"sync"
	"time"
)

// Tick is the default cadence. Half a minute is precise enough for
// "no internet after 22:00" UX and gentle on SD-card writes.
const Tick = 30 * time.Second

// DeviceProvider is the slice of the device registry the scheduler
// needs. Decoupled via an interface so we don't depend on the full
// deviceregistry package — easier to mock in tests too.
type DeviceProvider interface {
	List() []Device
}

// ProfileLookup resolves a profile ID to its policy. Same decoupling.
type ProfileLookup interface {
	IsBlockingAt(profileID string, t time.Time) bool
}

// MACSetUpdater is the side-effect target. The Linux backend
// implements it via nftables; tests use a recording fake.
type MACSetUpdater interface {
	// UpdateBlockedMACs replaces the kernel's block-set with the
	// given list. Implementations must be idempotent — the scheduler
	// calls this every tick whether or not anything changed.
	UpdateBlockedMACs(macs []string) error
}

// Device is the minimum the scheduler needs to know.
type Device struct {
	MAC       string
	ProfileID string
	// PauseUntil, when in the future, blocks the device regardless of
	// its profile schedule (the manual "pause" control).
	PauseUntil time.Time
	// Approved gates the device under quarantine mode.
	Approved bool
}

// Scheduler is the main loop.
type Scheduler struct {
	devices    DeviceProvider
	profiles   ProfileLookup
	updater    MACSetUpdater
	quarantine func() bool // network-wide new-device quarantine switch

	tick   time.Duration
	now    func() time.Time // injectable for tests
	logger *log.Logger

	mu      sync.Mutex
	lastSet []string // last set we wrote, for dedup logging
}

// Options configures NewScheduler.
type Options struct {
	Devices  DeviceProvider
	Profiles ProfileLookup
	Updater  MACSetUpdater
	// Quarantine reports whether new-device quarantine is on. nil =
	// always off.
	Quarantine func() bool
	Tick       time.Duration // 0 = default 30s
	Now        func() time.Time
	Logger     *log.Logger
}

// New constructs a Scheduler. Run starts the loop.
func New(opts Options) *Scheduler {
	if opts.Tick == 0 {
		opts.Tick = Tick
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = log.Default()
	}
	if opts.Quarantine == nil {
		opts.Quarantine = func() bool { return false }
	}
	return &Scheduler{
		devices:    opts.Devices,
		profiles:   opts.Profiles,
		updater:    opts.Updater,
		quarantine: opts.Quarantine,
		tick:       opts.Tick,
		now:        opts.Now,
		logger:     opts.Logger,
	}
}

// Run blocks the calling goroutine, ticking every Tick until ctx is
// cancelled. Suitable for `go scheduler.Run(ctx)`.
func (s *Scheduler) Run(ctx context.Context) {
	// Run an immediate tick so policies kick in without waiting one
	// full period after startup.
	s.RunOnce()
	t := time.NewTicker(s.tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.RunOnce()
		}
	}
}

// RunOnce computes the current block-set and applies it. Exposed for
// tests and for "force re-evaluate" callers (e.g. a profile or device
// change pushes an immediate tick instead of waiting up to 30s).
func (s *Scheduler) RunOnce() {
	now := s.now()
	quarantine := s.quarantine()
	all := s.devices.List()
	blocked := make([]string, 0, len(all))
	for _, d := range all {
		// A device is denied the internet if any of:
		//   - it's manually paused (timer still running),
		//   - quarantine is on and it hasn't been approved,
		//   - its profile's schedule is in a block window now.
		switch {
		case !d.PauseUntil.IsZero() && d.PauseUntil.After(now):
			blocked = append(blocked, d.MAC)
		case quarantine && !d.Approved:
			blocked = append(blocked, d.MAC)
		case d.ProfileID != "" && s.profiles.IsBlockingAt(d.ProfileID, now):
			blocked = append(blocked, d.MAC)
		}
	}
	sort.Strings(blocked)

	s.mu.Lock()
	changed := !sameStrings(blocked, s.lastSet)
	s.lastSet = append(s.lastSet[:0], blocked...)
	s.mu.Unlock()

	if err := s.updater.UpdateBlockedMACs(blocked); err != nil {
		s.logger.Printf("scheduler: update block-set: %v", err)
		return
	}
	if changed {
		s.logger.Printf("scheduler: block-set updated, %d MAC(s) blocked: %s",
			len(blocked), strings.Join(blocked, ","))
	}
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
