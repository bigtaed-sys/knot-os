package dns

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mdns "github.com/miekg/dns"
)

func TestValidateDoHURL(t *testing.T) {
	cases := []struct {
		in   string
		ok   bool
		hint string // substring expected in error
	}{
		{"https://cloudflare-dns.com/dns-query", true, ""},
		{"https://dns.quad9.net/dns-query", true, ""},
		// User typed an IP thinking it's a DoH endpoint. url.Parse
		// happily parses this as a path-only URL, so the hint that
		// fires is the scheme check.
		{"1.1.1.1", false, "scheme"},
		// Plain http is forbidden — the whole point of DoH is TLS.
		{"http://cloudflare-dns.com/dns-query", false, "scheme"},
		// Missing path: providers reject these too, but we can give
		// a kinder message before the request goes out.
		{"https://cloudflare-dns.com", false, "path"},
		{"https://cloudflare-dns.com/", false, "path"},
		{"", false, "empty"},
	}
	for _, c := range cases {
		err := ValidateDoHURL(c.in)
		if c.ok {
			if err != nil {
				t.Errorf("ValidateDoHURL(%q): unexpected error %v", c.in, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("ValidateDoHURL(%q): expected error", c.in)
			continue
		}
		if c.hint != "" && !strings.Contains(err.Error(), c.hint) {
			t.Errorf("ValidateDoHURL(%q): error %q missing hint %q", c.in, err, c.hint)
		}
	}
}

func TestDoHExchangeRoundtrip(t *testing.T) {
	// Stub upstream: parse the wire-format query, build a minimal
	// reply, send it back. Mirrors what a real DoH provider does
	// but is entirely local so the test doesn't hit the internet.
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("upstream got method %s, want POST", r.Method)
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		if got := r.Header.Get("Content-Type"); got != "application/dns-message" {
			t.Errorf("upstream got CT %q, want application/dns-message", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read", http.StatusBadRequest)
			return
		}
		req := new(mdns.Msg)
		if err := req.Unpack(body); err != nil {
			http.Error(w, "unpack", http.StatusBadRequest)
			return
		}
		// Build a synthetic reply for the first question.
		resp := new(mdns.Msg)
		resp.SetReply(req)
		if len(req.Question) > 0 {
			q := req.Question[0]
			rr, err := mdns.NewRR(q.Name + " 60 IN A 203.0.113.7")
			if err == nil {
				resp.Answer = append(resp.Answer, rr)
			}
		}
		out, _ := resp.Pack()
		w.Header().Set("Content-Type", "application/dns-message")
		_, _ = w.Write(out)
	}))
	defer upstream.Close()

	c := NewDoHClient()
	// httptest.NewTLSServer uses a self-signed cert. Skip
	// verification for the duration of this test — we know we're
	// hitting our own server.
	c.hc.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
	}

	q := new(mdns.Msg)
	q.SetQuestion(mdns.Fqdn("example.com"), mdns.TypeA)
	resp, err := c.Exchange(context.Background(), q, upstream.URL+"/dns-query")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("answer count = %d", len(resp.Answer))
	}
	a, ok := resp.Answer[0].(*mdns.A)
	if !ok || a.A.String() != "203.0.113.7" {
		t.Errorf("answer: %+v", resp.Answer[0])
	}
}

func TestDoHExchangeRejectsNonHTTPS(t *testing.T) {
	c := NewDoHClient()
	q := new(mdns.Msg)
	q.SetQuestion(mdns.Fqdn("example.com"), mdns.TypeA)
	if _, err := c.Exchange(context.Background(), q, "http://example.com/dns-query"); err == nil {
		t.Error("Exchange: expected error for non-https upstream")
	}
}

