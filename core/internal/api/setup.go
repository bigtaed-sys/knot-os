package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/knot-os/knot-os/core/internal/auth"
	"github.com/knot-os/knot-os/core/internal/config"
)

// MountSetup registers the /setup/* endpoints used by the first-run
// wizard. They are reachable only while the server's current role is
// "setup"; once the wizard completes the role transitions and these
// routes start responding 410 Gone.
func (s *Server) MountSetup(r chi.Router) {
	r.Route("/setup", func(r chi.Router) {
		r.Use(s.requireSetupRole)
		r.Get("/scan", s.handleSetupScan)
		r.Post("/complete", s.handleSetupComplete)
	})
}

// requireSetupRole gates setup-only endpoints. Returns 410 Gone when
// the role is no longer "setup" so a UI cached on a phone gets a
// distinct, well-defined response (rather than 404, which would also
// fire for typos).
func (s *Server) requireSetupRole(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		role := s.cfg.Role
		s.mu.RUnlock()
		if role != config.RoleSetup {
			writeError(w, http.StatusGone, "setup_complete", "device is already configured")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleSetupScan(w http.ResponseWriter, r *http.Request) {
	networks, err := s.backend.Scan(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "scan_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"networks": networks,
	})
}

// completeRequest is the body of POST /api/setup/complete. It carries
// every value the wizard collects, plus a confirmed admin password.
type completeRequest struct {
	Device struct {
		Name    string `json:"name"`
		Country string `json:"country"`
	} `json:"device"`

	Password string `json:"password"`

	Uplink struct {
		SSID string `json:"ssid"`
		PSK  string `json:"psk"`
	} `json:"uplink"`

	AP struct {
		SSID string `json:"ssid"`
		PSK  string `json:"psk"`
		Band string `json:"band"`
	} `json:"ap"`
}

func (s *Server) handleSetupComplete(w http.ResponseWriter, r *http.Request) {
	var req completeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "weak_password", err.Error())
		return
	}

	// Build the post-wizard config. Carry over the LAN defaults from
	// the bootstrap config so the wizard doesn't have to ask about
	// IP ranges — that's an "advanced" concern, hidden in v0.1.
	s.mu.RLock()
	lan := s.cfg.Network.LAN
	s.mu.RUnlock()

	finished := config.Config{
		Device: config.Device{
			Name:    req.Device.Name,
			Country: req.Device.Country,
		},
		Role: config.RoleWiFiExtender,
		Auth: config.Auth{PasswordHash: hash},
		Network: config.Network{
			Uplink: &config.WiFiUplink{SSID: req.Uplink.SSID, PSK: req.Uplink.PSK},
			AP:     &config.WiFiAP{SSID: req.AP.SSID, PSK: req.AP.PSK, Band: req.AP.Band},
			LAN:    lan,
		},
	}
	if err := finished.Validate(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_config", err.Error())
		return
	}

	// Same Apply -> Save invariant as PUT /config: never persist a
	// config that the network stack rejected.
	if err := s.backend.Apply(r.Context(), finished); err != nil {
		writeError(w, http.StatusInternalServerError, "apply_failed", err.Error())
		return
	}
	if err := config.Save(s.configPath, finished); err != nil {
		writeError(w, http.StatusInternalServerError, "save_failed", err.Error())
		return
	}
	s.mu.Lock()
	s.cfg = finished
	s.mu.Unlock()

	// Issue a session immediately — the user is implicitly logged in
	// with the password they just set. They won't be redirected to a
	// login screen right after the wizard.
	sess, err := s.sessions.Issue()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session_failed", err.Error())
		return
	}
	setSessionCookie(w, sess.Token)

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"device": finished.Device.Name,
	})
}
