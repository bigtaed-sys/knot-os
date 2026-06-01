package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/knot-os/knot-os/core/internal/deviceregistry"
	"github.com/knot-os/knot-os/core/internal/wol"
)

// macParam pulls {mac} from the URL and percent-decodes it. chi does
// NOT URL-decode path parameters by default, so a UI that sends
// /devices/dc%3Aa6%3A32%3A11%3A22%3A33 would otherwise hit our
// handlers with a 32-char string that the registry rightly refuses
// — visible to the user as "device not found" on every detail page.
func macParam(r *http.Request) string {
	raw := chi.URLParam(r, "mac")
	dec, err := url.PathUnescape(raw)
	if err != nil {
		return raw
	}
	return dec
}

// MountDevices registers /devices/* under the auth-gated group.
//
// Endpoints:
//
//	GET   /devices         — list all known devices (sorted by MAC).
//	GET   /devices/{mac}   — fetch a single device.
//	PATCH /devices/{mac}   — body { display_name?, profile_id? }
//
// MAC is matched case-insensitively; the API always echoes back the
// canonical lower-case form so callers can reliably URL-encode it.
func (s *Server) MountDevices(r chi.Router) {
	if s.devices == nil {
		r.Get("/devices", func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusServiceUnavailable, "devices_disabled", "device registry not configured")
		})
		return
	}
	r.Get("/devices", s.handleListDevices)
	r.Get("/devices/{mac}", s.handleGetDevice)
	r.Patch("/devices/{mac}", s.handlePatchDevice)
	r.Delete("/devices/{mac}", s.handleDeleteDevice)
	r.Post("/devices/{mac}/wake", s.handleWakeDevice)
}

// SetDeviceRegistry attaches a registry to the server. Called from main.
func (s *Server) SetDeviceRegistry(d *deviceregistry.Registry) {
	s.devices = d
}

// deviceJSON is the over-the-wire shape — has Label, Online, and
// Stale computed at serialization time, plus the in-memory
// LastARPSeen for transparency in the UI.
type deviceJSON struct {
	MAC          string    `json:"mac"`
	Label        string    `json:"label"`
	Hostname     string    `json:"hostname,omitempty"`
	DisplayName  string    `json:"display_name,omitempty"`
	IP           string    `json:"ip,omitempty"`
	Online       bool      `json:"online"`
	Stale        bool      `json:"stale"`
	LeaseExpires time.Time `json:"lease_expires,omitempty"`
	FirstSeen    time.Time `json:"first_seen"`
	LastSeen     time.Time `json:"last_seen"`
	LastARPSeen  time.Time `json:"last_arp_seen,omitempty"`
	ProfileID    string    `json:"profile_id,omitempty"`
}

func toJSON(d deviceregistry.Device, now time.Time) deviceJSON {
	return deviceJSON{
		MAC:          d.MAC,
		Label:        d.Label(),
		Hostname:     d.Hostname,
		DisplayName:  d.DisplayName,
		IP:           d.IP,
		Online:       d.Online(now),
		Stale:        d.Stale(now),
		LeaseExpires: d.LeaseExpires,
		FirstSeen:    d.FirstSeen,
		LastSeen:     d.LastSeen,
		LastARPSeen:  d.LastARPSeen,
		ProfileID:    d.ProfileID,
	}
}

func (s *Server) handleListDevices(w http.ResponseWriter, _ *http.Request) {
	now := time.Now()
	all := s.devices.List()
	out := make([]deviceJSON, 0, len(all))
	for _, d := range all {
		out = append(out, toJSON(d, now))
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": out})
}

func (s *Server) handleGetDevice(w http.ResponseWriter, r *http.Request) {
	mac := macParam(r)
	d, ok := s.devices.Get(mac)
	if !ok {
		writeError(w, http.StatusNotFound, "device_not_found", "no such device")
		return
	}
	writeJSON(w, http.StatusOK, toJSON(d, time.Now()))
}

// devicePatch carries the PATCH body. Pointer fields distinguish
// "explicitly set to empty" (cleared) from "absent" (unchanged).
type devicePatch struct {
	DisplayName *string `json:"display_name,omitempty"`
	ProfileID   *string `json:"profile_id,omitempty"`
}

func (s *Server) handlePatchDevice(w http.ResponseWriter, r *http.Request) {
	mac := macParam(r)
	var body devicePatch
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	updated, err := s.devices.Update(mac, func(d *deviceregistry.Device) {
		if body.DisplayName != nil {
			d.DisplayName = *body.DisplayName
		}
		if body.ProfileID != nil {
			d.ProfileID = *body.ProfileID
		}
	})
	if err != nil {
		var nf *errDeviceNotFound
		if errors.As(err, &nf) {
			writeError(w, http.StatusNotFound, "device_not_found", err.Error())
			return
		}
		// The registry's Update returns a fmt.Errorf; surface as 404
		// for unknown MAC since that's the only way Update fails.
		writeError(w, http.StatusNotFound, "device_not_found", err.Error())
		return
	}

	// Best-effort flush so changes survive a hard reset, even though
	// the periodic flusher would also catch it within 30s.
	if err := s.devices.FlushIfDirty(); err != nil {
		// Log via writeJSON-side; don't fail the request.
	}

	// If the profile assignment changed, ask the scheduler to
	// re-evaluate now instead of waiting up to 30s, and rebuild
	// routing: the new profile may carry a RouteVia tunnel, so
	// sing-box needs its per-device source-IP rule refreshed.
	// Without this the device's traffic keeps going direct until
	// the next full config-apply or reboot.
	if body.ProfileID != nil {
		s.kick()
		s.fireConfigApplied(s.Snapshot())
	}

	writeJSON(w, http.StatusOK, toJSON(updated, time.Now()))
}

// handleDeleteDevice removes a device entry from the registry. The
// primary use case is cleaning up duplicate entries left by iOS /
// Android private MAC randomization — one physical phone appearing
// under several MACs as it cycles through randomized addresses.
//
// We refuse to delete a device that's currently online: that would
// be confusing (it would silently come back on the next lease tick).
// The user gets a 409 with a clear message.
func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	mac := macParam(r)
	d, ok := s.devices.Get(mac)
	if !ok {
		writeError(w, http.StatusNotFound, "device_not_found", "no such device")
		return
	}
	if d.Online(time.Now()) {
		writeError(w, http.StatusConflict, "device_online",
			"cannot remove an online device — wait for its lease to expire")
		return
	}
	if err := s.devices.Forget(mac); err != nil {
		writeError(w, http.StatusInternalServerError, "delete_failed", err.Error())
		return
	}
	if err := s.devices.FlushIfDirty(); err != nil {
		writeError(w, http.StatusInternalServerError, "save_failed", err.Error())
		return
	}
	// Schedule re-evaluates the block-set; without a kick the deleted
	// MAC could linger in nftables until the next 30s tick.
	s.kick()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleWakeDevice sends a Wake-on-LAN magic packet for the named
// MAC. Useful for waking an Ethernet-attached desktop / NAS that's
// in S5/sleep — one click in the UI replaces the "rummage in
// router web admin for a hidden setting" dance most consumer
// routers force.
//
// The kernel-level UDP send completes silently; we don't retry,
// don't follow up with an ARP probe — the UI's regular device
// poll will flip the device back to online once it boots.
func (s *Server) handleWakeDevice(w http.ResponseWriter, r *http.Request) {
	mac := macParam(r)
	d, ok := s.devices.Get(mac)
	if !ok {
		writeError(w, http.StatusNotFound, "device_not_found", "no such device")
		return
	}
	cfg := s.Snapshot()
	lanCIDR := ""
	if cfg.Network.LAN != nil {
		lanCIDR = cfg.Network.LAN.CIDR
	}
	if lanCIDR == "" {
		writeError(w, http.StatusServiceUnavailable, "no_lan",
			"LAN not configured — can't compute broadcast address")
		return
	}
	bcast, err := wol.BroadcastForCIDR(lanCIDR)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "broadcast_failed", err.Error())
		return
	}
	if err := wol.Wake(d.MAC, bcast, 0); err != nil {
		writeError(w, http.StatusInternalServerError, "wake_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":        true,
		"mac":       d.MAC,
		"broadcast": bcast,
	})
}

// errDeviceNotFound is reserved for future typed-error paths from the
// registry. Currently the registry returns a fmt.Errorf-wrapped
// message; we treat the 404 path uniformly above.
type errDeviceNotFound struct{ MAC string }

func (e *errDeviceNotFound) Error() string { return "device not found: " + e.MAC }
