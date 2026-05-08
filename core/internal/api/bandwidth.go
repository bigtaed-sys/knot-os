package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/knot-os/knot-os/core/internal/bandwidth"
)

// SetBandwidthTracker wires the live-traffic tracker into the API.
// Pass nil to disable the endpoints.
func (s *Server) SetBandwidthTracker(t *bandwidth.Tracker) {
	s.bandwidth = t
}

// MountBandwidth registers /bandwidth/* under the auth-gated group.
//
// Endpoints:
//
//	GET /bandwidth          — current snapshot for every tracked device
//	GET /bandwidth/{mac}    — single device, full sparkline (30 min)
//
// The Devices page polls /bandwidth on a 5-second interval and uses
// the LastSample from each entry to render Kbps + a mini sparkline.
// The device-detail page polls /bandwidth/{mac} for the full graph.
func (s *Server) MountBandwidth(r chi.Router) {
	if s.bandwidth == nil {
		r.Get("/bandwidth", func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusServiceUnavailable, "bandwidth_disabled",
				"bandwidth metering not enabled in this build")
		})
		return
	}
	r.Get("/bandwidth", s.handleBandwidthAll)
	r.Get("/bandwidth/{mac}", s.handleBandwidthOne)
}

func (s *Server) handleBandwidthAll(w http.ResponseWriter, _ *http.Request) {
	stats := s.bandwidth.SnapshotAll()
	writeJSON(w, http.StatusOK, map[string]any{
		"devices": stats,
	})
}

func (s *Server) handleBandwidthOne(w http.ResponseWriter, r *http.Request) {
	mac := macParam(r)
	if mac == "" {
		writeError(w, http.StatusBadRequest, "bad_mac", "MAC required")
		return
	}
	st, ok := s.bandwidth.Snapshot(mac)
	if !ok {
		writeError(w, http.StatusNotFound, "no_data",
			"no bandwidth data for that device yet — wait a few seconds for the next sample")
		return
	}
	writeJSON(w, http.StatusOK, st)
}
