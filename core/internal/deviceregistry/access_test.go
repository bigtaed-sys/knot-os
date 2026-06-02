package deviceregistry

import (
	"path/filepath"
	"testing"
	"time"
)

func TestIsRandomizedMAC(t *testing.T) {
	cases := map[string]bool{
		"dc:a6:32:11:22:33": false, // Raspberry Pi OUI — globally unique
		"a2:bb:cc:dd:ee:ff": true,  // 0xa2 has the locally-administered bit
		"06:11:22:33:44:55": true,  // 0x06
		"00:11:22:33:44:55": false,
		"":                  false,
		"b8:27:eb:00:00:01": false, // another real Pi OUI
		"f2:00:00:00:00:00": true,
	}
	for mac, want := range cases {
		if got := IsRandomizedMAC(mac); got != want {
			t.Errorf("IsRandomizedMAC(%q)=%v, want %v", mac, got, want)
		}
	}
}

func newReg(t *testing.T) *Registry {
	return NewRegistry(Options{StoreFile: filepath.Join(t.TempDir(), "d.yaml")})
}

func TestCarryForwardRotatedMAC(t *testing.T) {
	now := time.Now()
	r := newReg(t)
	// Old entry: a rotated-away randomized MAC, offline (no lease),
	// with the user's customizations.
	old := &Device{
		MAC: "a2:11:11:11:11:11", Hostname: "Vanyas-iPhone",
		DisplayName: "Vanya", ProfileID: "kids", Approved: true,
		FirstSeen: now.Add(-72 * time.Hour),
	}
	r.devices[old.MAC] = old
	// New entry: same phone under a fresh randomized MAC + same hostname.
	newDev := &Device{MAC: "a6:22:22:22:22:22", FirstSeen: now}
	r.devices[newDev.MAC] = newDev

	r.carryForwardRotatedLocked(newDev, "Vanyas-iPhone", now)

	if newDev.DisplayName != "Vanya" || newDev.ProfileID != "kids" || !newDev.Approved {
		t.Errorf("identity not carried forward: %+v", newDev)
	}
	if !newDev.FirstSeen.Equal(old.FirstSeen) {
		t.Errorf("FirstSeen not inherited: %v", newDev.FirstSeen)
	}
	if _, ok := r.devices[old.MAC]; ok {
		t.Error("old ghost entry should have been removed")
	}
}

func TestCarryForwardSkipsWhenOldStillOnline(t *testing.T) {
	now := time.Now()
	r := newReg(t)
	// Same hostname but the old one is still online (valid lease) →
	// two genuinely different devices, must NOT merge.
	old := &Device{
		MAC: "a2:11:11:11:11:11", Hostname: "iPhone", DisplayName: "A",
		LeaseExpires: now.Add(time.Hour),
	}
	r.devices[old.MAC] = old
	newDev := &Device{MAC: "a6:22:22:22:22:22", FirstSeen: now}
	r.devices[newDev.MAC] = newDev

	r.carryForwardRotatedLocked(newDev, "iPhone", now)

	if newDev.DisplayName != "" {
		t.Error("should not inherit from an online same-hostname device")
	}
	if _, ok := r.devices[old.MAC]; !ok {
		t.Error("online device must not be deleted")
	}
}

func TestPruneStaleRandomized(t *testing.T) {
	now := time.Now()
	r := newReg(t)
	old := now.Add(-10 * time.Hour)
	r.devices["a2:00:00:00:00:01"] = &Device{MAC: "a2:00:00:00:00:01", LastSeen: old}                       // ghost → prune
	r.devices["a2:00:00:00:00:02"] = &Device{MAC: "a2:00:00:00:00:02", DisplayName: "Keep", LastSeen: old}   // named → keep
	r.devices["a2:00:00:00:00:03"] = &Device{MAC: "a2:00:00:00:00:03", PauseUntil: now.Add(time.Hour), LastSeen: old} // paused → keep
	r.devices["dc:a6:32:00:00:04"] = &Device{MAC: "dc:a6:32:00:00:04", LastSeen: old}                        // not randomized → keep
	r.devices["a2:00:00:00:00:05"] = &Device{MAC: "a2:00:00:00:00:05", LastSeen: now.Add(-1 * time.Hour)}    // too recent → keep

	n := r.PruneStaleRandomized(now, 6*time.Hour)
	if n != 1 {
		t.Errorf("pruned %d, want 1", n)
	}
	if _, ok := r.devices["a2:00:00:00:00:01"]; ok {
		t.Error("anonymous ghost should have been pruned")
	}
	for _, keep := range []string{"a2:00:00:00:00:02", "a2:00:00:00:00:03", "dc:a6:32:00:00:04", "a2:00:00:00:00:05"} {
		if _, ok := r.devices[keep]; !ok {
			t.Errorf("device %s should have been kept", keep)
		}
	}
}
