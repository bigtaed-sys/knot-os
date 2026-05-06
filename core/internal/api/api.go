// Package api implements the REST API surface served under /api/* by knotd.
//
// Endpoint groups:
//   - public:    /api/status, /api/auth/login    — no session required
//   - protected: /api/config, /api/auth/logout   — session cookie required
//   - setup:     /api/setup/*                    — only when role=setup;
//                added by setupapi.Mount in a separate file.
//
// Errors are returned in a uniform shape:
//
//	{ "error": { "code": "...", "message": "..." } }
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/knot-os/knot-os/core/internal/auth"
	"github.com/knot-os/knot-os/core/internal/config"
	"github.com/knot-os/knot-os/core/internal/deviceregistry"
	"github.com/knot-os/knot-os/core/internal/network"
	"github.com/knot-os/knot-os/core/internal/plugin"
	"github.com/knot-os/knot-os/core/internal/profile"
	knottls "github.com/knot-os/knot-os/core/internal/tls"
	"github.com/knot-os/knot-os/core/internal/update"
	"github.com/knot-os/knot-os/core/internal/guest"
	"github.com/knot-os/knot-os/core/internal/vpn"
)

// ErrConfigNotInitialized is returned when the API is asked for config state
// before the server has loaded one. It indicates a programming error.
var ErrConfigNotInitialized = errors.New("api: config not initialized")

// Server holds API state and produces an http.Handler. The zero value is
// not usable; construct via New.
type Server struct {
	configPath      string
	version         string
	backend         network.Backend
	sessions        *auth.Sessions
	plugins         *plugin.Registry
	devices         *deviceregistry.Registry
	profiles        *profile.Registry
	dns             *dnsServices
	tls             *knottls.Materials
	tlsSubject      func() knottls.LeafSubject
	sealer          config.Sealer
	updater         *update.Manager
	rescue          *update.Rescue
	vpn             *vpn.Registry
	guest           *guest.Registry
	kickScheduler   func()
	onConfigApplied func(config.Config)
	production      bool

	mu  sync.RWMutex
	cfg config.Config
}

// Options is the constructor input.
type Options struct {
	ConfigPath string
	Initial    config.Config
	Version    string
	Backend    network.Backend
	Sessions   *auth.Sessions
	Plugins    *plugin.Registry
}

// New constructs a Server.
func New(opts Options) *Server {
	if opts.Sessions == nil {
		opts.Sessions = auth.NewSessions()
	}
	return &Server{
		configPath: opts.ConfigPath,
		version:    opts.Version,
		backend:    opts.Backend,
		sessions:   opts.Sessions,
		plugins:    opts.Plugins,
		cfg:        opts.Initial,
	}
}

// SetSealer wires the at-rest secret sealer used by config writes.
// Pass nil (the default) to write configs as plaintext — that's
// what dev mode and the v0.1/v0.2 tests use.
func (s *Server) SetSealer(sl config.Sealer) { s.sealer = sl }

// SetOnConfigApplied registers a callback that fires after every
// successful config application — PUT /api/config and POST
// /api/setup/complete both call it. Used by main.go to react to
// role changes (e.g. start/stop the DNS resolver when entering or
// leaving wifi-extender mode).
func (s *Server) SetOnConfigApplied(fn func(config.Config)) {
	s.onConfigApplied = fn
}

func (s *Server) fireConfigApplied(cfg config.Config) {
	if s.onConfigApplied != nil {
		s.onConfigApplied(cfg)
	}
}

// FireConfigApplied is the public entry point for callers outside
// the api package (the guest-session expiry watcher in main.go,
// for example) that want to nudge the apply chain without going
// through PUT /api/config. Picks up the current snapshot under
// the lock and invokes the registered callback.
func (s *Server) FireConfigApplied() {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()
	s.fireConfigApplied(cfg)
}

// Handler returns the http.Handler to mount at /api.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(jsonContentType)
	r.NotFound(notFound)
	r.MethodNotAllowed(methodNotAllowed)

	// Public endpoints — reachable without authentication.
	r.Get("/status", s.handleStatus)
	r.Post("/auth/login", s.handleLogin)

	// Protected group — middleware applies only to routes registered
	// inside the closure. Unknown paths still hit the outer NotFound,
	// not the auth middleware.
	r.Group(func(r chi.Router) {
		r.Use(s.sessions.Middleware(unauthorizedJSON))
		r.Get("/config", s.handleGetConfig)
		r.Put("/config", s.handlePutConfig)
		r.Post("/auth/logout", s.handleLogout)
		r.Get("/auth/me", s.handleMe)
		s.MountPlugins(r)
		s.MountSystem(r)
		s.MountDevices(r)
		s.MountProfiles(r)
		s.MountDNS(r)
		s.MountTLS(r)
		s.MountVPN(r)
		s.MountGuest(r)
		s.MountChannels(r)
	})

	// Setup endpoints — gated by role inside the handler, no auth
	// required. Available only while role == "setup".
	s.MountSetup(r)

	return r
}

// Snapshot returns the current in-memory config. Callers must not mutate it.
func (s *Server) Snapshot() config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// SetConfig atomically swaps the in-memory config. Used by the setup
// flow to publish the final config after the wizard runs. The caller is
// responsible for persisting to disk first.
func (s *Server) SetConfig(c config.Config) {
	s.mu.Lock()
	s.cfg = c
	s.mu.Unlock()
}

// ConfigPath returns the on-disk path the API persists to. Used by the
// setup handler so it writes to the same place.
func (s *Server) ConfigPath() string { return s.configPath }

// Backend returns the active network backend. Exposed so the setup
// handler can scan Wi-Fi and apply the final config without holding a
// duplicate reference.
func (s *Server) Backend() network.Backend { return s.backend }

// Sessions returns the session store so the setup handler can issue a
// fresh session immediately after the wizard finishes.
func (s *Server) Sessions() *auth.Sessions { return s.sessions }

// --- public handlers --------------------------------------------------------

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	name := s.cfg.Device.Name
	role := s.cfg.Role
	authConfigured := s.cfg.Auth.PasswordHash != ""
	s.mu.RUnlock()

	netStatus, err := s.backend.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "backend_status_failed", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"version":         s.version,
		"device":          name,
		"role":            role,
		"auth_configured": authConfigured,
		"network":         netStatus,
	})
}

type loginRequest struct {
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	s.mu.RLock()
	hash := s.cfg.Auth.PasswordHash
	s.mu.RUnlock()

	if hash == "" {
		// Auth not yet configured — login is meaningless. The UI should
		// be on the setup wizard instead.
		writeError(w, http.StatusConflict, "not_configured", "admin password is not set; complete the setup wizard first")
		return
	}
	if err := auth.CheckPassword(hash, req.Password); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "wrong password")
		return
	}

	sess, err := s.sessions.Issue()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session_failed", err.Error())
		return
	}
	setSessionCookie(w, sess.Token)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- protected handlers ------------------------------------------------------

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

	// Auth fields are not transmitted over the API (json:"-") so the
	// incoming config has Auth zeroed. Carry over the existing hash
	// from the in-memory state to avoid wiping it on every save.
	s.mu.RLock()
	incoming.Auth = s.cfg.Auth
	s.mu.RUnlock()

	if err := incoming.Validate(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_config", err.Error())
		return
	}
	if err := s.backend.Apply(r.Context(), incoming); err != nil {
		writeError(w, http.StatusInternalServerError, "apply_failed", err.Error())
		return
	}
	if err := config.SaveWith(s.configPath, incoming, s.sealer); err != nil {
		writeError(w, http.StatusInternalServerError, "save_failed", err.Error())
		return
	}
	s.mu.Lock()
	s.cfg = incoming
	s.mu.Unlock()
	s.fireConfigApplied(incoming)
	writeJSON(w, http.StatusOK, incoming)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(auth.CookieName); err == nil {
		s.sessions.Revoke(c.Value)
	}
	clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	sess, ok := auth.FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no_session", "no session attached")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"created_at": sess.CreatedAt.UTC(),
		"expires_at": sess.ExpiresAt.UTC(),
	})
}

// --- helpers -----------------------------------------------------------------

func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		// Secure not set: knotd serves HTTP on port 80 inside the LAN
		// for v0.1. HTTPS lands later, then this becomes Secure: true.
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

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

func unauthorizedJSON(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
}
