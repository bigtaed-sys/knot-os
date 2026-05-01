package deviceregistry

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// LeaseEntry is one line of dnsmasq.leases.
//
// Format (space-separated):
//
//	<expiry-epoch> <mac> <ip> <hostname-or-*> <client-id-or-*>
//
// Entries with expiry 0 are static / infinite-lease and rare in our
// setup — handled the same way (treated as "currently valid").
type LeaseEntry struct {
	Expires  time.Time
	MAC      string
	IP       string
	Hostname string
}

// parseLeasesFile reads the entire dnsmasq lease file and returns
// every entry. Malformed lines are skipped (logged by the caller).
func parseLeasesFile(path string) ([]LeaseEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return parseLeases(f)
}

// parseLeases reads from an io.Reader of dnsmasq.leases-format text.
// Exposed (unexported package-level) for unit tests against fixtures.
func parseLeases(r io.Reader) ([]LeaseEntry, error) {
	var out []LeaseEntry
	s := bufio.NewScanner(r)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			// Bad line; skip. dnsmasq sometimes emits a duid-only
			// line at the top of the file.
			continue
		}
		exp, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		mac := strings.ToLower(fields[1])
		ip := fields[2]
		hostname := fields[3]
		if hostname == "*" {
			hostname = ""
		}
		var expires time.Time
		if exp > 0 {
			expires = time.Unix(exp, 0)
		} else {
			// Treat 0 (static / "no-expiry") as far-future so Online()
			// returns true.
			expires = time.Now().Add(365 * 24 * time.Hour)
		}
		out = append(out, LeaseEntry{
			Expires:  expires,
			MAC:      mac,
			IP:       ip,
			Hostname: hostname,
		})
	}
	if err := s.Err(); err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}
	return out, nil
}
