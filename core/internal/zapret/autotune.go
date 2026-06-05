package zapret

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

// probeHosts are TLS endpoints that ISP DPI commonly resets/throttles
// for YouTube + Discord. A strategy that lets these handshakes complete
// is, in practice, the one that "works" for those services. The router
// can probe them itself because the nft hook sits on WAN postrouting,
// which also catches knotd's own outbound connections.
var probeHosts = []string{
	"i.ytimg.com",
	"www.youtube.com",
	"cdn.discordapp.com",
	"gateway.discord.gg",
}

const (
	probeTimeout = 5 * time.Second
	// settle gives nfqws + the nft rule a moment to take effect after a
	// strategy switch before we start probing.
	settleDelay = 2 * time.Second
)

// TuneResult is one strategy's score from an auto-tune run.
type TuneResult struct {
	// Strategy is the preset ID, or "off" for the no-bypass baseline.
	Strategy string `json:"strategy"`
	Name     string `json:"name"`
	// OK is how many probe hosts completed a TLS handshake; Total is
	// how many were tried.
	OK    int `json:"ok"`
	Total int `json:"total"`
	// LatencyMS is the summed handshake time (failed hosts count the
	// full timeout) — the tiebreaker when OK is equal.
	LatencyMS int64 `json:"latency_ms"`
}

// AutoTune cycles through the built-in presets, applying each live and
// probing the YouTube/Discord test hosts, then leaves the best preset
// running. Returns the per-strategy scores (including an "off" baseline)
// and the winning preset ID.
//
// Requires a WAN interface and a usable nfqws binary (downloaded if
// absent). Serialised against Apply via the manager mutex — a tune run
// is exclusive and takes ~20-30s.
func (m *Manager) AutoTune(ctx context.Context, s Settings) ([]TuneResult, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if s.WANInterface == "" {
		return nil, "", fmt.Errorf("auto-tune needs a WAN interface (router mode)")
	}
	if m.runner == nil {
		return nil, "", fmt.Errorf("auto-tune unavailable on this host")
	}
	if _, err := MaterializeAssets(m.base); err != nil {
		return nil, "", err
	}
	binPath, err := EnsureBinary(ctx, m.base)
	if err != nil {
		return nil, "", err
	}

	strategies := LoadStrategies(m.base)
	if len(strategies) == 0 {
		return nil, "", fmt.Errorf("no strategies available")
	}

	var results []TuneResult

	// Baseline: bypass off, so the UI can show whether it's needed.
	_ = m.runner.Stop(ctx)
	sleep(ctx, settleDelay)
	base := probeAll(ctx)
	base.Strategy, base.Name = "off", "Без обхода"
	results = append(results, base)

	for _, p := range strategies {
		args, tcp, udp, err := BuildInvocation(Settings{Enabled: true, Strategy: p.ID}, m.base)
		if err != nil {
			continue
		}
		if err := m.runner.Start(ctx, binPath, args, s.WANInterface, tcp, udp); err != nil {
			continue
		}
		sleep(ctx, settleDelay)
		r := probeAll(ctx)
		r.Strategy, r.Name = p.ID, p.Name
		results = append(results, r)
		if ctx.Err() != nil {
			break
		}
	}

	winner := bestStrategy(results, strategies)

	// Leave the winner applied and running.
	wargs, wtcp, wudp, err := BuildInvocation(Settings{Enabled: true, Strategy: winner}, m.base)
	if err == nil {
		if err := m.runner.Start(ctx, binPath, wargs, s.WANInterface, wtcp, wudp); err == nil {
			m.lastKey = strings.Join([]string{s.WANInterface, wtcp, wudp, strings.Join(wargs, "\x00")}, "\x01")
		}
	}
	return results, winner, nil
}

// bestStrategy picks the strategy (never "off") with the most
// successful probes, breaking ties by lowest summed latency. Falls
// back to the first available strategy when nothing scored.
func bestStrategy(results []TuneResult, strategies []Strategy) string {
	best := ""
	var bestRes TuneResult
	for _, r := range results {
		if r.Strategy == "off" {
			continue
		}
		if best == "" || r.OK > bestRes.OK || (r.OK == bestRes.OK && r.LatencyMS < bestRes.LatencyMS) {
			best, bestRes = r.Strategy, r
		}
	}
	if best == "" && len(strategies) > 0 {
		return strategies[0].ID
	}
	return best
}

// probeAll runs every probe host concurrently and aggregates the score.
func probeAll(ctx context.Context) TuneResult {
	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		res = TuneResult{Total: len(probeHosts)}
	)
	for _, h := range probeHosts {
		wg.Add(1)
		go func(host string) {
			defer wg.Done()
			ok, d := probeTLS(ctx, host)
			mu.Lock()
			if ok {
				res.OK++
			}
			res.LatencyMS += d.Milliseconds()
			mu.Unlock()
		}(h)
	}
	wg.Wait()
	return res
}

// probeTLS attempts a TLS handshake to host:443, sending the real SNI so
// DPI (and nfqws's hostlist) see it. Cert validity is irrelevant — a
// completed handshake means the censor didn't reset the connection.
// Returns the elapsed time; on failure that's the full timeout.
func probeTLS(ctx context.Context, host string) (bool, time.Duration) {
	start := time.Now()
	d := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: probeTimeout},
		Config:    &tls.Config{InsecureSkipVerify: true, ServerName: host},
	}
	cctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	conn, err := d.DialContext(cctx, "tcp", net.JoinHostPort(host, "443"))
	elapsed := time.Since(start)
	if err != nil {
		return false, probeTimeout
	}
	_ = conn.Close()
	return true, elapsed
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// SortByScore ranks results best-first (most successful probes, then
// lowest latency). Used by the API to present the tune table.
func SortByScore(results []TuneResult) {
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].OK != results[j].OK {
			return results[i].OK > results[j].OK
		}
		return results[i].LatencyMS < results[j].LatencyMS
	})
}
