package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

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

// modemController is the optional backend capability for the SMS / USSD /
// network-selection endpoints. The Linux backend implements it via mmcli;
// other backends don't, so those endpoints degrade to 503.
type modemController interface {
	SendUSSD(ctx context.Context, code string) (string, error)
	ListSMS(ctx context.Context) ([]network.SMS, error)
	SendSMS(ctx context.Context, number, text string) error
	DeleteSMS(ctx context.Context, id string) error
	ModemNetwork(ctx context.Context) (network.ModemNetwork, error)
	SetNetworkModes(ctx context.Context, modes []string) error
	SetBands(ctx context.Context, bands []string) error
}

// MountModem registers the cellular-WAN endpoints:
//
//	GET  /modem          — settings + live ModemManager status
//	PUT  /modem          — set APN/PIN, SIM slot, data cap, WAN mode
//	POST /modem/ussd     — run a USSD code (prepaid balance)
//	GET  /modem/sms      — list stored SMS
//	POST /modem/sms      — send an SMS
//	DEL  /modem/sms/{id} — delete a stored SMS
//	GET  /modem/network  — supported/current access techs + bands
//	PUT  /modem/network  — set access techs / lock bands
func (s *Server) MountModem(r chi.Router) {
	r.Get("/modem", s.handleGetModem)
	r.Put("/modem", s.handlePutModem)
	r.Post("/modem/ussd", s.handleModemUSSD)
	r.Get("/modem/sms", s.handleModemListSMS)
	r.Post("/modem/sms", s.handleModemSendSMS)
	r.Delete("/modem/sms/{id}", s.handleModemDeleteSMS)
	r.Get("/modem/network", s.handleModemGetNetwork)
	r.Put("/modem/network", s.handleModemSetNetwork)
}

// modemCtl returns the backend's modem controller, or writes 503 and
// returns nil when the backend doesn't support these operations.
func (s *Server) modemCtl(w http.ResponseWriter) modemController {
	if c, ok := s.backend.(modemController); ok {
		return c
	}
	writeError(w, http.StatusServiceUnavailable, "modem_unavailable",
		"cellular control operations are not available on this backend")
	return nil
}

func (s *Server) handleModemUSSD(w http.ResponseWriter, r *http.Request) {
	c := s.modemCtl(w)
	if c == nil {
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if strings.TrimSpace(body.Code) == "" {
		writeError(w, http.StatusUnprocessableEntity, "missing_code", "a USSD code is required")
		return
	}
	resp, err := c.SendUSSD(r.Context(), body.Code)
	if err != nil {
		writeError(w, http.StatusBadGateway, "ussd_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"response": resp})
}

func (s *Server) handleModemListSMS(w http.ResponseWriter, r *http.Request) {
	c := s.modemCtl(w)
	if c == nil {
		return
	}
	msgs, err := c.ListSMS(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "sms_list_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": msgs})
}

func (s *Server) handleModemSendSMS(w http.ResponseWriter, r *http.Request) {
	c := s.modemCtl(w)
	if c == nil {
		return
	}
	var body struct {
		Number string `json:"number"`
		Text   string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := c.SendSMS(r.Context(), body.Number, body.Text); err != nil {
		writeError(w, http.StatusBadGateway, "sms_send_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleModemDeleteSMS(w http.ResponseWriter, r *http.Request) {
	c := s.modemCtl(w)
	if c == nil {
		return
	}
	if err := c.DeleteSMS(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeError(w, http.StatusBadGateway, "sms_delete_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleModemGetNetwork(w http.ResponseWriter, r *http.Request) {
	c := s.modemCtl(w)
	if c == nil {
		return
	}
	net, err := c.ModemNetwork(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "network_read_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, net)
}

func (s *Server) handleModemSetNetwork(w http.ResponseWriter, r *http.Request) {
	c := s.modemCtl(w)
	if c == nil {
		return
	}
	var body struct {
		Modes *[]string `json:"modes"` // nil = leave; [] = auto
		Bands *[]string `json:"bands"` // nil = leave; [] = any
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if body.Modes != nil {
		if err := c.SetNetworkModes(r.Context(), *body.Modes); err != nil {
			writeError(w, http.StatusBadGateway, "set_modes_failed", err.Error())
			return
		}
	}
	if body.Bands != nil {
		if err := c.SetBands(r.Context(), *body.Bands); err != nil {
			writeError(w, http.StatusBadGateway, "set_bands_failed", err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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
