package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	qrcode "github.com/skip2/go-qrcode"

	"github.com/knot-os/knot-os/core/internal/guest"
)

// MountGuest registers /guest endpoints under the auth-gated group.
//
//	GET    /guest   — current session (or 204 No Content)
//	POST   /guest   — create / replace the active session
//	DELETE /guest   — revoke immediately
//	GET    /guest/qr.png — full-size PNG of the WIFI: QR for the
//	                       active session (returned as image/png so
//	                       the UI can use a plain <img>)
func (s *Server) MountGuest(r chi.Router) {
	if s.guest == nil {
		r.Get("/guest", func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusServiceUnavailable, "guest_disabled", "guest network feature not configured")
		})
		return
	}
	r.Get("/guest", s.handleGuestGet)
	r.Post("/guest", s.handleGuestCreate)
	r.Delete("/guest", s.handleGuestRevoke)
	r.Get("/guest/qr.png", s.handleGuestQRPNG)
}

// SetGuestRegistry wires the guest registry into the API. nil
// disables — endpoints respond 503.
func (s *Server) SetGuestRegistry(g *guest.Registry) { s.guest = g }

// guestSessionJSON is the wire shape. Includes the current PSK
// in cleartext on purpose: the whole point of the API is to show
// the user what to share, and the endpoint is auth-gated.
type guestSessionJSON struct {
	SSID         string    `json:"ssid"`
	PSK          string    `json:"psk"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	RemainingSec int64     `json:"remaining_sec"`
	ProfileID    string    `json:"profile_id,omitempty"`
	WiFiQR       string    `json:"wifi_qr"`
	QRPNGBase64  string    `json:"qr_png_base64"`
}

func toGuestJSON(s guest.Session) (guestSessionJSON, error) {
	out := guestSessionJSON{
		SSID:      s.SSID,
		PSK:       s.PSK,
		CreatedAt: s.CreatedAt,
		ExpiresAt: s.ExpiresAt,
		ProfileID: s.ProfileID,
		WiFiQR:    s.WiFiQRString(),
	}
	if !s.ExpiresAt.IsZero() {
		rem := time.Until(s.ExpiresAt)
		if rem < 0 {
			rem = 0
		}
		out.RemainingSec = int64(rem / time.Second)
	}
	// Inline a 256x256 PNG so the UI can show the QR without a
	// second round-trip. Medium error correction tolerates phone-
	// camera glare.
	png, err := qrcode.Encode(s.WiFiQRString(), qrcode.Medium, 256)
	if err != nil {
		return out, err
	}
	out.QRPNGBase64 = base64Encode(png)
	return out, nil
}

func (s *Server) handleGuestGet(w http.ResponseWriter, _ *http.Request) {
	cur := s.guest.Current()
	if !cur.Active(time.Now()) {
		// 204 keeps the client free to render an empty state without
		// branching on a JSON body.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	body, err := toGuestJSON(cur)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "qr_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, body)
}

// guestCreateRequest is the body of POST /guest.
type guestCreateRequest struct {
	// SSID, when empty, defaults to "<main-ssid>-guest".
	SSID string `json:"ssid,omitempty"`
	// DurationSec is how long the guest network stays up.
	// 0 means "until I revoke".
	DurationSec int64 `json:"duration_sec"`
	// ProfileID hooks into the same registry LAN devices use.
	// Defaults to "guest" — caller can override or empty out.
	ProfileID string `json:"profile_id,omitempty"`
}

func (s *Server) handleGuestCreate(w http.ResponseWriter, r *http.Request) {
	var body guestCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	cfg := s.Snapshot()
	baseSSID := ""
	if cfg.Network.AP != nil {
		baseSSID = cfg.Network.AP.SSID
	}
	profile := body.ProfileID
	if profile == "" {
		// "guest" is shipped as a built-in profile; the apply path
		// tolerates it being absent (treats as "no profile").
		profile = "guest"
	}

	sess, err := s.guest.Create(baseSSID, guest.CreateOptions{
		SSID:      body.SSID,
		Duration:  time.Duration(body.DurationSec) * time.Second,
		ProfileID: profile,
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_session", err.Error())
		return
	}

	// Apply now so the new BSS comes up immediately.
	s.fireConfigApplied(cfg)

	jsonOut, err := toGuestJSON(sess)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "qr_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, jsonOut)
}

func (s *Server) handleGuestRevoke(w http.ResponseWriter, _ *http.Request) {
	if err := s.guest.Revoke(); err != nil {
		writeError(w, http.StatusInternalServerError, "revoke_failed", err.Error())
		return
	}
	s.fireConfigApplied(s.Snapshot())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleGuestQRPNG serves the QR straight as image/png. Useful when
// the UI wants to print the QR or right-click → save image, instead
// of consuming the inline base64 from /guest.
func (s *Server) handleGuestQRPNG(w http.ResponseWriter, _ *http.Request) {
	cur := s.guest.Current()
	if !cur.Active(time.Now()) {
		writeError(w, http.StatusNotFound, "no_session", "no active guest session")
		return
	}
	// Larger PNG for printable QR. 512x512 fits a 5cm physical
	// square at 250 dpi, which is well within phone-camera scan
	// range from across a room.
	png, err := qrcode.Encode(cur.WiFiQRString(), qrcode.Medium, 512)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "qr_failed", err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}
