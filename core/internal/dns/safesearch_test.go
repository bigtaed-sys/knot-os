package dns

import "testing"

func TestSafeSearchTarget(t *testing.T) {
	cases := map[string]string{
		"www.google.com":          "forcesafesearch.google.com",
		"google.com":              "forcesafesearch.google.com",
		"google.de":               "forcesafesearch.google.com",
		"www.google.co.uk":        "forcesafesearch.google.com",
		"google.com.au":           "forcesafesearch.google.com",
		"www.youtube.com":         "restrict.youtube.com",
		"m.youtube.com":           "restrict.youtube.com",
		"youtubei.googleapis.com": "restrict.youtube.com",
		"www.bing.com":            "strict.bing.com",
		"duckduckgo.com":          "safe.duckduckgo.com",
		// Not rewritten:
		"mail.google.com":  "",
		"drive.google.com": "",
		"googleblog.com":   "",
		"example.com":      "",
		"notgoogle.com":    "",
		"maps.google.com":  "",
	}
	for qname, want := range cases {
		got, ok := safeSearchTarget(qname)
		if want == "" {
			if ok {
				t.Errorf("safeSearchTarget(%q)=%q, want no rewrite", qname, got)
			}
			continue
		}
		if !ok || got != want {
			t.Errorf("safeSearchTarget(%q)=%q,%v; want %q", qname, got, ok, want)
		}
	}
}
