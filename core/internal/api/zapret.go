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
	r.Post("/zapret/autotune", s.handleAutoTuneZapret)
}

// zapretEgressIface mirrors the helper in cmd/knotd: the interface
// internet-bound traffic leaves on, which the nft hook (and the
// auto-tune probes) need. Empty disables (setup role / no WAN). In modem
// WAN mode the config interface is empty (the wwanN device is discovered
// at connect time), so it's resolved live from the backend.
func (s *Server) zapretEgressIface(cfg config.Config) string {
	switch cfg.Role {
	case config.RoleWiFiRouter:
		if cfg.Network.WAN == nil {
			return ""
		}
		if cfg.Network.WAN.Mode == "modem" {
			if p, ok := s.backend.(interface{ LiveModemIface() string }); ok {
				return p.LiveModemIface()
			}
			return ""
		}
		return cfg.Network.WAN.Interface
	case config.RoleWiFiExtender:
		return "wlan0"
	}
	return ""
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
	running, binaryPresent := false, false
	var autotune map[string]any
	if s.zapret != nil {
		for _, p := range zapret.LoadStrategies(s.zapret.BaseDir()) {
			presets = append(presets, presetJSON{ID: p.ID, Name: p.Name, Desc: p.Desc})
		}
		running = s.zapret.Running()
		binaryPresent = s.zapret.BinaryPresent()
		// Surface the last auto-tune run so the score table survives a
		// page reload (winner "" means nothing beat the censor).
		if results, winner, at, ok := s.zapret.LastTune(); ok {
			zapret.SortByScore(results)
			autotune = map[string]any{
				"results": results,
				"winner":  winner,
				"at":      at.UTC().Format("2006-01-02T15:04:05Z"),
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":     z.Enabled,
		"strategy":    z.Strategy,
		"custom_args": z.CustomArgs,
		"presets":     presets,
		"autotune":    autotune,
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
	// Zapret only drives nfqws + its own isolated nft table, so persist
	// and reconcile without a full network re-apply — toggling it must
	// not drop every Wi-Fi client.
	status, payload := s.persistConfigLight(incoming)
	writeJSON(w, status, payload)
}

func (s *Server) handleAutoTuneZapret(w http.ResponseWriter, r *http.Request) {
	if s.zapret == nil {
		writeError(w, http.StatusServiceUnavailable, "zapret_disabled", "engine not available")
		return
	}
	cfg := s.Snapshot()
	wan := s.zapretEgressIface(cfg)
	if wan == "" {
		writeError(w, http.StatusUnprocessableEntity, "no_wan",
			"auto-tune needs a WAN interface — switch to router mode first")
		return
	}

	results, winner, err := s.zapret.AutoTune(r.Context(), zapret.Settings{
		Enabled:      true,
		WANInterface: wan,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "autotune_failed", err.Error())
		return
	}
	zapret.SortByScore(results)

	// Persist the winner so it sticks across restarts. The re-apply this
	// triggers is a no-op (the winner is already running).
	incoming := cfg
	incoming.Network.Zapret = &config.Zapret{Enabled: true, Strategy: winner}
	if status, payload := s.persistConfigLight(incoming); status != http.StatusOK {
		writeJSON(w, status, payload)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"winner":  winner,
		"results": results,
	})
}

func (s *Server) handleRefreshZapret(w http.ResponseWriter, r *http.Request) {
	if s.zapret == nil {
		writeError(w, http.StatusServiceUnavailable, "zapret_disabled", "engine not available")
		return
	}
	base := s.zapret.BaseDir()
	nLists, errL := zapret.RefreshLists(r.Context(), base)
	nStrat, errS := zapret.RefreshStrategies(r.Context(), base)
	// Fake-payload bins that the newer strategies reference — best-effort,
	// the seed covers the common ones, so a failure here never fails the
	// refresh.
	nBins, _ := zapret.RefreshBins(r.Context(), base)
	if errL != nil && errS != nil {
		writeError(w, http.StatusBadGateway, "refresh_failed",
			"lists: "+errL.Error()+"; strategies: "+errS.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"lists":      nLists,
		"strategies": nStrat,
		"bins":       nBins,
		"updated":    nLists + nStrat + nBins,
	})
}
