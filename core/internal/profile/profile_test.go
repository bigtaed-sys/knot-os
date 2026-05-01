package profile

import (
	"path/filepath"
	"testing"
	"time"
)

func TestBuiltinsAreValid(t *testing.T) {
	for _, p := range builtins() {
		if err := p.Validate(); err != nil {
			t.Errorf("builtin %q invalid: %v", p.ID, err)
		}
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]Profile{
		"empty id":   {Name: "x"},
		"bad id":     {ID: "Bad ID!", Name: "x"},
		"empty name": {ID: "ok"},
		"bad day": {ID: "ok", Name: "x", BlockWindows: []BlockWindow{
			{Days: []int{99}, Start: "10:00", End: "11:00"},
		}},
		"bad start": {ID: "ok", Name: "x", BlockWindows: []BlockWindow{
			{Days: []int{1}, Start: "25:00", End: "11:00"},
		}},
		"same start end": {ID: "ok", Name: "x", BlockWindows: []BlockWindow{
			{Days: []int{1}, Start: "10:00", End: "10:00"},
		}},
	}
	for name, p := range cases {
		if err := p.Validate(); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestIsActiveAtSameDay(t *testing.T) {
	w := BlockWindow{
		Days:  []int{1, 2, 3, 4, 5}, // Mon-Fri
		Start: "09:00",
		End:   "17:00",
	}
	// Wednesday (weekday 3) at 12:00 -> active
	wed12 := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	if !w.IsActiveAt(wed12) {
		t.Error("Wed 12:00 should be active")
	}
	// Wednesday at 18:00 -> not active
	wed18 := time.Date(2026, 5, 6, 18, 0, 0, 0, time.UTC)
	if w.IsActiveAt(wed18) {
		t.Error("Wed 18:00 should not be active")
	}
	// Saturday at 12:00 -> not active
	sat12 := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	if w.IsActiveAt(sat12) {
		t.Error("Sat 12:00 should not be active")
	}
}

func TestIsActiveAtCrossMidnight(t *testing.T) {
	w := BlockWindow{
		Days:  []int{0, 1, 2, 3, 4, 5, 6},
		Start: "22:00",
		End:   "07:00",
	}
	// Wed 23:00 -> active (window started today)
	wed23 := time.Date(2026, 5, 6, 23, 0, 0, 0, time.UTC)
	if !w.IsActiveAt(wed23) {
		t.Error("Wed 23:00 should be active")
	}
	// Thu 03:00 -> active (window started yesterday Wed 22:00)
	thu3 := time.Date(2026, 5, 7, 3, 0, 0, 0, time.UTC)
	if !w.IsActiveAt(thu3) {
		t.Error("Thu 03:00 should be active (carryover)")
	}
	// Thu 08:00 -> not active
	thu8 := time.Date(2026, 5, 7, 8, 0, 0, 0, time.UTC)
	if w.IsActiveAt(thu8) {
		t.Error("Thu 08:00 should not be active")
	}
	// Wed 21:30 -> not active (before start)
	wed21 := time.Date(2026, 5, 6, 21, 30, 0, 0, time.UTC)
	if w.IsActiveAt(wed21) {
		t.Error("Wed 21:30 should not be active")
	}
}

func TestIsBlockingAtUsesAnyWindow(t *testing.T) {
	p := Profile{
		ID: "test", Name: "Test",
		BlockWindows: []BlockWindow{
			{Days: []int{0, 6}, Start: "00:00", End: "12:00"},  // weekend morning
			{Days: []int{1, 2, 3, 4, 5}, Start: "09:00", End: "17:00"}, // weekday
		},
	}
	// Saturday at 10:00 (weekday 6 = Saturday)
	sat10 := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	if !p.IsBlockingAt(sat10) {
		t.Error("Sat 10:00 should be blocking (weekend window)")
	}
	// Saturday at 14:00
	sat14 := time.Date(2026, 5, 9, 14, 0, 0, 0, time.UTC)
	if p.IsBlockingAt(sat14) {
		t.Error("Sat 14:00 should not be blocking")
	}
	// Wednesday at 11:00 (weekday window)
	wed11 := time.Date(2026, 5, 6, 11, 0, 0, 0, time.UTC)
	if !p.IsBlockingAt(wed11) {
		t.Error("Wed 11:00 should be blocking")
	}
}

func TestRegistryStartsWithBuiltins(t *testing.T) {
	r := NewRegistry(filepath.Join(t.TempDir(), "p.yaml"))
	all := r.List()
	if len(all) < 3 {
		t.Fatalf("expected at least 3 builtins, got %d", len(all))
	}
	for _, want := range []string{"default", "kids", "guest"} {
		if _, ok := r.Get(want); !ok {
			t.Errorf("missing builtin: %q", want)
		}
	}
}

func TestRegistrySaveLoadCustom(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "p.yaml")
	r := NewRegistry(store)
	custom := Profile{
		ID: "work", Name: "Workhours", Description: "9-17 пн-пт",
		BlockWindows: []BlockWindow{
			{Days: []int{1, 2, 3, 4, 5}, Start: "09:00", End: "17:00"},
		},
		DNSBlocklists: []string{"social"},
	}
	if err := r.Put(custom); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := r.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	r2 := NewRegistry(store)
	if err := r2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, ok := r2.Get("work")
	if !ok {
		t.Fatal("custom profile lost")
	}
	if got.Name != "Workhours" {
		t.Errorf("Name: %q", got.Name)
	}
	if len(got.BlockWindows) != 1 || got.BlockWindows[0].Start != "09:00" {
		t.Errorf("BlockWindows: %+v", got.BlockWindows)
	}
}

func TestCannotDeleteBuiltin(t *testing.T) {
	r := NewRegistry(filepath.Join(t.TempDir(), "p.yaml"))
	if err := r.Delete("default"); err == nil {
		t.Error("expected error deleting builtin")
	}
}
