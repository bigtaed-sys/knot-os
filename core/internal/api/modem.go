package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/knot-os/knot-os/core/internal/config"
	"github.com/knot-os/knot-os/core/internal/modemmetrics"
	"github.com/knot-os/knot-os/core/internal/network"
)

// modemStatusProvider is the optional backend capability the cellular
// endpoints need. The Linux backend implements it via ModemManager;
// dev/mock backends don't, so the API degrades to "no modem".
type modemStatusProvider interface {
	ModemStatus(ctx context.Context) (network.ModemStatus, error)
}

// MountModem registers the cellular-WAN endpoints:
//
//	GET /modem  — current settings + live ModemManager status
//	PUT /modem  — set APN/PIN and whether the modem is the WAN
func (s *Server) MountModem(r chi.Router) {
	r.Get("/modem", s.handleGetModem)
	r.Put("/modem", s.handlePutModem)
}

// SetModemMetrics wires the cellular usage/signal tracker so GET /modem
// can report data usage against the cap and a signal sparkline.
func (s *Server) SetModemMetrics(t *modemmetrics.Tracker) { s.modemMetrics = t }

func (s *Server) handleGetModem(w http.ResponseWriter, r *http.Request) {
	cfg := s.Snapshot()
	m := config.Modem{}
	if cfg.Network.WAN != nil && cfg.Network.WAN.Modem != nil {
		m = *cfg.Network.WAN.Modem
	}
	asWAN := cfg.Network.WAN != nil && cfg.Network.WAN.Mode == "modem"

	status := network.ModemStatus{Present: false}
	if mp, ok := s.backend.(modemStatusProvider); ok {
		if st, err := mp.ModemStatus(r.Context()); err == nil {
			status = st
		}
	}

	resp := map[string]any{
		"as_wan":          asWAN,
		"apn":             m.APN,
		"username":        m.Username,
		"has_pin":         m.PIN != "",
		"sim_slot":        m.SIMSlot,
		"data_limit_mb":   m.DataLimitMB,
		"cycle_reset_day": m.CycleResetDay,
		"status":          status,
		// router_mode tells the UI a modem can only be the WAN in
		// full-router mode (the extender uses a Wi-Fi uplink).
		"router_mode": cfg.Role == config.RoleWiFiRouter,
	}
	if s.modemMetrics != nil {
		resp["usage"] = s.modemMetrics.Snapshot()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePutModem(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AsWAN         bool    `json:"as_wan"`
		APN           string  `json:"apn"`
		Username      string  `json:"username"`
		Password      string  `json:"password"`
		PIN           *string `json:"pin"`      // nil = leave unchanged; "" = clear
		SIMSlot       *int    `json:"sim_slot"` // nil = leave unchanged
		DataLimitMB   *int    `json:"data_limit_mb"`
		CycleResetDay *int    `json:"cycle_reset_day"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	incoming := s.Snapshot()
	if incoming.Network.WAN == nil {
		incoming.Network.WAN = &config.WAN{}
	}
	m := config.Modem{}
	if incoming.Network.WAN.Modem != nil {
		m = *incoming.Network.WAN.Modem
	}
	m.APN = body.APN
	m.Username = body.Username
	if body.Password != "" {
		m.Password = body.Password
	}
	if body.PIN != nil {
		m.PIN = *body.PIN
	}
	if body.SIMSlot != nil {
		m.SIMSlot = *body.SIMSlot
	}
	if body.DataLimitMB != nil {
		m.DataLimitMB = *body.DataLimitMB
	}
	if body.CycleResetDay != nil {
		m.CycleResetDay = *body.CycleResetDay
		if s.modemMetrics != nil {
			s.modemMetrics.SetResetDay(m.CycleResetDay)
		}
	}
	incoming.Network.WAN.Modem = &m
	if body.AsWAN {
		incoming.Network.WAN.Mode = "modem"
	} else if incoming.Network.WAN.Mode == "modem" {
		// Turning the modem off as WAN reverts to the Ethernet path.
		incoming.Network.WAN.Mode = "dhcp"
	}

	// Switching the WAN reconfigures the uplink, so this goes through the
	// full transactional apply (snapshot + health-check + rollback).
	status, payload := s.commitConfig(r.Context(), incoming, "api:put-modem")
	writeJSON(w, status, payload)
}
