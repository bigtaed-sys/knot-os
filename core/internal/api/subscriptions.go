package api

import (
	"encoding/json"
	"errors"
	"net/http"

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
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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
