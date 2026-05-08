//go:build linux

package bandwidth

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// LinuxSampler reads /proc/net/nf_conntrack on each tick, aggregates
// byte counters by source IP, resolves IP→MAC against the device
// registry, and feeds the result into the Tracker.
//
// Why /proc/net/nf_conntrack and not the conntrack-tools CLI:
// reading the procfs file is a single open + scan, no fork, no IPC,
// no extra Debian package required at image build time. The format
// is stable since 2.6.x kernels.
type LinuxSampler struct {
	tracker  *Tracker
	resolver IPToMACResolver
	// LANCIDR limits aggregation to the LAN — WAN-side connections
	// (router → internet) shouldn't show up as device traffic.
	// Empty == no filter.
	LANCIDR string
}

// IPToMACResolver maps an IP address to the device's MAC. Wired by
// main.go to query deviceregistry.Registry.
type IPToMACResolver interface {
	MACForIP(ip string) (string, bool)
}

// NewLinuxSampler builds a sampler. tracker and resolver are required.
func NewLinuxSampler(t *Tracker, r IPToMACResolver, lanCIDR string) *LinuxSampler {
	return &LinuxSampler{tracker: t, resolver: r, LANCIDR: lanCIDR}
}

// Run blocks, sampling every SampleInterval until ctx is cancelled.
// Spawn it from a goroutine in main.go.
func (s *LinuxSampler) Run(ctx context.Context) {
	ticker := time.NewTicker(SampleInterval)
	defer ticker.Stop()
	last := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			batch := s.sampleOnce()
			interval := now.Sub(last).Seconds()
			s.tracker.Update(now, batch, interval)
			last = now
		}
	}
}

// sampleOnce reads procfs once and returns a batch of FlowMetric
// per MAC. Errors are swallowed (returned as empty batch) — a
// transient read failure shouldn't kill the whole subsystem.
func (s *LinuxSampler) sampleOnce() []FlowMetric {
	f, err := os.Open("/proc/net/nf_conntrack")
	if err != nil {
		return nil
	}
	defer f.Close()

	// Aggregate by MAC. We accumulate the cumulative bytes; the
	// tracker computes deltas itself from the previous reading.
	per := make(map[string]*FlowMetric)
	var lanNet *net.IPNet
	if s.LANCIDR != "" {
		_, lanNet, _ = net.ParseCIDR(s.LANCIDR)
	}

	scanner := bufio.NewScanner(f)
	// nf_conntrack lines can get long with extension fields.
	scanner.Buffer(make([]byte, 0, 8192), 64*1024)
	for scanner.Scan() {
		line := scanner.Text()
		entry, ok := parseConntrackLine(line)
		if !ok {
			continue
		}

		// Treat the LAN side of the connection as the device. For
		// outbound (LAN→WAN) flows that's `src` of the original
		// tuple. For inbound forwarded traffic (rare on a NAT
		// router, but possible with port-forwards), the destination
		// is on the LAN. We check both.
		var lanIP string
		if lanNet != nil {
			if lanNet.Contains(net.ParseIP(entry.origSrc)) {
				lanIP = entry.origSrc
			} else if lanNet.Contains(net.ParseIP(entry.origDst)) {
				lanIP = entry.origDst
			}
		} else {
			lanIP = entry.origSrc
		}
		if lanIP == "" {
			continue
		}
		mac, ok := s.resolver.MACForIP(lanIP)
		if !ok {
			continue
		}

		// Direction relative to the device:
		//   "out" = device sent it (origSrc == LAN IP) → use orig bytes
		//   "in"  = device received it (origDst == LAN IP) → use reply bytes
		fm := per[mac]
		if fm == nil {
			fm = &FlowMetric{MAC: mac}
			per[mac] = fm
		}
		if lanIP == entry.origSrc {
			fm.BytesOut += entry.origBytes
			fm.BytesIn += entry.replyBytes
		} else {
			fm.BytesIn += entry.origBytes
			fm.BytesOut += entry.replyBytes
		}
	}

	out := make([]FlowMetric, 0, len(per))
	for _, m := range per {
		out = append(out, *m)
	}
	return out
}

// conntrackEntry is the subset of fields we care about from one
// /proc/net/nf_conntrack line.
type conntrackEntry struct {
	origSrc    string
	origDst    string
	origBytes  uint64
	replyBytes uint64
}

// parseConntrackLine extracts (orig_src, orig_dst, orig_bytes,
// reply_bytes) from a single line of /proc/net/nf_conntrack.
//
// Format example (real, IPv4 TCP):
//
//	ipv4   2 tcp   6 431999 ESTABLISHED \
//	  src=192.168.42.55 dst=104.16.0.1 sport=51234 dport=443 \
//	  packets=12 bytes=890 \
//	  src=104.16.0.1 dst=192.168.42.55 sport=443 dport=51234 \
//	  packets=10 bytes=4521 [ASSURED] mark=0 use=2
//
// The fields appear twice (original tuple + reply tuple). We pick
// the FIRST src/dst/bytes triple as orig and the SECOND as reply.
func parseConntrackLine(line string) (conntrackEntry, bool) {
	// Skip non-IPv4 entries — keeps the parser simple. IPv6 path
	// lands in v2026.07 alongside the broader v6 work.
	if !strings.HasPrefix(line, "ipv4 ") {
		return conntrackEntry{}, false
	}

	var e conntrackEntry
	gotOrigSrc := false
	gotOrigDst := false
	gotOrigBytes := false

	// Tokenize on spaces. Field order is reasonably stable; we
	// pattern-match on prefixes.
	for _, tok := range strings.Fields(line) {
		switch {
		case strings.HasPrefix(tok, "src="):
			if !gotOrigSrc {
				e.origSrc = strings.TrimPrefix(tok, "src=")
				gotOrigSrc = true
			}
		case strings.HasPrefix(tok, "dst="):
			if !gotOrigDst {
				e.origDst = strings.TrimPrefix(tok, "dst=")
				gotOrigDst = true
			}
		case strings.HasPrefix(tok, "bytes="):
			n, err := strconv.ParseUint(strings.TrimPrefix(tok, "bytes="), 10, 64)
			if err != nil {
				continue
			}
			if !gotOrigBytes {
				e.origBytes = n
				gotOrigBytes = true
			} else {
				e.replyBytes = n
			}
		}
	}
	if !gotOrigSrc || !gotOrigDst {
		return conntrackEntry{}, false
	}
	return e, true
}

// FormatRate humanises a Kbps reading to "12 Kbps" / "1.4 Mbps" /
// "—" etc. for log lines.
func FormatRate(kbps float64) string {
	if kbps < 0.5 {
		return "—"
	}
	if kbps < 1000 {
		return fmt.Sprintf("%.0f Kbps", kbps)
	}
	return fmt.Sprintf("%.1f Mbps", kbps/1000)
}
