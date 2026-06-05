package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/knot-os/knot-os/core/internal/config"
	"github.com/knot-os/knot-os/core/internal/zapret"
)

// SetZapretManager attaches the engine manager (status + lists refresh).
func (s *Server) SetZapretManager(m *zapret.Manager) { s.zapret = m }

// MountZapret registers the DPI-bypass endpoints:
//
//	GET  /zapret          — current settings, preset catalogue, status
//	PUT  /zapret          — update settings (transactional apply)
//	POST /zapret/refresh  — re-pull domain lists from upstream
func (s *Server) MountZapret(r chi.Router) {
	r.Get("/zapret", s.handleGetZapret)
	r.Put("/zapret", s.handlePutZapret)
	r.Post("/zapret/refresh", s.handleRefreshZapret)
}

type presetJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Desc string `json:"desc"`
}

func (s *Server) handleGetZapret(w http.ResponseWriter, _ *http.Request) {
	cfg := s.Snapshot()
	z := cfg.Network.Zapret
	if z == nil {
		z = &config.Zapret{Strategy: "general"}
	}
	presets := make([]presetJSON, 0)
	for _, p := range zapret.Presets() {
		presets = append(presets, presetJSON{ID: p.ID, Name: p.Name, Desc: p.Desc})
	}
	running, binaryPresent := false, false
	if s.zapret != nil {
		running = s.zapret.Running()
		binaryPresent = s.zapret.BinaryPresent()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":     z.Enabled,
		"strategy":    z.Strategy,
		"custom_args": z.CustomArgs,
		"presets":     presets,
		"status": map[string]any{
			"running":        running,
			"binary_present": binaryPresent,
			"router_mode":    cfg.Role == config.RoleWiFiRouter,
		},
	})
}

func (s *Server) handlePutZapret(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled    bool   `json:"enabled"`
		Strategy   string `json:"strategy"`
		CustomArgs string `json:"custom_args"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	incoming := s.Snapshot()
	incoming.Network.Zapret = &config.Zapret{
		Enabled:    body.Enabled,
		Strategy:   body.Strategy,
		CustomArgs: body.CustomArgs,
	}
	status, payload := s.commitConfig(r.Context(), incoming, "api:put-zapret")
	writeJSON(w, status, payload)
}

func (s *Server) handleRefreshZapret(w http.ResponseWriter, r *http.Request) {
	if s.zapret == nil {
		writeError(w, http.StatusServiceUnavailable, "zapret_disabled", "engine not available")
		return
	}
	n, err := zapret.RefreshLists(r.Context(), s.zapret.BaseDir())
	if err != nil {
		writeError(w, http.StatusBadGateway, "refresh_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": n})
}
