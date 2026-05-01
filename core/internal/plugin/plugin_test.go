package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifest(t *testing.T, dir, id, body string) {
	t.Helper()
	pluginDir := filepath.Join(dir, id)
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", pluginDir, err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, ManifestFile), []byte(body), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func TestDiscoverFindsValidManifests(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "alpha", `id: alpha
name: Alpha
version: 0.1.0
description: First plugin
`)
	writeManifest(t, dir, "bravo", `id: bravo
name: Bravo
version: 1.0.0
`)

	r := NewRegistry(dir)
	if err := r.Discover(); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	got := r.List()
	if len(got) != 2 {
		t.Fatalf("expected 2 plugins, got %d", len(got))
	}
	if got[0].ID != "alpha" || got[1].ID != "bravo" {
		t.Errorf("not in alphabetical order: %+v", got)
	}
}

func TestDiscoverIgnoresNonDirectories(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "real", `id: real
name: Real
version: 0.1.0
`)
	if err := os.WriteFile(filepath.Join(dir, "stray.txt"), []byte("ignore me"), 0o644); err != nil {
		t.Fatalf("write stray: %v", err)
	}

	r := NewRegistry(dir)
	if err := r.Discover(); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(r.List()) != 1 {
		t.Errorf("expected 1 plugin (skip non-dirs), got %d", len(r.List()))
	}
}

func TestDiscoverRejectsMismatchedID(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "real-name", `id: spoofed-id
name: X
version: 1.0.0
`)
	r := NewRegistry(dir)
	err := r.Discover()
	if err == nil {
		t.Fatal("expected error for mismatched id/dir, got nil")
	}
	if !strings.Contains(err.Error(), "does not match directory name") {
		t.Errorf("unexpected error: %v", err)
	}
	if len(r.List()) != 0 {
		t.Errorf("rejected plugin should not be registered, got %d", len(r.List()))
	}
}

func TestDiscoverRejectsBadYAML(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "broken", "this: is: : not: valid: yaml")
	r := NewRegistry(dir)
	if err := r.Discover(); err == nil {
		t.Fatal("expected error for malformed yaml")
	}
}

func TestDiscoverRejectsBadID(t *testing.T) {
	cases := map[string]string{
		"slash/in":   "id: slash/in\nname: x\nversion: 1\n",
		"empty":      "id: \nname: x\nversion: 1\n",
		"with space": "id: with space\nname: x\nversion: 1\n",
	}
	for label, body := range cases {
		dir := t.TempDir()
		writeManifest(t, dir, "x", body)
		r := NewRegistry(dir)
		if err := r.Discover(); err == nil {
			t.Errorf("%s: expected error", label)
		}
	}
}

func TestDiscoverMissingDirIsEmpty(t *testing.T) {
	r := NewRegistry(filepath.Join(t.TempDir(), "does-not-exist"))
	if err := r.Discover(); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(r.List()) != 0 {
		t.Errorf("expected empty list, got %d", len(r.List()))
	}
}

func TestEnabledStatePreservedAcrossDiscovery(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "p1", "id: p1\nname: One\nversion: 1\n")

	r := NewRegistry(dir)
	if err := r.Discover(); err != nil {
		t.Fatal(err)
	}
	if !r.SetEnabled("p1", true) {
		t.Fatal("SetEnabled returned false for known id")
	}

	// Add a second plugin and rediscover. p1 must stay enabled.
	writeManifest(t, dir, "p2", "id: p2\nname: Two\nversion: 1\n")
	if err := r.Discover(); err != nil {
		t.Fatal(err)
	}
	got, _ := r.Get("p1")
	if !got.Enabled {
		t.Error("p1 lost enabled state on rediscovery")
	}
	got2, _ := r.Get("p2")
	if got2.Enabled {
		t.Error("p2 should default to disabled")
	}
}

func TestMenuParsedAndValidated(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "menud", `id: menud
name: Menu Demo
version: 0.1.0
menu:
  - path: /plugins/menud/home
    label: Home
    icon: bi-house
    order: 10
  - path: /plugins/menud/settings
    label: Settings
    icon: bi-gear
`)
	r := NewRegistry(dir)
	if err := r.Discover(); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	got, ok := r.Get("menud")
	if !ok {
		t.Fatal("menud not registered")
	}
	if len(got.Menu) != 2 {
		t.Fatalf("expected 2 menu items, got %d", len(got.Menu))
	}
	if got.Menu[0].Path != "/plugins/menud/home" || got.Menu[0].Icon != "bi-house" {
		t.Errorf("menu item 0 mismatch: %+v", got.Menu[0])
	}
}

func TestMenuRejectsBadPath(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "bad", `id: bad
name: Bad
version: 0.1.0
menu:
  - path: relative/path
    label: Bad
`)
	r := NewRegistry(dir)
	if err := r.Discover(); err == nil {
		t.Error("expected error for relative menu path")
	}
}

func TestApplyEnabledMap(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "p1", "id: p1\nname: One\nversion: 1\n")
	writeManifest(t, dir, "p2", "id: p2\nname: Two\nversion: 1\n")

	r := NewRegistry(dir)
	if err := r.Discover(); err != nil {
		t.Fatal(err)
	}
	r.ApplyEnabledMap(map[string]bool{"p1": true, "ghost": true})

	if g, _ := r.Get("p1"); !g.Enabled {
		t.Error("p1 should be enabled")
	}
	if g, _ := r.Get("p2"); g.Enabled {
		t.Error("p2 should be disabled")
	}
	got := r.EnabledMap()
	if len(got) != 1 || !got["p1"] {
		t.Errorf("EnabledMap = %v, want only p1", got)
	}
}
