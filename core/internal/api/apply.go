package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// MountApply registers /apply/* under the auth-gated group.
//
// Endpoints:
//
//	GET /apply/recent — last N apply attempts (newest first), capped
//	                    at MaxHistory in the coordinator. Used by the
//	                    audit-log UI panel and Telegram /log.
//	GET /apply/{id}   — single attempt with full detail. The setup
//	                    wizard polls this during step 8 to render
//	                    progress + final status.
//	GET /apply/current — in-flight attempt, or 204 if idle. Cheap to
//	                    poll; the wizard hits this on a 1s interval
//	                    while waiting for an apply to terminate.
func (s *Server) MountApply(r chi.Router) {
	if s.applyCoord == nil {
		// No coordinator wired (tests, minimal dev) — endpoints
		// 503 with a hint rather than 404 so clients can detect
		// the difference.
		r.Get("/apply/recent", func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusServiceUnavailable, "apply_disabled",
				"transactional apply not enabled in this build")
		})
		return
	}
	r.Get("/apply/recent", s.handleApplyRecent)
	r.Get("/apply/current", s.handleApplyCurrent)
	r.Get("/apply/{id}", s.handleApplyGet)
}

func (s *Server) handleApplyRecent(w http.ResponseWriter, _ *http.Request) {
	atts := s.applyCoord.Recent(0)
	writeJSON(w, http.StatusOK, map[string]any{
		"attempts": atts,
	})
}

func (s *Server) handleApplyCurrent(w http.ResponseWriter, _ *http.Request) {
	cur := s.applyCoord.Current()
	if cur == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, cur)
}

func (s *Server) handleApplyGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	att := s.applyCoord.Get(id)
	if att == nil {
		writeError(w, http.StatusNotFound, "not_found",
			"no apply attempt with that ID (history is capped to the last "+
				"few attempts; older ones roll off)")
		return
	}
	writeJSON(w, http.StatusOK, att)
}
