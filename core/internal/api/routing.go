package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/knot-os/knot-os/core/internal/routing"
)

// SetRoutingProvider wires a closure the API calls to compute the
// current routing state on demand. Pass nil to disable the
// /routing endpoint. main.go's adapter pulls live snapshots out of
// the three registries and calls routing.FromRegistries.
func (s *Server) SetRoutingProvider(fn func() (routing.Result, error)) {
	s.routingProvider = fn
}

// MountRouting registers /routing under the auth-gated group.
//
// Endpoints:
//
//	GET /routing — current per-device decisions and missing outbounds
//
// Useful for the «Маршрутизация» UI page so it can render which
// device is going through which server, without re-implementing
// the routing logic on the client side.
func (s *Server) MountRouting(r chi.Router) {
	if s.routingProvider == nil {
		r.Get("/routing", func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusServiceUnavailable, "routing_disabled",
				"routing module not configured")
		})
		return
	}
	r.Get("/routing", s.handleRoutingGet)
}

type routingResponseDevice struct {
	MAC       string `json:"mac"`
	Outbound  string `json:"outbound"`
	Status    string `json:"status"`
	ProfileID string `json:"profile_id"`
}

func (s *Server) handleRoutingGet(w http.ResponseWriter, _ *http.Request) {
	res, err := s.routingProvider()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "routing_failed", err.Error())
		return
	}
	devs := make([]routingResponseDevice, 0, len(res.DeviceRoutes))
	for mac, dr := range res.DeviceRoutes {
		devs = append(devs, routingResponseDevice{
			MAC:       mac,
			Outbound:  dr.Outbound,
			Status:    dr.Status,
			ProfileID: dr.ProfileID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"devices":           devs,
		"missing_outbounds": res.MissingOutbounds,
	})
}
