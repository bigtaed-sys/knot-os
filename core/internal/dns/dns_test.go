package dns

import (
	"strings"
	"testing"
)

func TestBlocklistContainsParent(t *testing.T) {
	b := NewBlocklist("ads")
	b.Add("doubleclick.net")
	b.Add("ads.example.com")

	cases := map[string]bool{
		"doubleclick.net":          true,
		"foo.doubleclick.net":      true,
		"a.b.c.doubleclick.net":    true,
		"doubleclickX.net":         false, // not a subdomain
		"ads.example.com":          true,
		"banner.ads.example.com":   true,
		"example.com":              false, // parent not blocked unless explicitly listed
		"unrelated.com":            false,
	}
	for q, want := range cases {
		if got := b.Contains(q); got != want {
			t.Errorf("Contains(%q)=%v, want %v", q, got, want)
		}
	}
}

func TestBlocklistNormalisesInput(t *testing.T) {
	b := NewBlocklist("x")
	b.Add("Example.COM.")
	b.Add(" .badspace ")
	b.Add("")

	if !b.Contains("example.com") {
		t.Error("Add should lowercase and strip trailing dot")
	}
}

func TestParseHostsFile(t *testing.T) {
	input := `
# StevenBlack/hosts header
# Comment

0.0.0.0 doubleclick.net
0.0.0.0 ads.example.com banner.example.com
127.0.0.1 localhost  # ignored — only null-route entries
::      ipv6.tracker.com
not-an-entry
0.0.0.0 # missing domain
`
	b := NewBlocklist("ads")
	added, err := ParseHostsFile(strings.NewReader(input), b)
	if err != nil {
		t.Fatalf("ParseHostsFile: %v", err)
	}
	wantAdded := 4
	if added != wantAdded {
		t.Errorf("added=%d, want %d", added, wantAdded)
	}
	for _, d := range []string{"doubleclick.net", "ads.example.com", "banner.example.com", "ipv6.tracker.com"} {
		if !b.Contains(d) {
			t.Errorf("expected %q in blocklist", d)
		}
	}
	if b.Contains("localhost") {
		t.Error("localhost should not be blocked")
	}
}

func TestRegistryAnyContains(t *testing.T) {
	r := NewRegistry()
	ads := NewBlocklist("ads")
	ads.Add("doubleclick.net")
	r.Set("ads", ads)
	trk := NewBlocklist("trackers")
	trk.Add("scorecardresearch.com")
	r.Set("trackers", trk)

	if !r.AnyContains([]string{"ads"}, "doubleclick.net") {
		t.Error("ads should match doubleclick")
	}
	if !r.AnyContains([]string{"ads", "trackers"}, "scorecardresearch.com") {
		t.Error("trackers should match scorecardresearch")
	}
	if r.AnyContains([]string{"ads"}, "scorecardresearch.com") {
		t.Error("only-ads filter shouldn't match scorecardresearch")
	}
	if r.AnyContains([]string{"missing-list"}, "doubleclick.net") {
		t.Error("nonexistent list should not match anything")
	}
	sizes := r.Sizes()
	if sizes["ads"] != 1 || sizes["trackers"] != 1 {
		t.Errorf("Sizes mismatch: %+v", sizes)
	}
}
