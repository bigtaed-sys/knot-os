package api

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/knot-os/knot-os/core/internal/subscription"
)

// SetSubscriptions wires the subscription registry + fetcher into
// the API. Pass nil to disable the endpoints (handlers respond with
// 503 in that case).
func (s *Server) SetSubscriptions(reg *subscription.Registry, fetcher *subscription.Fetcher) {
	s.subs = reg
	s.subFetcher = fetcher
}

// MountSubscriptions registers /subscriptions/* under the auth-gated
// group.
//
// Endpoints:
//
//	GET    /subscriptions                    — list all
//	POST   /subscriptions                    — add a URL-backed subscription
//	GET    /subscriptions/{id}               — one subscription with servers
//	PATCH  /subscriptions/{id}               — edit display name / URL / UA
//	DELETE /subscriptions/{id}               — remove (manual is reserved)
//	POST   /subscriptions/{id}/refresh       — re-fetch + re-parse
//	POST   /subscriptions/manual/uris        — paste a single share URI
//	DELETE /subscriptions/manual/uris/{srv}  — remove a manual server
func (s *Server) MountSubscriptions(r chi.Router) {
	if s.subs == nil {
		r.Get("/subscriptions", func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusServiceUnavailable, "subs_disabled",
				"subscription feature not enabled")
		})
		return
	}
	r.Get("/subscriptions", s.handleSubsList)
	r.Post("/subscriptions", s.handleSubsAdd)
	// Static path registered before the {id} wildcard so chi routes
	// "ping" to the prober, not to handleSubsGet with id="ping".
	r.Get("/subscriptions/ping", s.handlePingServers)
	r.Get("/subscriptions/{id}", s.handleSubsGet)
	r.Patch("/subscriptions/{id}", s.handleSubsPatch)
	r.Delete("/subscriptions/{id}", s.handleSubsDelete)
	r.Post("/subscriptions/{id}/refresh", s.handleSubsRefresh)
	r.Post("/subscriptions/manual/uris", s.handleSubsManualAdd)
	r.Delete("/subscriptions/manual/uris/{srv}", s.handleSubsManualRemove)
}

func (s *Server) handleSubsList(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"subscriptions": s.subs.List(),
	})
}

type subsAddRequest struct {
	DisplayName string `json:"display_name"`
	URL         string `json:"url"`
	UserAgent   string `json:"user_agent,omitempty"`
}

func (s *Server) handleSubsAdd(w http.ResponseWriter, r *http.Request) {
	var req subsAddRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	added, err := s.subs.Add(subscription.Subscription{
		DisplayName: req.DisplayName,
		URL:         req.URL,
		UserAgent:   req.UserAgent,
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_subscription", err.Error())
		return
	}
	if err := s.subs.FlushIfDirty(); err != nil {
		writeError(w, http.StatusInternalServerError, "save_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, added)
}

func (s *Server) handleSubsGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	got, ok := s.subs.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "subscription not found")
		return
	}
	writeJSON(w, http.StatusOK, got)
}

func (s *Server) handleSubsPatch(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req subsAddRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	updated, err := s.subs.Update(id, subscription.Subscription{
		DisplayName: req.DisplayName,
		URL:         req.URL,
		UserAgent:   req.UserAgent,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "update_failed", err.Error())
		return
	}
	if err := s.subs.FlushIfDirty(); err != nil {
		writeError(w, http.StatusInternalServerError, "save_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleSubsDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.subs.Remove(id); err != nil {
		// Manual-rejection vs not-found yield different surface text
		// but both reasonably collapse to 422 — clients should treat
		// the body as the source of truth.
		writeError(w, http.StatusUnprocessableEntity, "remove_failed", err.Error())
		return
	}
	if err := s.subs.FlushIfDirty(); err != nil {
		writeError(w, http.StatusInternalServerError, "save_failed", err.Error())
		return
	}
	// Removing a subscription drops its outbounds — rebuild routing so
	// any device pinned to one of them flips to the kill-switch instead
	// of silently leaking direct.
	s.fireConfigApplied(s.Snapshot())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSubsRefresh(w http.ResponseWriter, r *http.Request) {
	if s.subFetcher == nil {
		writeError(w, http.StatusServiceUnavailable, "fetcher_disabled",
			"subscription fetcher not configured")
		return
	}
	id := chi.URLParam(r, "id")
	if id == subscription.ManualID {
		writeError(w, http.StatusUnprocessableEntity, "manual_no_fetch",
			"manual subscription has no URL to fetch")
		return
	}
	res, parseErrs, err := s.subFetcher.FetchAndApply(r.Context(), s.subs, id)
	if err != nil {
		writeError(w, http.StatusBadGateway, "fetch_failed", err.Error())
		return
	}
	if err := s.subs.FlushIfDirty(); err != nil {
		writeError(w, http.StatusInternalServerError, "save_failed", err.Error())
		return
	}
	// A refresh can add, change, or drop servers; rebuild routing so
	// new outbounds become usable and vanished ones trip the
	// kill-switch on the devices that pointed at them.
	s.fireConfigApplied(s.Snapshot())
	got, _ := s.subs.Get(id)
	writeJSON(w, http.StatusOK, map[string]any{
		"subscription":          got,
		"subscription_userinfo": res.SubscriptionUserinfo,
		"profile_title":         res.ProfileTitle,
		"parse_warnings":        errsToStrings(parseErrs),
	})
}

type manualURIRequest struct {
	URI string `json:"uri"`
}

func (s *Server) handleSubsManualAdd(w http.ResponseWriter, r *http.Request) {
	var req manualURIRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	srv, err := s.subs.AddManualURI(req.URI)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_uri", err.Error())
		return
	}
	if err := s.subs.FlushIfDirty(); err != nil {
		writeError(w, http.StatusInternalServerError, "save_failed", err.Error())
		return
	}
	s.fireConfigApplied(s.Snapshot())
	writeJSON(w, http.StatusCreated, srv)
}

func (s *Server) handleSubsManualRemove(w http.ResponseWriter, r *http.Request) {
	srv := chi.URLParam(r, "srv")
	if err := s.subs.RemoveManualServer(srv); err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	if err := s.subs.FlushIfDirty(); err != nil {
		writeError(w, http.StatusInternalServerError, "save_failed", err.Error())
		return
	}
	s.fireConfigApplied(s.Snapshot())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// pingResult is one server's reachability probe outcome.
type pingResult struct {
	Tag       string `json:"tag"`
	OK        bool   `json:"ok"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
	Error     string `json:"error,omitempty"`
}

// handlePingServers TCP-connects to every subscription server's
// host:port and reports the round-trip time. This is a plain
// reachability/latency probe from the router itself — it does NOT
// perform a proxy handshake, so it works for every server regardless
// of whether the engine can speak its protocol (an xhttp server still
// gets a ping even though sing-box can't tunnel through it). Probes
// run concurrently with a small worker cap and a short per-dial
// timeout so the whole sweep returns in a couple seconds at most.
func (s *Server) handlePingServers(w http.ResponseWriter, r *http.Request) {
	outs := s.subs.AllOutbounds()
	results := make([]pingResult, len(outs))

	const (
		workers     = 16
		dialTimeout = 3 * time.Second
	)
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i, o := range outs {
		if o.Server == "" || o.Port <= 0 {
			results[i] = pingResult{Tag: o.Tag, Error: "noaddr"}
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, tag, addr string) {
			defer wg.Done()
			defer func() { <-sem }()
			start := time.Now()
			d := net.Dialer{Timeout: dialTimeout}
			conn, err := d.DialContext(r.Context(), "tcp", addr)
			if err != nil {
				results[i] = pingResult{Tag: tag, Error: shortDialError(err)}
				return
			}
			_ = conn.Close()
			results[i] = pingResult{Tag: tag, OK: true, LatencyMS: time.Since(start).Milliseconds()}
		}(i, o.Tag, net.JoinHostPort(o.Server, strconv.Itoa(o.Port)))
	}
	wg.Wait()

	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// shortDialError collapses a verbose net dial error into a one-word
// reason the UI can show in a badge.
func shortDialError(err error) string {
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return "timeout"
	}
	return "unreachable"
}

func errsToStrings(errs []error) []string {
	if len(errs) == 0 {
		return nil
	}
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		if e == nil {
			continue
		}
		out = append(out, e.Error())
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// preserve package import even if subscription happens to be stubbed.
var _ = errors.New
