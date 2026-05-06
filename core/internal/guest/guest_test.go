package guest

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGeneratePSKShape(t *testing.T) {
	for i := 0; i < 50; i++ {
		p, err := GeneratePSK()
		if err != nil {
			t.Fatal(err)
		}
		if len(p) != 12 {
			t.Errorf("psk length = %d, want 12 (%q)", len(p), p)
		}
		// Reduced alphabet — none of the ambiguous chars.
		for _, banned := range "0O1lI" {
			if strings.ContainsRune(p, banned) {
				t.Errorf("psk %q contains banned char %q", p, banned)
			}
		}
	}
}

func TestValidateSSID(t *testing.T) {
	good := []string{"home", "Knot-guest", "我的網絡", strings.Repeat("a", 32)}
	for _, s := range good {
		if err := ValidateSSID(s); err != nil {
			t.Errorf("ssid %q rejected: %v", s, err)
		}
	}
	bad := []string{"", strings.Repeat("a", 33), "with\x00null", "tab\there"}
	for _, s := range bad {
		if err := ValidateSSID(s); err == nil {
			t.Errorf("ssid %q should be rejected", s)
		}
	}
}

func TestWiFiQRStringEscapes(t *testing.T) {
	s := Session{SSID: "weird; net", PSK: "p:s\\k"}
	got := s.WiFiQRString()
	want := `WIFI:T:WPA;S:weird\; net;P:p\:s\\k;;`
	if got != want {
		t.Errorf("wifi-qr:\n got=%s\nwant=%s", got, want)
	}
}

func TestSessionActiveAndRemaining(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name   string
		s      Session
		want   bool
	}{
		{"empty", Session{}, false},
		{"valid until later", Session{SSID: "x", PSK: "y", ExpiresAt: now.Add(time.Hour)}, true},
		{"expired", Session{SSID: "x", PSK: "y", ExpiresAt: now.Add(-time.Minute)}, false},
		{"manual (no expiry)", Session{SSID: "x", PSK: "y"}, true},
	}
	for _, c := range cases {
		if got := c.s.Active(now); got != c.want {
			t.Errorf("%s: Active = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestRegistryCreateAndRevoke(t *testing.T) {
	store := filepath.Join(t.TempDir(), "guest.yaml")
	r, err := Open(store)
	if err != nil {
		t.Fatal(err)
	}
	if r.Current().SSID != "" {
		t.Error("fresh registry should have empty session")
	}

	s, err := r.Create("KnotNet", CreateOptions{Duration: time.Hour, ProfileID: "guest"})
	if err != nil {
		t.Fatal(err)
	}
	if s.SSID != "KnotNet-guest" {
		t.Errorf("default ssid: %q", s.SSID)
	}
	if len(s.PSK) != 12 {
		t.Errorf("psk len: %d", len(s.PSK))
	}
	if s.ExpiresAt.IsZero() || s.ExpiresAt.Before(time.Now().Add(50*time.Minute)) {
		t.Errorf("expires_at not set right: %v", s.ExpiresAt)
	}

	// Reload from disk.
	r2, err := Open(store)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Current().SSID != "KnotNet-guest" {
		t.Error("session lost on reload")
	}

	if err := r2.Revoke(); err != nil {
		t.Fatal(err)
	}
	if r2.Current().SSID != "" {
		t.Error("session not cleared")
	}
}

func TestRegistrySweepExpired(t *testing.T) {
	store := filepath.Join(t.TempDir(), "guest.yaml")
	r, _ := Open(store)
	_, _ = r.Create("X", CreateOptions{Duration: time.Minute})

	// Not expired yet.
	if r.SweepExpired(time.Now()) {
		t.Error("not yet expired but sweep returned true")
	}
	// Past expiry.
	if !r.SweepExpired(time.Now().Add(2 * time.Minute)) {
		t.Error("expired but sweep returned false")
	}
	if r.Current().SSID != "" {
		t.Error("expired session not cleared")
	}
}

func TestRegistrySweepRespectsManualSessions(t *testing.T) {
	store := filepath.Join(t.TempDir(), "guest.yaml")
	r, _ := Open(store)
	_, _ = r.Create("X", CreateOptions{Duration: 0}) // manual = no expiry
	if r.SweepExpired(time.Now().Add(365 * 24 * time.Hour)) {
		t.Error("manual session should never auto-expire")
	}
}

func TestCreateRejectsBadSSID(t *testing.T) {
	r, _ := Open(filepath.Join(t.TempDir(), "g.yaml"))
	if _, err := r.Create("", CreateOptions{SSID: strings.Repeat("a", 50)}); err == nil {
		t.Error("expected SSID-too-long rejection")
	}
}
