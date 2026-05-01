package dns

import (
	"bufio"
	"io"
	"strings"
)

// ParseHostsFile reads StevenBlack/hosts-style content into the
// blocklist. Format (lines):
//
//	0.0.0.0 example.com
//	127.0.0.1 ads.tracker.com
//	# comments and blank lines are ignored
//
// Lines that don't look like a valid hosts entry are silently
// skipped. Returns the number of distinct domains added.
func ParseHostsFile(r io.Reader, into *Blocklist) (int, error) {
	added := 0
	s := bufio.NewScanner(r)
	// hosts files can grow above the default 64KB scanner buffer in a
	// single line (unlikely but cheap to widen).
	s.Buffer(make([]byte, 0, 1<<16), 1<<20)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		// Strip end-of-line comments.
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		ip := fields[0]
		// We only accept null-route entries to avoid accidentally
		// importing a /etc/hosts file with real loopback aliases like
		// "127.0.0.1 localhost".
		if ip != "0.0.0.0" && ip != "::" {
			continue
		}
		for _, name := range fields[1:] {
			name = normalizeDomain(name)
			if name == "" || name == "localhost" || name == "broadcasthost" {
				continue
			}
			before := into.Size()
			into.Add(name)
			if into.Size() > before {
				added++
			}
		}
	}
	if err := s.Err(); err != nil {
		return added, err
	}
	return added, nil
}
