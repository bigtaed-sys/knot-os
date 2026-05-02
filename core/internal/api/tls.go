package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	knottls "github.com/knot-os/knot-os/core/internal/tls"
)

// MountTLS registers /tls/* under the auth-gated group.
//
// Endpoints:
//
//	GET  /tls/info       — fingerprints, expiry, SANs of the
//	                       active leaf and the device root.
//	POST /tls/regenerate — re-issue the leaf using whatever
//	                       LeafSubject the daemon currently
//	                       computes from config (called when the
//	                       device hostname or LAN gateway changes
//	                       and the user wants the cert to catch up
//	                       without a full reboot).
//
// The root cert download itself is *not* under /api — it lives at
// /setup-ca.crt on the public allowlist so a user mid-onboarding
// can fetch it without clearing a browser warning first.
func (s *Server) MountTLS(r chi.Router) {
	if s.tls == nil {
		r.Get("/tls/info", func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusServiceUnavailable, "tls_disabled", "TLS materials not configured")
		})
		return
	}
	r.Get("/tls/info", s.handleTLSInfo)
	r.Post("/tls/regenerate", s.handleTLSRegenerate)
}

// SetTLSMaterials wires the daemon's TLS materials into the API.
// Pass nil (or skip the call) and the endpoints respond 503.
//
// regenerateSubject is invoked when the user asks for a re-issue;
// it must return the LeafSubject the daemon currently believes is
// correct (so the new cert covers the right SANs). Kept as a
// callback rather than a snapshot because role transitions can
// move the gateway IP between the time the API server is
// constructed and the time the user clicks Regenerate.
func (s *Server) SetTLSMaterials(m *knottls.Materials, regenerateSubject func() knottls.LeafSubject) {
	s.tls = m
	s.tlsSubject = regenerateSubject
}

func (s *Server) handleTLSInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.tls.Snapshot())
}

func (s *Server) handleTLSRegenerate(w http.ResponseWriter, _ *http.Request) {
	subj := knottls.LeafSubject{}
	if s.tlsSubject != nil {
		subj = s.tlsSubject()
	}
	if err := s.tls.Regenerate(subj); err != nil {
		writeError(w, http.StatusInternalServerError, "regenerate_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.tls.Snapshot())
}
