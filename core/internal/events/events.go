// Package events is knotd's tiny in-process pub/sub bus. The point
// of having it as its own package is to break import cycles: lots
// of subsystems want to *publish* events (deviceregistry, network
// backend, dns), and one subsystem wants to *subscribe* to them
// (notify), but neither side should know about the other directly.
//
// Wire shape: every event implements the Event interface. The Bus
// dispatches every Publish() call to every Subscribe()d handler in
// a fresh goroutine, so a slow notifier (Telegram HTTP, webhook)
// can't block the publisher.
package events

import (
	"context"
	"sync"
	"time"
)

// Kind identifies what happened. Bot subscribers filter on this.
type Kind string

const (
	// KindWANStatus fires whenever the WAN interface flips between
	// up and down (router role only). Payload: WANStatus.
	KindWANStatus Kind = "wan_status"
	// KindDeviceJoined fires the first time a MAC is observed in
	// the device registry. Payload: DeviceJoined.
	KindDeviceJoined Kind = "device_joined"
	// KindDeviceProfileChanged fires when a device's profile
	// assignment changes via UI/API. Payload: DeviceProfileChanged.
	KindDeviceProfileChanged Kind = "device_profile_changed"
	// KindGuestSession fires on guest-network create / revoke /
	// expire. Payload: GuestSession.
	KindGuestSession Kind = "guest_session"
	// KindUpdateAvailable fires when the periodic update check
	// finds a newer release. Payload: UpdateAvailable.
	KindUpdateAvailable Kind = "update_available"
	// KindDataCap fires once per billing cycle when cellular usage
	// crosses the configured monthly cap. Payload: DataCap.
	KindDataCap Kind = "data_cap"
	// KindModemFailed fires when the cellular modem enters ModemManager's
	// "failed" state (usually a SIM problem). Payload: ModemFailed.
	KindModemFailed Kind = "modem_failed"
)

// Event is the supertype for everything on the bus. Concrete event
// types embed *one* Kind constant for dispatch + carry their own
// data.
type Event struct {
	Kind Kind
	When time.Time
	// Payload is the event-specific struct. Subscribers type-assert
	// based on Kind. Keeping it as `any` avoids one new package per
	// event type at this small scale.
	Payload any
}

// WANStatus is the payload for KindWANStatus.
type WANStatus struct {
	Up        bool
	Interface string
	IP        string // current IP (when Up); empty when Down
}

// DeviceJoined is the payload for KindDeviceJoined.
type DeviceJoined struct {
	MAC      string
	Hostname string
	IP       string
}

// DeviceProfileChanged is the payload for KindDeviceProfileChanged.
type DeviceProfileChanged struct {
	MAC          string
	Label        string
	NewProfileID string
	OldProfileID string
}

// GuestSession is the payload for KindGuestSession.
type GuestSession struct {
	Action string // "created" | "revoked" | "expired"
	SSID   string
}

// UpdateAvailable is the payload for KindUpdateAvailable.
type UpdateAvailable struct {
	CurrentVersion string
	LatestVersion  string
}

// DataCap is the payload for KindDataCap.
type DataCap struct {
	UsedBytes  uint64
	LimitBytes uint64
}

// ModemFailed is the payload for KindModemFailed.
type ModemFailed struct {
	Reason string // ModemManager state-failed-reason / hint
}

// Handler receives events. Run inside a fresh goroutine per event,
// so panics here don't crash the publisher; recover'd at dispatch.
type Handler func(ctx context.Context, ev Event)

// Bus is the dispatcher. Zero value is usable; construct with
// NewBus when you want the convenience.
type Bus struct {
	mu   sync.RWMutex
	subs []subscription
}

type subscription struct {
	id      uint64
	kinds   []Kind // empty == all
	handler Handler
}

// NewBus returns an empty Bus.
func NewBus() *Bus { return &Bus{} }

// Subscribe registers h to receive events. kinds restricts the
// dispatch — empty slice means "deliver every event". The returned
// id can be passed to Unsubscribe.
func (b *Bus) Subscribe(h Handler, kinds ...Kind) uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := uint64(len(b.subs)) + 1
	b.subs = append(b.subs, subscription{id: id, kinds: kinds, handler: h})
	return id
}

// Unsubscribe removes the subscription with the given id. Safe to
// call with an unknown id — no-op.
func (b *Bus) Unsubscribe(id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, s := range b.subs {
		if s.id == id {
			b.subs = append(b.subs[:i], b.subs[i+1:]...)
			return
		}
	}
}

// Publish dispatches an event to every matching subscriber. Each
// handler runs in a fresh goroutine; the publisher returns
// immediately. ctx is the lifetime of the publishing operation —
// handlers that outlive it should use their own contexts.
//
// If ev.When is zero, set to time.Now().
func (b *Bus) Publish(ctx context.Context, ev Event) {
	if ev.When.IsZero() {
		ev.When = time.Now()
	}
	b.mu.RLock()
	subs := make([]subscription, 0, len(b.subs))
	for _, s := range b.subs {
		if matches(s.kinds, ev.Kind) {
			subs = append(subs, s)
		}
	}
	b.mu.RUnlock()
	for _, s := range subs {
		go safeDispatch(ctx, s.handler, ev)
	}
}

func matches(filter []Kind, k Kind) bool {
	if len(filter) == 0 {
		return true
	}
	for _, f := range filter {
		if f == k {
			return true
		}
	}
	return false
}

func safeDispatch(ctx context.Context, h Handler, ev Event) {
	defer func() {
		// A panicking handler shouldn't take the daemon down. We
		// swallow here; in production the operator notices through
		// the Telegram silence (if it's the bot that crashed) or
		// systemd's restart counter.
		_ = recover()
	}()
	h(ctx, ev)
}
