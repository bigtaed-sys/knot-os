package deviceregistry

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"time"
)

// DefaultARPFile is /proc/net/arp on a real Linux device. Override
// in tests via Options.ARPFile.
const DefaultARPFile = "/proc/net/arp"

// ARPEntry is one parsed row of /proc/net/arp.
type ARPEntry struct {
	IP    string
	MAC   string
	Flags int    // 0x0=incomplete, 0x2=complete, 0x4=permanent
	IFace string
}

// Complete reports whether the kernel recently completed neighbour
// discovery for this entry. Incomplete entries (flags=0) mean the
// kernel asked but got no reply — the device is gone or filtering
// our ARP probes; we don't trust those for liveness.
func (e ARPEntry) Complete() bool {
	return e.Flags&0x2 != 0
}

// parseARPFile reads /proc/net/arp and returns every parseable row.
// Format (header line skipped):
//
//	IP address       HW type     Flags       HW address            Mask     Device
//	192.168.42.55    0x1         0x2         dc:a6:32:11:22:33     *        wlan0
//
// Best-effort: any line that doesn't have at least 6 fields or
// where the flags field doesn't parse is silently skipped.
func parseARPFile(path string) ([]ARPEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return parseARP(f)
}

func parseARP(r io.Reader) ([]ARPEntry, error) {
	var out []ARPEntry
	s := bufio.NewScanner(r)
	header := true
	for s.Scan() {
		line := s.Text()
		if header {
			header = false
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		flags := parseHexFlags(fields[2])
		out = append(out, ARPEntry{
			IP:    fields[0],
			MAC:   strings.ToLower(fields[3]),
			Flags: flags,
			IFace: fields[5],
		})
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func parseHexFlags(s string) int {
	if !strings.HasPrefix(s, "0x") {
		return 0
	}
	n := 0
	for _, c := range s[2:] {
		switch {
		case c >= '0' && c <= '9':
			n = n*16 + int(c-'0')
		case c >= 'a' && c <= 'f':
			n = n*16 + int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			n = n*16 + int(c-'A') + 10
		default:
			return 0
		}
	}
	return n
}

// RefreshFromARP merges the current /proc/net/arp snapshot into the
// registry. For every complete entry whose MAC is known, we stamp
// LastARPSeen=now. New MACs are NOT created — we only track what
// went through DHCP, since random IoT chatter on the LAN would
// otherwise flood the device list.
//
// A missing /proc/net/arp (non-Linux dev box, fresh boot before
// the kernel has populated anything) is treated as "no signal" and
// returns nil so the watcher keeps ticking without log noise.
func (r *Registry) RefreshFromARP() error {
	if r.arpFile == "" {
		return nil
	}
	entries, err := parseARPFile(r.arpFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	changed := false
	for _, e := range entries {
		if !e.Complete() {
			continue
		}
		mac := normalizeMAC(e.MAC)
		if mac == "" {
			continue
		}
		d, ok := r.devices[mac]
		if !ok {
			continue
		}
		d.LastARPSeen = now
		d.LastSeen = now
		changed = true
	}
	if changed {
		// Don't mark dirty: LastARPSeen is yaml:"-" (in-memory only),
		// and bumping LastSeen on ARP doesn't add information that
		// needs to survive a reboot.
	}
	return nil
}

// StartARPWatcher kicks off a goroutine that polls /proc/net/arp on
// a 30-second cadence. Coarse enough to keep SoC wakeups rare on
// Pi Zero 2W, fine enough that a device leaving the LAN flips its
// "online" pill within a couple of minutes (lease still valid +
// ARP stale = offline by Online()).
//
// Cancels with ctx. Safe to call multiple times if startup retries
// the registry construction; the goroutine just runs harmlessly
// in parallel.
func (r *Registry) StartARPWatcher(ctx context.Context) {
	if r.arpFile == "" {
		return
	}
	go func() {
		// Initial pass right away so a freshly-booted UI doesn't
		// have to wait 30s for the first liveness signal.
		if err := r.RefreshFromARP(); err != nil {
			r.logger.Printf("deviceregistry: initial ARP refresh: %v", err)
		}
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := r.RefreshFromARP(); err != nil {
					r.logger.Printf("deviceregistry: ARP refresh: %v", err)
				}
			}
		}
	}()
}
