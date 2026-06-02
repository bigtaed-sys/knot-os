package dns

import "strings"

// SafeSearch enforcement works by CNAME-rewriting queries for the
// major search engines and YouTube to the providers' own "safe"
// hostnames, exactly as AdGuard Home / Pi-hole do it:
//
//	www.google.com   → forcesafesearch.google.com
//	*.youtube.com    → restrict.youtube.com   (Strict mode)
//	www.bing.com     → strict.bing.com
//	duckduckgo.com   → safe.duckduckgo.com
//
// Those targets are A/AAAA-only records the providers maintain; the
// resolver returns a CNAME to the target plus the target's resolved
// addresses, so the client connects to the enforcement endpoint
// without any client-side config. HTTPS/SVCB queries get NODATA so
// the browser falls back to the rewritten A/AAAA.
//
// We deliberately keep the list small and exact: only the bare
// search domain and its www/m hosts are rewritten, never sibling
// services (mail.google.com, drive.google.com, …).

// staticSafeSearch maps an exact lowercased query name (no trailing
// dot) to its enforcement target.
var staticSafeSearch = map[string]string{
	"www.youtube.com":         "restrict.youtube.com",
	"m.youtube.com":           "restrict.youtube.com",
	"youtube.com":             "restrict.youtube.com",
	"youtubei.googleapis.com": "restrict.youtube.com",
	"youtube.googleapis.com":  "restrict.youtube.com",
	"www.youtube-nocookie.com": "restrict.youtube.com",
	"www.bing.com":            "strict.bing.com",
	"bing.com":                "strict.bing.com",
	"duckduckgo.com":          "safe.duckduckgo.com",
	"www.duckduckgo.com":      "safe.duckduckgo.com",
}

// safeSearchTarget returns the enforcement hostname for qname, or
// ("", false) when the name is not a search domain we rewrite.
// qname must already be normalized (lowercase, no trailing dot).
func safeSearchTarget(qname string) (string, bool) {
	if t, ok := staticSafeSearch[qname]; ok {
		return t, true
	}
	// Google ships ~190 country TLDs (google.de, google.co.uk …);
	// the bare domain and its www host are search, everything else
	// (mail., drive., …) is not.
	if isGoogleSearchDomain(qname) {
		return "forcesafesearch.google.com", true
	}
	return "", false
}

// isGoogleSearchDomain reports whether qname is google.<tld> or
// www.google.<tld> for a 1- or 2-label TLD (com, de, co.uk, com.au).
func isGoogleSearchDomain(qname string) bool {
	qname = strings.TrimPrefix(qname, "www.")
	const pfx = "google."
	if !strings.HasPrefix(qname, pfx) {
		return false
	}
	rest := qname[len(pfx):]
	if rest == "" {
		return false
	}
	// Reject anything that still carries a sub-label before "google"
	// (already stripped www above) — i.e. only google.<tld> remains.
	if strings.Count(rest, ".") > 1 {
		return false
	}
	for _, r := range rest {
		if !(r >= 'a' && r <= 'z') && r != '.' {
			return false
		}
	}
	return true
}
