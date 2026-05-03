package api

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/knot-os/knot-os/core/internal/auth"
	"github.com/knot-os/knot-os/core/internal/config"
	"github.com/knot-os/knot-os/core/internal/network/capability"
)

// MountSetup registers the /setup/* endpoints used by the first-run
// wizard. They are reachable only while the server's current role is
// "setup"; once the wizard completes the role transitions and these
// routes start responding 410 Gone.
func (s *Server) MountSetup(r chi.Router) {
	r.Route("/setup", func(r chi.Router) {
		r.Use(s.requireSetupRole)
		r.Get("/scan", s.handleSetupScan)
		r.Get("/capability", s.handleSetupCapability)
		r.Post("/complete", s.handleSetupComplete)
	})
}

// handleSetupCapability runs the hardware probe and returns it.
// The wizard uses the result to decide whether to show the
// "extender vs full router" role picker (only when at least one
// USB-Eth adapter is detected) and the guest-AP step (Pi 4/5).
//
// Each call runs the probe live — no caching, so a user who
// plugs in the dongle mid-wizard can hit "rescan" and see it
// without restarting knotd.
func (s *Server) handleSetupCapability(w http.ResponseWriter, r *http.Request) {
	rep, err := capability.Probe{}.Run()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "capability_failed", err.Error())
		return
	}
	// Best-effort log so an operator with serial / journalctl access
	// can see exactly what was detected, which is faster to debug
	// than "the wizard didn't show the role step on my Pi".
	if r != nil {
		// Use the request context's logger if present in the future;
		// for now, log via the standard logger which is wired to
		// stderr in main.go.
		log.Printf("setup/capability: pi=%q router_capable=%v eth=%d",
			rep.Pi, rep.RouterCapable, len(rep.Eth))
		for _, a := range rep.Eth {
			log.Printf("setup/capability: eth %s driver=%s usb=%s:%s model=%q",
				a.Interface, a.Driver, a.USBVendor, a.USBProduct, a.Model)
		}
	}
	writeJSON(w, http.StatusOK, rep)
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
//
// Role drives which sub-blocks are required:
//   - "wifi-extender" (default): Uplink is required, WAN is ignored.
//   - "wifi-router":              WAN is required, Uplink is ignored.
type completeRequest struct {
	Device struct {
		Name    string `json:"name"`
		Country string `json:"country"`
	} `json:"device"`

	Password string `json:"password"`

	// Role: "wifi-extender" (default if empty) or "wifi-router".
	Role string `json:"role,omitempty"`

	Uplink struct {
		SSID string `json:"ssid"`
		PSK  string `json:"psk"`
	} `json:"uplink"`

	AP struct {
		SSID    string `json:"ssid"`
		PSK     string `json:"psk"`
		Band    string `json:"band"`
		Channel int    `json:"channel,omitempty"`
	} `json:"ap"`

	WAN struct {
		Interface string `json:"interface"`
		Mode      string `json:"mode,omitempty"`
	} `json:"wan"`
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

	role := config.Role(req.Role)
	if role == "" {
		role = config.RoleWiFiExtender
	}

	finished := config.Config{
		Device: config.Device{
			Name:    req.Device.Name,
			Country: req.Device.Country,
		},
		Role: role,
		Auth: config.Auth{PasswordHash: hash},
		Network: config.Network{
			AP: &config.WiFiAP{
				SSID:    req.AP.SSID,
				PSK:     req.AP.PSK,
				Band:    req.AP.Band,
				Channel: req.AP.Channel,
			},
			LAN: lan,
		},
	}
	switch role {
	case config.RoleWiFiExtender:
		finished.Network.Uplink = &config.WiFiUplink{
			SSID: req.Uplink.SSID,
			PSK:  req.Uplink.PSK,
		}
	case config.RoleWiFiRouter:
		mode := req.WAN.Mode
		if mode == "" {
			mode = "dhcp"
		}
		finished.Network.WAN = &config.WAN{
			Interface: req.WAN.Interface,
			Mode:      mode,
		}
	default:
		writeError(w, http.StatusUnprocessableEntity, "invalid_role",
			"role must be \"wifi-extender\" or \"wifi-router\"")
		return
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
	if err := config.SaveWith(s.configPath, finished, s.sealer); err != nil {
		writeError(w, http.StatusInternalServerError, "save_failed", err.Error())
		return
	}
	s.mu.Lock()
	s.cfg = finished
	s.mu.Unlock()
	s.fireConfigApplied(finished)

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
