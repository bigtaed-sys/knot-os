package notify

import (
	"strings"
	"testing"
)

// TestPhrasesParity ensures every key present in the RU table is
// also present in EN (and vice-versa). Catches the easy mistake of
// adding a new phrase to one table and forgetting the other.
func TestPhrasesParity(t *testing.T) {
	for k := range phrasesRU {
		if _, ok := phrasesEN[k]; !ok {
			t.Errorf("phrase %q exists in RU but not EN", k)
		}
	}
	for k := range phrasesEN {
		if _, ok := phrasesRU[k]; !ok {
			t.Errorf("phrase %q exists in EN but not RU", k)
		}
	}
}

// TestPhrasesFormatVerbsMatch ensures both languages use the same
// %s/%d placeholder shape for any given key. A mismatch means
// fmt.Sprintf will mis-format the chosen translation depending on
// the user's lang setting — a real-world bug we hit pre-M30 once.
func TestPhrasesFormatVerbsMatch(t *testing.T) {
	for k, ru := range phrasesRU {
		en := phrasesEN[k]
		ruVerbs := extractVerbs(ru)
		enVerbs := extractVerbs(en)
		if ruVerbs != enVerbs {
			t.Errorf("phrase %q has different format verbs: RU=%q EN=%q", k, ruVerbs, enVerbs)
		}
	}
}

// TestPhrasesNonEmpty catches accidentally-cleared values.
func TestPhrasesNonEmpty(t *testing.T) {
	for k, v := range phrasesRU {
		if v == "" {
			t.Errorf("phrase %q has empty RU value", k)
		}
	}
	for k, v := range phrasesEN {
		if v == "" {
			t.Errorf("phrase %q has empty EN value", k)
		}
	}
}

// TestL10nFallback verifies that an unknown lang (or a missing
// key) doesn't crash and produces something useful.
func TestL10nFallback(t *testing.T) {
	tr := newL10n("ru")

	// Unknown lang → falls back to EN.
	if got := tr.T("zh", "welcome"); !strings.Contains(got, "👋") {
		t.Errorf("unknown lang fallback failed: %q", got)
	}

	// Missing key → returns the key itself, so a typo surfaces.
	if got := tr.T("ru", "this_is_a_typo"); got != "this_is_a_typo" {
		t.Errorf("missing key did not return key: %q", got)
	}

	// Format args.
	got := tr.T("ru", "status_clients", 5)
	if !strings.Contains(got, "5") {
		t.Errorf("format args not applied: %q", got)
	}
}

// TestRoutingPhrasesPresent ensures the new M30 routing keys are
// wired up so /routing doesn't render the literal key string.
func TestRoutingPhrasesPresent(t *testing.T) {
	for _, k := range []string{
		"routing_title", "routing_empty", "routing_subs",
		"routing_buckets", "routing_missing", "tips",
	} {
		if _, ok := phrasesRU[k]; !ok {
			t.Errorf("missing RU key: %s", k)
		}
		if _, ok := phrasesEN[k]; !ok {
			t.Errorf("missing EN key: %s", k)
		}
	}
}

// extractVerbs returns the format-verb subset of the string,
// stripped of everything else. We only care about %s/%d/%v/%%
// equivalence, not surrounding text.
func extractVerbs(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			continue
		}
		if i+1 >= len(s) {
			continue
		}
		c := s[i+1]
		switch c {
		case 's', 'd', 'v', '%':
			b.WriteByte('%')
			b.WriteByte(c)
		}
	}
	return b.String()
}
