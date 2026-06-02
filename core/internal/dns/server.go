package dns

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	mdns "github.com/miekg/dns"
)

// Defaults — overridable from Options.
var (
	DefaultUpstreams = []string{"1.1.1.1:53", "8.8.8.8:53"}
)

// DeviceLookup resolves a source IP into the blocklist names that
// apply to that device, plus the device MAC for query-log
// attribution. Decoupled from deviceregistry/profile so the package
// has no upward dependencies.
type DeviceLookup interface {
	BlocklistsForIP(ip net.IP) (mac string, blocklists []string, ok bool)
	// SafeSearchForIP reports whether the device at ip is on a
	// profile that enforces SafeSearch. Checked per query on the hot
	// path, so the implementation should be cheap.
	SafeSearchForIP(ip net.IP) bool
}

// CaptiveLookup decides, per source IP, whether the device should be
// captive-redirected to the router's landing page. When ok is true,
// the resolver answers every A query with `ip` (the router's LAN
// address) and NODATA for everything else, so any hostname the device
// opens lands on knotd — the "blocked / awaiting approval" page. Used
// for the per-device pause + quarantine landing screen.
type CaptiveLookup interface {
	CaptiveIP(srcIP net.IP) (ip net.IP, ok bool)
}

// nopLookup is a DeviceLookup that knows nothing — used when the
// resolver runs without device-aware filtering (dev mode, single-user
// scenarios). Every query is treated as "no profile" -> no blocklist.
type nopLookup struct{}

func (nopLookup) BlocklistsForIP(_ net.IP) (string, []string, bool) {
	return "", nil, false
}

func (nopLookup) SafeSearchForIP(_ net.IP) bool { return false }

// QueryEvent is what the resolver emits to the optional QueryLog after
// each request. Fields are intentionally narrow so a high-traffic LAN
// doesn't produce a fire hose of large structs.
type QueryEvent struct {
	When    time.Time
	SrcMAC  string // empty if unknown
	SrcIP   string
	QName   string
	QType   string // "A", "AAAA", "HTTPS", ...
	Blocked bool
	BlockedBy string // blocklist name, when Blocked is true
}

// QueryLog is the consumer side. The simplest implementation just
// pushes into a ring buffer; M11d builds on this.
type QueryLog interface {
	Append(QueryEvent)
}

// nopLog discards events.
type nopLog struct{}

func (nopLog) Append(_ QueryEvent) {}

// Options configures a Server.
type Options struct {
	// Listen is the address+port to bind ("192.168.42.1:53"). Empty
	// disables the resolver entirely (useful in dev / tests).
	Listen string
	// UpstreamMode picks the wire transport for upstream queries.
	// "udp" (default) keeps the v0.2 behaviour of plain RFC 1035.
	// "doh" runs every query over HTTPS to a provider's
	// /dns-query endpoint.
	UpstreamMode UpstreamMode
	// Upstreams is the list of upstream resolvers tried in order.
	// Format depends on UpstreamMode:
	//
	//   udp → "host:port"  (e.g. "1.1.1.1:53")
	//   doh → full URL     (e.g. "https://cloudflare-dns.com/dns-query")
	//
	// Empty falls back to the matching default list.
	Upstreams []string
	// Blocklists is the registry the resolver looks blocklist names
	// up in. Required.
	Blocklists *Registry
	// Devices resolves source IPs to a profile's blocklists. Pass
	// nil to disable per-device filtering.
	Devices DeviceLookup
	// Captive, when non-nil, redirects blocked devices' DNS to the
	// router's landing page. Pass nil to disable.
	Captive CaptiveLookup
	// Log receives one QueryEvent per query. Pass nil to discard.
	Log QueryLog
	// Cache, when non-nil, is consulted before forwarding. Allowed
	// (NOERROR-with-answers, NXDOMAIN) responses are stored back.
	// Blocklist NXDOMAINs do not enter the cache because the answer
	// depends on the source's profile.
	Cache *Cache
	// Logger receives operational messages (start/stop, errors).
	Logger *log.Logger
}

// Server is the running DNS resolver. Construct with New and start
// with Run; one goroutine per protocol (UDP+TCP) is launched.
//
// The listen address is mutable via SetListen — useful when the role
// flips between setup (dnsmasq still owns 53 for the captive portal)
// and wifi-extender (knotd takes 53 over). SetListen restarts the
// internal listeners idempotently.
type Server struct {
	opts Options

	// doh is the DoH client used when UpstreamMode == UpstreamModeDoH.
	// Holds a TLS+HTTP/2 connection pool so per-query latency on hot
	// upstreams is one RTT.
	doh *DoHClient

	mu     sync.Mutex
	listen string
	udp    *mdns.Server
	tcp    *mdns.Server
}

// New constructs a Server (does not start listening).
func New(opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = log.Default()
	}
	if opts.UpstreamMode == "" {
		opts.UpstreamMode = UpstreamModeUDP
	}
	if len(opts.Upstreams) == 0 {
		switch opts.UpstreamMode {
		case UpstreamModeDoH:
			opts.Upstreams = append([]string(nil), DefaultDoHUpstreams...)
		default:
			opts.Upstreams = append([]string(nil), DefaultUpstreams...)
		}
	}
	if opts.Devices == nil {
		opts.Devices = nopLookup{}
	}
	if opts.Log == nil {
		opts.Log = nopLog{}
	}
	s := &Server{opts: opts, listen: opts.Listen}
	if opts.UpstreamMode == UpstreamModeDoH {
		s.doh = NewDoHClient()
	}
	return s
}

// SetUpstreams atomically swaps mode + upstreams at runtime. Used
// by the API when the user picks a different DNS provider in the
// UI — the resolver picks up the change on the very next query.
func (s *Server) SetUpstreams(mode UpstreamMode, upstreams []string) {
	if mode == "" {
		mode = UpstreamModeUDP
	}
	s.mu.Lock()
	s.opts.UpstreamMode = mode
	if len(upstreams) == 0 {
		switch mode {
		case UpstreamModeDoH:
			upstreams = append([]string(nil), DefaultDoHUpstreams...)
		default:
			upstreams = append([]string(nil), DefaultUpstreams...)
		}
	}
	s.opts.Upstreams = append([]string(nil), upstreams...)
	if mode == UpstreamModeDoH && s.doh == nil {
		s.doh = NewDoHClient()
	}
	s.mu.Unlock()
	s.opts.Logger.Printf("dns: upstreams switched to mode=%s, %d entries", mode, len(upstreams))
}

// Run blocks until ctx is cancelled, keeping the listeners (if any)
// alive. The server starts immediately if Options.Listen is set;
// otherwise it idles until SetListen is called.
func (s *Server) Run(ctx context.Context) error {
	if s.opts.Blocklists == nil {
		return fmt.Errorf("dns.Server: Blocklists registry is required")
	}
	if s.listen != "" {
		if err := s.startLocked(); err != nil {
			s.opts.Logger.Printf("dns: initial listen on %s failed: %v", s.listen, err)
		}
	}
	<-ctx.Done()
	s.stopLocked()
	return nil
}

// SetListen swaps the listen address. Empty stops the resolver.
// Calling with the same address as currently active is a no-op.
func (s *Server) SetListen(addr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if addr == s.listen && (addr == "" || s.udp != nil) {
		return
	}
	s.listen = addr
	s.stopRunningLocked()
	if addr == "" {
		s.opts.Logger.Printf("dns: resolver stopped (no listen address)")
		return
	}
	if err := s.startLocked(); err != nil {
		s.opts.Logger.Printf("dns: listen on %s failed: %v", addr, err)
	}
}

// startLocked spawns UDP+TCP listeners. Caller must hold s.mu OR be
// the constructor's startup path (where no other goroutines see s yet).
func (s *Server) startLocked() error {
	addr := s.listen
	if addr == "" {
		return nil
	}
	handler := mdns.HandlerFunc(s.handle)
	udp := &mdns.Server{Addr: addr, Net: "udp", Handler: handler}
	tcp := &mdns.Server{Addr: addr, Net: "tcp", Handler: handler}

	udpReady := make(chan error, 1)
	tcpReady := make(chan error, 1)
	udp.NotifyStartedFunc = func() { udpReady <- nil }
	tcp.NotifyStartedFunc = func() { tcpReady <- nil }

	go func() {
		if err := udp.ListenAndServe(); err != nil {
			s.opts.Logger.Printf("dns: udp listener exited: %v", err)
		}
	}()
	go func() {
		if err := tcp.ListenAndServe(); err != nil {
			s.opts.Logger.Printf("dns: tcp listener exited: %v", err)
		}
	}()

	// Wait briefly for both to come up so SetListen returns after the
	// listener is actually accepting traffic.
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()
	select {
	case <-udpReady:
	case <-timeout.C:
		_ = udp.Shutdown()
		return fmt.Errorf("udp listener did not start within 2s")
	}
	timeout.Reset(2 * time.Second)
	select {
	case <-tcpReady:
	case <-timeout.C:
		_ = udp.Shutdown()
		_ = tcp.Shutdown()
		return fmt.Errorf("tcp listener did not start within 2s")
	}

	s.udp = udp
	s.tcp = tcp
	s.opts.Logger.Printf("dns: listening on %s (udp+tcp), upstreams=%s",
		addr, strings.Join(s.opts.Upstreams, ","))
	return nil
}

func (s *Server) stopRunningLocked() {
	if s.udp == nil && s.tcp == nil {
		return
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if s.udp != nil {
		_ = s.udp.ShutdownContext(shutdownCtx)
	}
	if s.tcp != nil {
		_ = s.tcp.ShutdownContext(shutdownCtx)
	}
	s.udp = nil
	s.tcp = nil
}

func (s *Server) stopLocked() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopRunningLocked()
}

// handle is the per-request hot path. It checks the blocklist, then
// either replies NXDOMAIN or forwards upstream.
func (s *Server) handle(w mdns.ResponseWriter, r *mdns.Msg) {
	if len(r.Question) == 0 {
		// Empty queries are protocol errors; just drop.
		return
	}
	q := r.Question[0]
	qname := normalizeDomain(q.Name)
	srcIP, srcPort := splitAddr(w.RemoteAddr())

	// Captive landing: a blocked device's every lookup resolves to the
	// router so any site it opens shows the "blocked / awaiting
	// approval" page. Takes precedence over normal resolution.
	if s.opts.Captive != nil {
		if gw, ok := s.opts.Captive.CaptiveIP(srcIP); ok {
			s.replyCaptive(w, r, gw)
			s.opts.Log.Append(QueryEvent{
				When: time.Now(), SrcIP: srcIP.String(),
				QName: qname, QType: mdns.TypeToString[q.Qtype],
				Blocked: true, BlockedBy: "captive",
			})
			return
		}
	}

	mac, blocklists, _ := s.opts.Devices.BlocklistsForIP(srcIP)

	if blocklistName := s.findBlocking(qname, blocklists); blocklistName != "" {
		s.replyNXDOMAIN(w, r)
		s.opts.Log.Append(QueryEvent{
			When: time.Now(), SrcMAC: mac, SrcIP: srcIP.String(),
			QName: qname, QType: mdns.TypeToString[q.Qtype],
			Blocked: true, BlockedBy: blocklistName,
		})
		return
	}

	// SafeSearch: rewrite the major search engines / YouTube to their
	// enforcement hostnames for devices whose profile enables it.
	// Checked before the cache because the cache is shared across
	// devices and the rewrite is per-profile.
	if s.opts.Devices.SafeSearchForIP(srcIP) {
		if target, ok := safeSearchTarget(qname); ok {
			s.replySafeSearch(w, r, target)
			s.opts.Log.Append(QueryEvent{
				When: time.Now(), SrcMAC: mac, SrcIP: srcIP.String(),
				QName: qname, QType: mdns.TypeToString[q.Qtype],
				Blocked: true, BlockedBy: "safesearch",
			})
			return
		}
	}

	if cached, ok := s.opts.Cache.Get(r); ok {
		if err := w.WriteMsg(cached); err != nil {
			s.opts.Logger.Printf("dns: write cached reply: %v", err)
		}
		s.opts.Log.Append(QueryEvent{
			When: time.Now(), SrcMAC: mac, SrcIP: srcIP.String(),
			QName: qname, QType: mdns.TypeToString[q.Qtype],
		})
		return
	}

	resp, err := s.forward(r)
	if err != nil {
		s.opts.Logger.Printf("dns: forward %s from %s:%d: %v", qname, srcIP, srcPort, err)
		s.replyServFail(w, r)
		return
	}
	s.opts.Cache.Set(r, resp)
	if err := w.WriteMsg(resp); err != nil {
		s.opts.Logger.Printf("dns: write reply: %v", err)
	}
	s.opts.Log.Append(QueryEvent{
		When: time.Now(), SrcMAC: mac, SrcIP: srcIP.String(),
		QName: qname, QType: mdns.TypeToString[q.Qtype],
	})
}

// findBlocking walks the configured blocklists and returns the name
// of the first that contains qname (or any parent), else "".
func (s *Server) findBlocking(qname string, blocklists []string) string {
	if len(blocklists) == 0 {
		return ""
	}
	for _, name := range blocklists {
		l, ok := s.opts.Blocklists.Get(name)
		if !ok {
			continue
		}
		if l.Contains(qname) {
			return name
		}
	}
	return ""
}

func (s *Server) forward(r *mdns.Msg) (*mdns.Msg, error) {
	s.mu.Lock()
	mode := s.opts.UpstreamMode
	upstreams := append([]string(nil), s.opts.Upstreams...)
	doh := s.doh
	s.mu.Unlock()

	if mode == UpstreamModeDoH {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		var lastErr error
		for _, up := range upstreams {
			resp, err := doh.Exchange(ctx, r, up)
			if err == nil {
				return resp, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}

	// Default: plain UDP RFC 1035.
	c := &mdns.Client{Net: "udp", Timeout: 4 * time.Second}
	var lastErr error
	for _, up := range upstreams {
		resp, _, err := c.Exchange(r, up)
		if err == nil {
			return resp, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func (s *Server) replyNXDOMAIN(w mdns.ResponseWriter, r *mdns.Msg) {
	resp := new(mdns.Msg)
	resp.SetRcode(r, mdns.RcodeNameError) // NXDOMAIN
	_ = w.WriteMsg(resp)
}

// replyCaptive answers an A query with the router's IP (short TTL) and
// NODATA for anything else, forcing the client onto the landing page
// over IPv4.
func (s *Server) replyCaptive(w mdns.ResponseWriter, r *mdns.Msg, ip net.IP) {
	resp := new(mdns.Msg)
	resp.SetReply(r)
	resp.Authoritative = true
	q := r.Question[0]
	if q.Qtype == mdns.TypeA && ip.To4() != nil {
		if rr, err := mdns.NewRR(fmt.Sprintf("%s 30 IN A %s", q.Name, ip.String())); err == nil {
			resp.Answer = append(resp.Answer, rr)
		}
	}
	// AAAA / HTTPS / etc. → NOERROR with no answer (NODATA), so the
	// client falls back to the captive A record.
	_ = w.WriteMsg(resp)
}

// replySafeSearch answers a search-engine query with a CNAME to the
// provider's enforcement host plus that host's resolved addresses.
// A/AAAA queries get the CNAME + forwarded answers; HTTPS/SVCB and
// everything else get NODATA so the client falls back to A/AAAA.
func (s *Server) replySafeSearch(w mdns.ResponseWriter, r *mdns.Msg, target string) {
	q := r.Question[0]
	resp := new(mdns.Msg)
	resp.SetReply(r)
	resp.Authoritative = true

	if q.Qtype == mdns.TypeA || q.Qtype == mdns.TypeAAAA {
		if cn, err := mdns.NewRR(fmt.Sprintf("%s 300 IN CNAME %s.", q.Name, target)); err == nil {
			resp.Answer = append(resp.Answer, cn)
		}
		// Resolve the enforcement host's addresses upstream and splice
		// them in under the CNAME. Best-effort: on failure the client
		// still gets the CNAME and re-queries the target itself.
		probe := new(mdns.Msg)
		probe.SetQuestion(mdns.Fqdn(target), q.Qtype)
		probe.RecursionDesired = true
		if up, err := s.forward(probe); err == nil && up != nil {
			resp.Answer = append(resp.Answer, up.Answer...)
		}
	}
	_ = w.WriteMsg(resp)
}

func (s *Server) replyServFail(w mdns.ResponseWriter, r *mdns.Msg) {
	resp := new(mdns.Msg)
	resp.SetRcode(r, mdns.RcodeServerFailure)
	_ = w.WriteMsg(resp)
}

// splitAddr extracts the IP and port from a net.Addr, working for
// both UDPAddr and TCPAddr.
func splitAddr(a net.Addr) (net.IP, int) {
	switch v := a.(type) {
	case *net.UDPAddr:
		return v.IP, v.Port
	case *net.TCPAddr:
		return v.IP, v.Port
	default:
		return nil, 0
	}
}
