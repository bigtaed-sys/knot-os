package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/knot-os/knot-os/core/internal/deviceregistry"
)

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
}

// SetDeviceRegistry attaches a registry to the server. Called from main.
func (s *Server) SetDeviceRegistry(d *deviceregistry.Registry) {
	s.devices = d
}

// deviceJSON is the over-the-wire shape — has Label and Online
// computed at serialization time, plus all persistable fields.
type deviceJSON struct {
	MAC          string    `json:"mac"`
	Label        string    `json:"label"`
	Hostname     string    `json:"hostname,omitempty"`
	DisplayName  string    `json:"display_name,omitempty"`
	IP           string    `json:"ip,omitempty"`
	Online       bool      `json:"online"`
	LeaseExpires time.Time `json:"lease_expires,omitempty"`
	FirstSeen    time.Time `json:"first_seen"`
	LastSeen     time.Time `json:"last_seen"`
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
		LeaseExpires: d.LeaseExpires,
		FirstSeen:    d.FirstSeen,
		LastSeen:     d.LastSeen,
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
	mac := chi.URLParam(r, "mac")
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
	mac := chi.URLParam(r, "mac")
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

	writeJSON(w, http.StatusOK, toJSON(updated, time.Now()))
}

// errDeviceNotFound is reserved for future typed-error paths from the
// registry. Currently the registry returns a fmt.Errorf-wrapped
// message; we treat the 404 path uniformly above.
type errDeviceNotFound struct{ MAC string }

func (e *errDeviceNotFound) Error() string { return "device not found: " + e.MAC }
