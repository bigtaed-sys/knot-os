package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/knot-os/knot-os/core/internal/profile"
)

// MountProfiles registers /profiles/* under the auth-gated group.
//
// Endpoints:
//
//	GET    /profiles         — list all profiles (built-ins + user)
//	GET    /profiles/{id}    — single profile
//	PUT    /profiles/{id}    — create or update a profile
//	DELETE /profiles/{id}    — delete (built-ins refused with 409)
func (s *Server) MountProfiles(r chi.Router) {
	if s.profiles == nil {
		r.Get("/profiles", func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusServiceUnavailable, "profiles_disabled", "profile registry not configured")
		})
		return
	}
	r.Get("/profiles", s.handleListProfiles)
	r.Get("/profiles/{id}", s.handleGetProfile)
	r.Put("/profiles/{id}", s.handlePutProfile)
	r.Delete("/profiles/{id}", s.handleDeleteProfile)
}

// SetProfileRegistry attaches a registry to the server. Called from main.
func (s *Server) SetProfileRegistry(p *profile.Registry) {
	s.profiles = p
}

// SetSchedulerKick lets the API trigger an immediate scheduler tick
// after a device's profile changes (or a profile's schedule changes),
// so the user doesn't wait up to 30s for policy to take effect.
//
// Pass nil if no scheduler is wired (tests, dev mode without one).
func (s *Server) SetSchedulerKick(kick func()) {
	s.kickScheduler = kick
}

func (s *Server) kick() {
	if s.kickScheduler != nil {
		s.kickScheduler()
	}
}

func (s *Server) handleListProfiles(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"profiles": s.profiles.List(),
	})
}

func (s *Server) handleGetProfile(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := s.profiles.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "profile_not_found", "no such profile")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handlePutProfile(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var p profile.Profile
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	// URL parameter wins — we don't let the body claim a different ID.
	p.ID = id

	if err := s.profiles.Put(p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_profile", err.Error())
		return
	}
	if err := s.profiles.FlushIfDirty(); err != nil {
		writeError(w, http.StatusInternalServerError, "save_failed", err.Error())
		return
	}
	s.kick()

	out, _ := s.profiles.Get(id)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDeleteProfile(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.profiles.Delete(id); err != nil {
		writeError(w, http.StatusConflict, "delete_failed", err.Error())
		return
	}
	if err := s.profiles.FlushIfDirty(); err != nil {
		writeError(w, http.StatusInternalServerError, "save_failed", err.Error())
		return
	}
	s.kick()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
