package dns

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	mdns "github.com/miekg/dns"
)

// DoH (DNS-over-HTTPS, RFC 8484) upstream support.
//
// We POST the raw DNS wire-format query to the provider's
// /dns-query endpoint with Content-Type: application/dns-message,
// and parse the same wire-format answer back. POST keeps URLs out
// of TLS keylog files and avoids the base64url-in-URL gymnastics
// the GET variant requires.
//
// The DoH path piggy-backs on the existing cache + query-log
// shared by the UDP forwarder — same observability, same per-
// device blocklists. From the rest of the resolver's perspective
// the only difference is which transport `forward()` chose.

// UpstreamMode picks the wire transport for upstream resolution.
type UpstreamMode string

const (
	// UpstreamModeUDP is the default plain-UDP RFC 1035 path.
	// Targets are "host:port" (1.1.1.1:53).
	UpstreamModeUDP UpstreamMode = "udp"
	// UpstreamModeDoH is RFC 8484 over HTTPS. Targets are full
	// URLs (https://cloudflare-dns.com/dns-query).
	UpstreamModeDoH UpstreamMode = "doh"
)

// DefaultDoHUpstreams is the conservative built-in DoH list — both
// providers operate large anycast pools, both have decent
// reachability from .ru / .by / .kz networks, and using two means
// a single-provider outage doesn't black-hole the whole resolver.
//
// Users can override via the Protection UI; what's here is just
// the "drop a fresh image, it works" default.
var DefaultDoHUpstreams = []string{
	"https://cloudflare-dns.com/dns-query",
	"https://dns.quad9.net/dns-query",
}

// DoHClient is a tiny RFC 8484 client. Reused across many
// queries — its http.Client maintains a TLS+HTTP/2 connection
// pool, so per-query latency on hot upstreams is just one RTT.
type DoHClient struct {
	hc      *http.Client
	timeout time.Duration
}

// NewDoHClient builds a DoH client with sane defaults.
func NewDoHClient() *DoHClient {
	return &DoHClient{
		hc: &http.Client{
			// Per-attempt budget. Resolver's `forward()` retries
			// across all upstreams within its own deadline, so this
			// is just the per-upstream cap.
			Timeout: 4 * time.Second,
			Transport: &http.Transport{
				// Generous pool defaults — a busy LAN with 50
				// devices peaks around a few hundred queries per
				// minute, and HTTP/2 multiplexes them on one
				// connection per provider anyway.
				MaxIdleConns:        16,
				MaxIdleConnsPerHost: 8,
				IdleConnTimeout:     90 * time.Second,
				TLSHandshakeTimeout: 6 * time.Second,
				DialContext: (&net.Dialer{
					Timeout:   5 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
			},
		},
		timeout: 4 * time.Second,
	}
}

// Exchange sends r to the supplied https URL and returns the
// parsed reply. Mirrors miekg/dns Client.Exchange's contract so
// the resolver's `forward()` can swap transports trivially.
func (c *DoHClient) Exchange(ctx context.Context, r *mdns.Msg, upstreamURL string) (*mdns.Msg, error) {
	if !strings.HasPrefix(upstreamURL, "https://") {
		return nil, fmt.Errorf("doh: upstream %q is not https", upstreamURL)
	}

	pkt, err := r.Pack()
	if err != nil {
		return nil, fmt.Errorf("doh: pack query: %w", err)
	}

	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewReader(pkt))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")
	// Identifier UA — operators sometimes log this; surface that
	// it's KnotOS so support questions are easier on the provider's
	// end too.
	req.Header.Set("User-Agent", "knotd/dns-doh")

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("doh: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// Read a small chunk of the body to surface the provider's
		// error message in our log without ballooning memory.
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("doh: %s: HTTP %d: %s", upstreamURL, resp.StatusCode, strings.TrimSpace(string(preview)))
	}

	// RFC 8484 §6 caps the wire-format response at the same 65535
	// limit DNS itself imposes. Bound the read to be defensive
	// against a hostile-or-broken upstream.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, fmt.Errorf("doh: read body: %w", err)
	}
	out := new(mdns.Msg)
	if err := out.Unpack(body); err != nil {
		return nil, fmt.Errorf("doh: unpack reply: %w", err)
	}
	return out, nil
}

// ValidateDoHURL checks a user-entered upstream string. Returns nil
// if the URL is acceptable; an error explaining the problem
// otherwise. Used by the API to give a helpful message when the
// user types e.g. "1.1.1.1" expecting it to be a DoH URL.
func ValidateDoHURL(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return errors.New("empty URL")
	}
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("scheme %q: only https is supported (RFC 8484)", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("missing host")
	}
	if u.Path == "" || u.Path == "/" {
		return errors.New("missing path — most providers want /dns-query")
	}
	return nil
}
