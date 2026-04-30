// Package api implements the REST API surface served under /api/* by knotd.
//
// The API is JSON over HTTP. All requests and responses use UTF-8 JSON.
// Errors are returned in a consistent shape:
//
//	{ "error": { "code": "...", "message": "..." } }
//
// In M2 the API exposes the configuration document and a status endpoint.
// Auth is added in M4.
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/knot-os/knot-os/core/internal/config"
	"github.com/knot-os/knot-os/core/internal/network"
)

// ErrConfigNotInitialized is returned when the API is asked for config state
// before the server has loaded one. It indicates a programming error.
var ErrConfigNotInitialized = errors.New("api: config not initialized")

// Server holds API state and produces an http.Handler. The zero value is
// not usable; construct via New.
type Server struct {
	configPath string
	version    string
	backend    network.Backend

	mu  sync.RWMutex
	cfg config.Config
}

// Options is the constructor input.
type Options struct {
	// ConfigPath is the on-disk location of config.yaml. The API persists
	// changes here.
	ConfigPath string
	// Initial is the in-memory config snapshot loaded at startup.
	Initial config.Config
	// Version is the running knotd version, surfaced via /api/status.
	Version string
	// Backend is the network backend used to apply config changes and
	// report runtime status. Required.
	Backend network.Backend
}

// New constructs a Server.
func New(opts Options) *Server {
	return &Server{
		configPath: opts.ConfigPath,
		version:    opts.Version,
		backend:    opts.Backend,
		cfg:        opts.Initial,
	}
}

// Handler returns the http.Handler to mount at /api.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(jsonContentType)
	r.NotFound(notFound)
	r.MethodNotAllowed(methodNotAllowed)

	r.Get("/status", s.handleStatus)
	r.Get("/config", s.handleGetConfig)
	r.Put("/config", s.handlePutConfig)

	return r
}

// Snapshot returns a deep-ish copy of the current config. Maps are shared
// for now — callers must not mutate the returned value.
func (s *Server) Snapshot() config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// --- handlers ----------------------------------------------------------------

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	name := s.cfg.Device.Name
	configRole := s.cfg.Role
	s.mu.RUnlock()

	netStatus, err := s.backend.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "backend_status_failed", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"version":     s.version,
		"device":      name,
		"role":        configRole,
		"network":     netStatus,
	})
}

func (s *Server) handleGetConfig(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	writeJSON(w, http.StatusOK, s.cfg)
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	var incoming config.Config
	if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := incoming.Validate(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_config", err.Error())
		return
	}

	// Apply to the network stack first; if that fails, do not persist —
	// keeping disk and live state divergent would be much worse than
	// rejecting the change. On success, persist atomically.
	if err := s.backend.Apply(r.Context(), incoming); err != nil {
		writeError(w, http.StatusInternalServerError, "apply_failed", err.Error())
		return
	}
	if err := config.Save(s.configPath, incoming); err != nil {
		writeError(w, http.StatusInternalServerError, "save_failed", err.Error())
		return
	}
	s.mu.Lock()
	s.cfg = incoming
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, incoming)
}

// --- helpers -----------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func jsonContentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		next.ServeHTTP(w, r)
	})
}

func notFound(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotFound, "not_found", "no such API endpoint")
}

func methodNotAllowed(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed for this endpoint")
}
