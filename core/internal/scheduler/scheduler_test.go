package scheduler

import (
	"sort"
	"strings"
	"testing"
	"time"
)

type fakeDevices struct{ list []Device }

func (f *fakeDevices) List() []Device { return f.list }

type fakeProfiles struct{ blocking map[string]bool }

func (f *fakeProfiles) IsBlockingAt(id string, _ time.Time) bool {
	return f.blocking[id]
}

type fakeUpdater struct {
	calls [][]string
	err   error
}

func (f *fakeUpdater) UpdateBlockedMACs(macs []string) error {
	cp := append([]string(nil), macs...)
	f.calls = append(f.calls, cp)
	return f.err
}

func TestRunOncePausedAndQuarantine(t *testing.T) {
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	dev := &fakeDevices{list: []Device{
		{MAC: "aa:aa:aa:aa:aa:aa", Approved: true, PauseUntil: now.Add(time.Hour)}, // paused → blocked
		{MAC: "bb:bb:bb:bb:bb:bb", Approved: true, PauseUntil: now.Add(-time.Hour)}, // pause expired → not blocked
		{MAC: "cc:cc:cc:cc:cc:cc", Approved: false},                                  // quarantine + not approved → blocked
		{MAC: "dd:dd:dd:dd:dd:dd", Approved: true},                                   // fine
		{MAC: "ee:ee:ee:ee:ee:ee", Approved: true, ProfileID: "kids"},                // schedule blocks
	}}
	prof := &fakeProfiles{blocking: map[string]bool{"kids": true}}
	upd := &fakeUpdater{}
	s := New(Options{
		Devices: dev, Profiles: prof, Updater: upd, Tick: time.Hour,
		Quarantine: func() bool { return true },
		Now:        func() time.Time { return now },
	})
	s.RunOnce()

	got := upd.calls[0]
	sort.Strings(got)
	want := []string{"aa:aa:aa:aa:aa:aa", "cc:cc:cc:cc:cc:cc", "ee:ee:ee:ee:ee:ee"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("blocked set = %v, want %v", got, want)
	}
}

func TestRunOnceQuarantineOffIgnoresApproval(t *testing.T) {
	dev := &fakeDevices{list: []Device{{MAC: "cc:cc:cc:cc:cc:cc", Approved: false}}}
	upd := &fakeUpdater{}
	// Quarantine off (default) → an un-approved device is NOT blocked.
	s := New(Options{Devices: dev, Profiles: &fakeProfiles{}, Updater: upd, Tick: time.Hour})
	s.RunOnce()
	if len(upd.calls[0]) != 0 {
		t.Errorf("quarantine off should block nothing, got %v", upd.calls[0])
	}
}

func TestRunOncePicksBlockedDevices(t *testing.T) {
	dev := &fakeDevices{list: []Device{
		{MAC: "11:11:11:11:11:11", ProfileID: "kids"},
		{MAC: "22:22:22:22:22:22", ProfileID: "default"},
		{MAC: "33:33:33:33:33:33", ProfileID: "kids"},
		{MAC: "44:44:44:44:44:44", ProfileID: ""}, // no profile
	}}
	prof := &fakeProfiles{blocking: map[string]bool{"kids": true, "default": false}}
	upd := &fakeUpdater{}
	s := New(Options{Devices: dev, Profiles: prof, Updater: upd, Tick: time.Hour})
	s.RunOnce()

	if len(upd.calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(upd.calls))
	}
	got := upd.calls[0]
	sort.Strings(got)
	want := []string{"11:11:11:11:11:11", "33:33:33:33:33:33"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d]=%q, want %q", i, got[i], want[i])
		}
	}
}

func TestRunOnceEmptyWhenNothingBlocked(t *testing.T) {
	dev := &fakeDevices{list: []Device{
		{MAC: "11:11:11:11:11:11", ProfileID: "default"},
	}}
	prof := &fakeProfiles{blocking: map[string]bool{}}
	upd := &fakeUpdater{}
	s := New(Options{Devices: dev, Profiles: prof, Updater: upd, Tick: time.Hour})
	s.RunOnce()
	if len(upd.calls[0]) != 0 {
		t.Errorf("expected empty block set, got %v", upd.calls[0])
	}
}
