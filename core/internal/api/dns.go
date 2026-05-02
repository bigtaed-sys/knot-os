package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	knotdns "github.com/knot-os/knot-os/core/internal/dns"
)

// dnsServices is the slim handle the API needs into the DNS layer.
// Concrete types live in main.go and core/internal/dns; the API
// only depends on this trio of capabilities.
type dnsServices struct {
	log         *knotdns.RingLog
	blocklists  *knotdns.Registry
	downloader  *knotdns.Downloader
}

// MountDNS registers /dns/* under the auth-gated group.
//
// Endpoints:
//
//	GET  /dns/stats                                — overall counters,
//	                                                 top blocked, blocklist
//	                                                 sizes, source download stats
//	GET  /dns/queries?limit=N&since=RFC3339        — recent queries (newest first)
//	POST /dns/refresh                              — force blocklist re-fetch
func (s *Server) MountDNS(r chi.Router) {
	if s.dns == nil || s.dns.log == nil || s.dns.blocklists == nil {
		r.Get("/dns/stats", func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusServiceUnavailable, "dns_disabled", "DNS resolver not configured")
		})
		return
	}
	r.Get("/dns/stats", s.handleDNSStats)
	r.Get("/dns/queries", s.handleDNSQueries)
	r.Post("/dns/refresh", s.handleDNSRefresh)
}

// SetDNSServices wires the in-memory DNS structures into the API.
// Called from main once the resolver is constructed. Pass nil
// pointers (or skip the call entirely) and the endpoints respond
// with 503.
func (s *Server) SetDNSServices(log *knotdns.RingLog, blocklists *knotdns.Registry, downloader *knotdns.Downloader) {
	s.dns = &dnsServices{
		log:        log,
		blocklists: blocklists,
		downloader: downloader,
	}
}

// dnsStatsResponse is the JSON shape returned by GET /dns/stats. Kept
// stable: the Protection UI in M12 binds against these field names.
type dnsStatsResponse struct {
	Queries      uint64                            `json:"queries"`
	Blocked      uint64                            `json:"blocked"`
	BlockedRatio float64                           `json:"blocked_ratio"`
	TopBlocked   []knotdns.TopBlocked              `json:"top_blocked"`
	BufferSize   int                               `json:"buffer_size"`
	BufferCap    int                               `json:"buffer_cap"`
	Blocklists   map[string]int                    `json:"blocklists"`
	Sources      map[string]knotdns.SourceStats    `json:"sources,omitempty"`
}

func (s *Server) handleDNSStats(w http.ResponseWriter, _ *http.Request) {
	stats := s.dns.log.Stats(10)
	resp := dnsStatsResponse{
		Queries:    stats.TotalQueries,
		Blocked:    stats.TotalBlocked,
		TopBlocked: stats.TopBlocked,
		BufferSize: stats.BufferSize,
		BufferCap:  stats.BufferCap,
		Blocklists: s.dns.blocklists.Sizes(),
	}
	if stats.TotalQueries > 0 {
		resp.BlockedRatio = float64(stats.TotalBlocked) / float64(stats.TotalQueries)
	}
	if s.dns.downloader != nil {
		resp.Sources = s.dns.downloader.Stats()
	}
	writeJSON(w, http.StatusOK, resp)
}

// dnsQueryEntry mirrors knotdns.QueryEvent with stable JSON tags.
type dnsQueryEntry struct {
	When      time.Time `json:"when"`
	SrcMAC    string    `json:"src_mac,omitempty"`
	SrcIP     string    `json:"src_ip"`
	QName     string    `json:"qname"`
	QType     string    `json:"qtype"`
	Blocked   bool      `json:"blocked"`
	BlockedBy string    `json:"blocked_by,omitempty"`
}

const (
	defaultDNSQueryLimit = 200
	maxDNSQueryLimit     = 2000
)

func (s *Server) handleDNSQueries(w http.ResponseWriter, r *http.Request) {
	limit := defaultDNSQueryLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be a positive integer")
			return
		}
		if n > maxDNSQueryLimit {
			n = maxDNSQueryLimit
		}
		limit = n
	}
	var since time.Time
	if v := r.URL.Query().Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_since", "since must be RFC3339")
			return
		}
		since = t
	}
	events := s.dns.log.Snapshot(limit, since)
	out := make([]dnsQueryEntry, len(events))
	for i, e := range events {
		out[i] = dnsQueryEntry{
			When:      e.When,
			SrcMAC:    e.SrcMAC,
			SrcIP:     e.SrcIP,
			QName:     e.QName,
			QType:     e.QType,
			Blocked:   e.Blocked,
			BlockedBy: e.BlockedBy,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"queries": out})
}

func (s *Server) handleDNSRefresh(w http.ResponseWriter, _ *http.Request) {
	if s.dns.downloader == nil {
		writeError(w, http.StatusServiceUnavailable, "downloader_disabled", "blocklist downloader not configured")
		return
	}
	s.dns.downloader.RefreshNow()
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}
