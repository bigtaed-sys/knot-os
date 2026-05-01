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
}

// nopLookup is a DeviceLookup that knows nothing — used when the
// resolver runs without device-aware filtering (dev mode, single-user
// scenarios). Every query is treated as "no profile" -> no blocklist.
type nopLookup struct{}

func (nopLookup) BlocklistsForIP(_ net.IP) (string, []string, bool) {
	return "", nil, false
}

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
	// Upstreams is the list of "host:port" upstream resolvers tried
	// in order. Empty falls back to DefaultUpstreams.
	Upstreams []string
	// Blocklists is the registry the resolver looks blocklist names
	// up in. Required.
	Blocklists *Registry
	// Devices resolves source IPs to a profile's blocklists. Pass
	// nil to disable per-device filtering.
	Devices DeviceLookup
	// Log receives one QueryEvent per query. Pass nil to discard.
	Log QueryLog
	// Logger receives operational messages (start/stop, errors).
	Logger *log.Logger
}

// Server is the running DNS resolver. Construct with New and start
// with Run; one goroutine per protocol (UDP+TCP) is launched.
type Server struct {
	opts Options
	udp  *mdns.Server
	tcp  *mdns.Server
}

// New constructs a Server (does not start listening).
func New(opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = log.Default()
	}
	if len(opts.Upstreams) == 0 {
		opts.Upstreams = append([]string(nil), DefaultUpstreams...)
	}
	if opts.Devices == nil {
		opts.Devices = nopLookup{}
	}
	if opts.Log == nil {
		opts.Log = nopLog{}
	}
	return &Server{opts: opts}
}

// Run starts the listeners and blocks until ctx is cancelled. Both
// UDP and TCP are bound — DNS clients fall back to TCP for large
// responses.
func (s *Server) Run(ctx context.Context) error {
	if s.opts.Listen == "" {
		s.opts.Logger.Printf("dns: listen address empty, resolver disabled")
		<-ctx.Done()
		return nil
	}
	if s.opts.Blocklists == nil {
		return fmt.Errorf("dns.Server: Blocklists registry is required")
	}

	handler := mdns.HandlerFunc(s.handle)
	s.udp = &mdns.Server{Addr: s.opts.Listen, Net: "udp", Handler: handler}
	s.tcp = &mdns.Server{Addr: s.opts.Listen, Net: "tcp", Handler: handler}

	var wg sync.WaitGroup
	wg.Add(2)
	errCh := make(chan error, 2)
	go func() {
		defer wg.Done()
		if err := s.udp.ListenAndServe(); err != nil {
			errCh <- fmt.Errorf("udp: %w", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := s.tcp.ListenAndServe(); err != nil {
			errCh <- fmt.Errorf("tcp: %w", err)
		}
	}()

	s.opts.Logger.Printf("dns: listening on %s (udp+tcp), upstreams=%s",
		s.opts.Listen, strings.Join(s.opts.Upstreams, ","))

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.udp.ShutdownContext(shutdownCtx)
		_ = s.tcp.ShutdownContext(shutdownCtx)
		wg.Wait()
		return nil
	case err := <-errCh:
		return err
	}
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

	resp, err := s.forward(r)
	if err != nil {
		s.opts.Logger.Printf("dns: forward %s from %s:%d: %v", qname, srcIP, srcPort, err)
		s.replyServFail(w, r)
		return
	}
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
	c := &mdns.Client{Net: "udp", Timeout: 4 * time.Second}
	var lastErr error
	for _, up := range s.opts.Upstreams {
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
