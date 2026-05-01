package deviceregistry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseLeases(t *testing.T) {
	sample := `1746140000 dc:a6:32:11:22:33 192.168.42.150 my-phone *
1746140100 aa:bb:cc:dd:ee:ff 192.168.42.151 * *
0          11:22:33:44:55:66 192.168.42.10  static-host *
`
	entries, err := parseLeases(strings.NewReader(sample))
	if err != nil {
		t.Fatalf("parseLeases: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].MAC != "dc:a6:32:11:22:33" {
		t.Errorf("MAC: want dc:..., got %q", entries[0].MAC)
	}
	if entries[0].Hostname != "my-phone" {
		t.Errorf("Hostname: want my-phone, got %q", entries[0].Hostname)
	}
	if entries[1].Hostname != "" {
		t.Errorf("expected empty hostname for *, got %q", entries[1].Hostname)
	}
	// Static lease (expiry=0) gets pushed far into the future so Online() works.
	if entries[2].Expires.Before(time.Now()) {
		t.Errorf("static lease should treat as far-future, got %v", entries[2].Expires)
	}
}

func TestParseLeasesIgnoresJunk(t *testing.T) {
	junk := `# comment
duid 00:01:00:01:...
not-a-real-line
1746140000 incomplete
`
	entries, err := parseLeases(strings.NewReader(junk))
	if err != nil {
		t.Fatalf("parseLeases: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries from junk, got %d: %+v", len(entries), entries)
	}
}

func TestRegistrySaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "devices.yaml")

	r := NewRegistry(Options{StoreFile: store})
	now := time.Now().Round(time.Second)

	// Inject a device manually as if discovered.
	r.devices["dc:a6:32:11:22:33"] = &Device{
		MAC:         "dc:a6:32:11:22:33",
		Hostname:    "my-phone",
		DisplayName: "Phone (Anna)",
		ProfileID:   "kids",
		FirstSeen:   now,
		LastSeen:    now,
		// Live fields - should not survive save/load.
		IP:           "192.168.42.150",
		LeaseExpires: now.Add(time.Hour),
	}
	r.dirty = true

	if err := r.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	r2 := NewRegistry(Options{StoreFile: store})
	if err := r2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	d, ok := r2.Get("dc:a6:32:11:22:33")
	if !ok {
		t.Fatal("device not loaded")
	}
	if d.DisplayName != "Phone (Anna)" {
		t.Errorf("DisplayName: %q", d.DisplayName)
	}
	if d.ProfileID != "kids" {
		t.Errorf("ProfileID: %q", d.ProfileID)
	}
	if d.IP != "" {
		t.Errorf("IP should not persist, got %q", d.IP)
	}
	if !d.LeaseExpires.IsZero() {
		t.Errorf("LeaseExpires should not persist, got %v", d.LeaseExpires)
	}
}

func TestRefreshFromLeasesMergesAndCreates(t *testing.T) {
	dir := t.TempDir()
	leaseFile := filepath.Join(dir, "dnsmasq.leases")
	if err := os.WriteFile(leaseFile, []byte(
		"1746140000 dc:a6:32:11:22:33 192.168.42.150 my-phone *\n"+
			"1746140100 aa:bb:cc:dd:ee:ff 192.168.42.151 laptop *\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(Options{LeaseFile: leaseFile})
	if err := r.RefreshFromLeases(); err != nil {
		t.Fatalf("RefreshFromLeases: %v", err)
	}
	if got := len(r.List()); got != 2 {
		t.Fatalf("want 2 devices, got %d", got)
	}

	// Replace one lease, the existing device should keep its identity.
	if err := os.WriteFile(leaseFile, []byte(
		"1746150000 dc:a6:32:11:22:33 192.168.42.160 my-phone *\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.RefreshFromLeases(); err != nil {
		t.Fatal(err)
	}
	d, ok := r.Get("dc:a6:32:11:22:33")
	if !ok {
		t.Fatal("phone vanished")
	}
	if d.IP != "192.168.42.160" {
		t.Errorf("IP not updated: %q", d.IP)
	}
	// The laptop is still in the registry even though it isn't in the
	// new lease file - we treat lease absence as "not currently leased",
	// not "forget the device".
	if _, ok := r.Get("aa:bb:cc:dd:ee:ff"); !ok {
		t.Error("laptop should remain registered")
	}
}

func TestSetDisplayNameAndProfile(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "devices.yaml")
	r := NewRegistry(Options{StoreFile: store})
	r.devices["dc:a6:32:11:22:33"] = &Device{
		MAC: "dc:a6:32:11:22:33", FirstSeen: time.Now(),
	}
	if _, err := r.SetDisplayName("DC:A6:32:11:22:33", "Phone"); err != nil {
		t.Fatalf("SetDisplayName: %v", err)
	}
	d, _ := r.Get("dc:a6:32:11:22:33")
	if d.DisplayName != "Phone" {
		t.Errorf("DisplayName: %q", d.DisplayName)
	}
	if _, err := r.SetProfileID("dc:a6:32:11:22:33", "kids"); err != nil {
		t.Fatalf("SetProfileID: %v", err)
	}
	d, _ = r.Get("dc:a6:32:11:22:33")
	if d.ProfileID != "kids" {
		t.Errorf("ProfileID: %q", d.ProfileID)
	}
	// Unknown MAC errors out.
	if _, err := r.SetDisplayName("ff:ff:ff:ff:ff:ff", "Ghost"); err == nil {
		t.Error("expected error for unknown MAC")
	}
}

func TestLabelFallbacks(t *testing.T) {
	cases := []struct {
		d    Device
		want string
	}{
		{Device{DisplayName: "Anna's Phone"}, "Anna's Phone"},
		{Device{Hostname: "iPhone"}, "iPhone"},
		{Device{Hostname: "*"}, "Unknown device"},
		{Device{MAC: "dc:a6:32:11:22:33"}, "Device 2233"},
		{Device{}, "Unknown device"},
	}
	for _, c := range cases {
		got := c.d.Label()
		if got != c.want {
			t.Errorf("Label(%+v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestOnlineByLeaseExpiry(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		d    Device
		want bool
	}{
		{"current lease", Device{LeaseExpires: now.Add(time.Hour)}, true},
		{"expired", Device{LeaseExpires: now.Add(-time.Hour)}, false},
		{"no lease", Device{}, false},
	}
	for _, c := range cases {
		if got := c.d.Online(now); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
