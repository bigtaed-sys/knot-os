package deviceregistry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sampleARP = `IP address       HW type     Flags       HW address            Mask     Device
192.168.42.55    0x1         0x2         dc:a6:32:11:22:33     *        wlan0
192.168.42.99    0x1         0x0         00:00:00:00:00:00     *        wlan0
192.168.42.100   0x1         0x6         aa:bb:cc:dd:ee:ff     *        wlan0
`

func TestParseARPSkipsHeaderAndIncomplete(t *testing.T) {
	got, err := parseARP(strings.NewReader(sampleARP))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3", len(got))
	}
	if !got[0].Complete() || got[0].MAC != "dc:a6:32:11:22:33" {
		t.Errorf("entry[0] = %+v", got[0])
	}
	if got[1].Complete() {
		t.Errorf("entry[1] flags=%#x should be incomplete", got[1].Flags)
	}
	if !got[2].Complete() {
		t.Errorf("entry[2] flags=%#x should be complete (perm bit + reachable)", got[2].Flags)
	}
}

func TestRefreshFromARPStampsOnlyKnownMACs(t *testing.T) {
	dir := t.TempDir()
	arpPath := filepath.Join(dir, "arp")
	if err := os.WriteFile(arpPath, []byte(sampleARP), 0o644); err != nil {
		t.Fatal(err)
	}

	r := NewRegistry(Options{
		StoreFile: filepath.Join(dir, "store.yaml"),
		ARPFile:   arpPath,
	})
	// Seed only one of the MACs from the ARP file. The other should
	// be ignored (we don't auto-create from ARP — DHCP is the
	// authoritative source of "this device exists").
	r.devices["dc:a6:32:11:22:33"] = &Device{
		MAC:          "dc:a6:32:11:22:33",
		LeaseExpires: time.Now().Add(time.Hour),
	}

	before := time.Now()
	if err := r.RefreshFromARP(); err != nil {
		t.Fatal(err)
	}

	d, ok := r.Get("dc:a6:32:11:22:33")
	if !ok {
		t.Fatal("seeded device disappeared")
	}
	if d.LastARPSeen.Before(before) {
		t.Errorf("LastARPSeen not stamped: %v", d.LastARPSeen)
	}
	// Unknown MAC must not have been added.
	if _, ok := r.Get("aa:bb:cc:dd:ee:ff"); ok {
		t.Error("ARP-only MAC should not be auto-registered")
	}
}

func TestOnlineRespectsARPLiveness(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	leaseValid := now.Add(time.Hour)

	cases := []struct {
		name   string
		device Device
		want   bool
	}{
		{
			name:   "no ARP signal, lease valid → online (boot grace)",
			device: Device{LeaseExpires: leaseValid},
			want:   true,
		},
		{
			name:   "lease expired → offline regardless",
			device: Device{LeaseExpires: now.Add(-time.Minute), LastARPSeen: now},
			want:   false,
		},
		{
			name:   "ARP recent, lease valid → online",
			device: Device{LeaseExpires: leaseValid, LastARPSeen: now.Add(-30 * time.Second)},
			want:   true,
		},
		{
			name:   "ARP stale (10 min ago), lease valid → offline",
			device: Device{LeaseExpires: leaseValid, LastARPSeen: now.Add(-10 * time.Minute)},
			want:   false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.device.Online(now); got != c.want {
				t.Errorf("Online()=%v, want %v", got, c.want)
			}
		})
	}
}

func TestStaleAfter30Days(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	old := now.Add(-40 * 24 * time.Hour)
	recent := now.Add(-7 * 24 * time.Hour)

	if !(Device{LastSeen: old}.Stale(now)) {
		t.Error("device last seen 40 days ago should be stale")
	}
	if (Device{LastSeen: recent}.Stale(now)) {
		t.Error("device last seen 7 days ago should not be stale")
	}
	// Online devices are never stale.
	online := Device{
		LeaseExpires: now.Add(time.Hour),
		LastARPSeen:  now,
		LastSeen:     old, // even with old LastSeen
	}
	if online.Stale(now) {
		t.Error("online device must not be marked stale")
	}
}
