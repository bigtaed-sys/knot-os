package api

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	qrcode "github.com/skip2/go-qrcode"

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
		r.Get("/qr", s.handleSetupQR)
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
//
// The probe runs in a separate goroutine with a 5-second deadline.
// In practice the probe is microseconds (just sysfs reads); the
// deadline exists so the API can never wedge if the kernel's sysfs
// stalls during USB hotplug, which the user has actually hit
// in the field — without the deadline the wizard's spinner stays
// up indefinitely and the user can't even get to the
// extender-only fallback.
func (s *Server) handleSetupCapability(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	type result struct {
		rep capability.Report
		err error
	}
	ch := make(chan result, 1)
	go func() {
		// Bring every USB-Ethernet admin-UP first. Without this the
		// kernel's carrier file reads 0 even on a plugged-in cable
		// — see setup_linkup_linux.go for the full story.
		bringUSBEthUp()
		rep, err := capability.Probe{}.Run()
		ch <- result{rep, err}
	}()

	select {
	case res := <-ch:
		if res.err != nil {
			writeError(w, http.StatusInternalServerError, "capability_failed", res.err.Error())
			return
		}
		log.Printf("setup/capability: pi=%q router_capable=%v eth=%d (%v)",
			res.rep.Pi, res.rep.RouterCapable, len(res.rep.Eth), time.Since(start))
		for _, a := range res.rep.Eth {
			log.Printf("setup/capability: eth %s driver=%s usb=%s:%s model=%q",
				a.Interface, a.Driver, a.USBVendor, a.USBProduct, a.Model)
		}
		writeJSON(w, http.StatusOK, res.rep)
	case <-time.After(5 * time.Second):
		log.Printf("setup/capability: probe timed out after 5s — sysfs read stuck")
		writeError(w, http.StatusServiceUnavailable, "capability_timeout",
			"hardware probe did not complete in 5s")
	}
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

// handleSetupQR renders the `text` query parameter as a PNG QR code.
// The wizard's Wi-Fi step points an <img> at this endpoint with the
// `WIFI:T:WPA;S:...;P:...;H:false;` join string so the user (or a
// guest) can scan the about-to-be-created network straight off the
// onboarding screen. Encoding server-side reuses the same go-qrcode
// dependency the WireGuard peer QR already pulls in, so the UI bundle
// stays dependency-free.
//
// The text is the user's own credentials echoed back as an image; we
// cap the length purely to bound the encoder's work.
func (s *Server) handleSetupQR(w http.ResponseWriter, r *http.Request) {
	text := r.URL.Query().Get("text")
	if text == "" {
		writeError(w, http.StatusBadRequest, "missing_text", "text query parameter is required")
		return
	}
	if len(text) > 512 {
		writeError(w, http.StatusRequestEntityTooLarge, "text_too_long", "text exceeds 512 bytes")
		return
	}
	png, err := qrcode.Encode(text, qrcode.Medium, 256)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "qr_failed", err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(png)
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

	// Wipe the device registry: every entry collected during setup
	// came from a phone that briefly joined KnotOS-setup-XXXX to
	// run the wizard. iOS/Android use a per-SSID randomized MAC,
	// so the SAME physical phone reconnects to the production
	// SSID under a different MAC — leaving the setup-time MAC as
	// a permanent ghost in the device list. Clearing here is
	// exactly what the user expects: "the moment I finish setup,
	// my device list shows only what's connected to my real Wi-Fi".
	if s.devices != nil {
		if err := s.devices.Reset(); err != nil {
			// Non-fatal — just log; the in-memory state is wiped
			// regardless and the periodic flusher will re-create
			// a clean store file on the next change.
			log.Printf("setup/complete: device registry reset: %v", err)
		}
	}

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
