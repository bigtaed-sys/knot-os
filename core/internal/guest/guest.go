// Package guest owns time-limited guest Wi-Fi sessions: a separate
// SSID + auto-generated PSK that come up on a multi-BSSID hostapd
// instance and disappear automatically when the user-set timer
// expires.
//
// Design choices:
//
//   - Single active session at a time. Multiple parallel guest
//     networks would mean multiple hostapd BSS sections per phy
//     and multiple SSIDs in the airwaves; for v0.4 the UX win
//     ("scan QR, you're in") doesn't need that. Future versions
//     can split this into a real registry.
//   - The session lives in /etc/knot/guest.yaml so a knotd restart
//     doesn't accidentally kill an active guest. The expiry watcher
//     re-checks on every tick whether the session has aged out.
//   - PSK is 12 chars: a..z A..Z 0..9 with `0`, `O`, `1`, `l`, `I`
//     stripped. ~71 bits of entropy is plenty for a 4-hour window
//     and the reduced alphabet means it can be read off a phone
//     screen by a guest without retyping ambiguous characters.
package guest

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Session is one currently-active guest network.
type Session struct {
	// SSID broadcast on the guest BSS. Defaults to
	// "<host>-guest" but the user can override.
	SSID string `yaml:"ssid" json:"ssid"`
	// PSK is the auto-generated WPA2 passphrase. 12 chars,
	// reduced alphabet (no ambiguous 0/O/1/l/I).
	PSK string `yaml:"psk" json:"psk"`
	// CreatedAt is when the user clicked "Create".
	CreatedAt time.Time `yaml:"created_at" json:"created_at"`
	// ExpiresAt is when the watcher will tear the BSS down.
	// Zero means "no expiry" — only used when the user explicitly
	// picked the "until I revoke" option.
	ExpiresAt time.Time `yaml:"expires_at" json:"expires_at"`
	// ProfileID is the per-device profile applied to anything
	// connecting via this guest BSS — typically a built-in
	// "guest" profile that turns ad-block on and DNS through
	// knotd's resolver. Empty = no profile.
	ProfileID string `yaml:"profile_id,omitempty" json:"profile_id,omitempty"`
}

// Active reports whether the session is currently in force at t.
func (s Session) Active(t time.Time) bool {
	if s.SSID == "" || s.PSK == "" {
		return false
	}
	if s.ExpiresAt.IsZero() {
		// Manual-revoke session: stays up until the user kills it.
		return true
	}
	return t.Before(s.ExpiresAt)
}

// Remaining returns the time left before the session expires.
// Returns 0 when ExpiresAt is in the past, math.MaxInt64
// equivalent (huge duration) when ExpiresAt is zero.
func (s Session) Remaining(t time.Time) time.Duration {
	if s.ExpiresAt.IsZero() {
		return 365 * 24 * time.Hour // sentinel large value
	}
	d := s.ExpiresAt.Sub(t)
	if d < 0 {
		return 0
	}
	return d
}

// WiFiQRString produces the standard `WIFI:T:WPA;S:<ssid>;P:<psk>;;`
// payload that iOS Camera, Android camera apps, and most QR readers
// recognise as "join this Wi-Fi". Special chars in SSID/PSK are
// escaped per the de-facto standard.
func (s Session) WiFiQRString() string {
	return fmt.Sprintf("WIFI:T:WPA;S:%s;P:%s;;",
		escapeWiFiField(s.SSID), escapeWiFiField(s.PSK))
}

// escapeWiFiField escapes the few characters the Wi-Fi QR format
// reserves: `\`, `;`, `,`, `:`, `"`. Everything else passes through.
// Our generated PSKs avoid all of these by construction; the SSID
// could be user-typed, so escape there matters.
func escapeWiFiField(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 4)
	for _, r := range s {
		switch r {
		case '\\', ';', ',', ':', '"':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Registry persists the currently-active session.
type Registry struct {
	mu        sync.RWMutex
	current   Session
	storePath string
}

// Open loads or creates the registry. Missing file = no active
// session, which is the normal state on a freshly-booted device.
func Open(path string) (*Registry, error) {
	r := &Registry{storePath: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, fmt.Errorf("guest: read %s: %w", path, err)
	}
	var s Session
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("guest: parse %s: %w", path, err)
	}
	r.current = s
	return r, nil
}

// Current returns a snapshot of the active session (or zero if
// none). Use Session.Active to decide whether to render.
func (r *Registry) Current() Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.current
}

// CreateOptions covers the user-supplied knobs.
type CreateOptions struct {
	// SSID, when empty, defaults to baseSSID + "-guest" (or just
	// "knot-guest" if baseSSID is empty too).
	SSID string
	// Duration is how long the network stays up. Zero means "until
	// revoked" — the session never expires automatically.
	Duration time.Duration
	// ProfileID is the built-in guest profile to apply. Caller is
	// responsible for ensuring it exists in the profile registry;
	// the apply path falls back to "no profile" if it's missing.
	ProfileID string
}

// Create generates a fresh PSK, replaces any existing session,
// persists, and returns the new session.
func (r *Registry) Create(baseSSID string, opts CreateOptions) (Session, error) {
	psk, err := GeneratePSK()
	if err != nil {
		return Session{}, err
	}
	ssid := strings.TrimSpace(opts.SSID)
	if ssid == "" {
		if baseSSID == "" {
			ssid = "knot-guest"
		} else {
			ssid = baseSSID + "-guest"
		}
	}
	if err := ValidateSSID(ssid); err != nil {
		return Session{}, err
	}

	now := time.Now()
	s := Session{
		SSID:      ssid,
		PSK:       psk,
		CreatedAt: now,
		ProfileID: opts.ProfileID,
	}
	if opts.Duration > 0 {
		s.ExpiresAt = now.Add(opts.Duration)
	}
	r.mu.Lock()
	r.current = s
	err = r.persistLocked()
	r.mu.Unlock()
	if err != nil {
		return Session{}, err
	}
	return s, nil
}

// Revoke clears the active session and persists the empty state.
// Idempotent — calling on an already-empty registry is fine.
func (r *Registry) Revoke() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.current = Session{}
	return r.persistLocked()
}

// SweepExpired returns true and clears the session if the current
// one has aged past ExpiresAt. The apply hook is then responsible
// for tearing down the BSS. Caller is the watcher goroutine.
func (r *Registry) SweepExpired(now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current.SSID == "" {
		return false
	}
	if r.current.ExpiresAt.IsZero() {
		// Manual-revoke session, never auto-expires.
		return false
	}
	if now.Before(r.current.ExpiresAt) {
		return false
	}
	r.current = Session{}
	_ = r.persistLocked() // best-effort
	return true
}

func (r *Registry) persistLocked() error {
	if r.storePath == "" {
		return nil
	}
	dir := filepath.Dir(r.storePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(&r.current)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".guest-*.tmp")
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
	return os.Rename(tmpName, r.storePath)
}

// pskAlphabet is letters+digits minus the visually-ambiguous
// 0 / O / 1 / l / I. Cuts the entropy slightly (52 chars ≈ 5.7
// bits each, 12 chars ≈ 68 bits) — still well above what a
// dictionary attack on a 4-hour-window WPA2 handshake can chew.
const pskAlphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// GeneratePSK returns a fresh 12-char passphrase.
func GeneratePSK() (string, error) {
	const n = 12
	out := make([]byte, n)
	max := big.NewInt(int64(len(pskAlphabet)))
	for i := range out {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = pskAlphabet[idx.Int64()]
	}
	return string(out), nil
}

// ValidateSSID is permissive: 1..32 chars (the 802.11 limit), no
// embedded NULs, no characters that confuse hostapd's parser.
func ValidateSSID(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return errors.New("ssid: empty")
	}
	if len(s) > 32 {
		return fmt.Errorf("ssid: %d chars > 32 (802.11 limit)", len(s))
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("ssid: control character %#x not allowed", r)
		}
	}
	return nil
}
