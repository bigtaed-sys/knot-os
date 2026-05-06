package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/knot-os/knot-os/core/internal/channelscan"
	"github.com/knot-os/knot-os/core/internal/config"
)

// MountChannels registers /network/channels under the auth-gated
// group.
//
//	GET   /network/channels        — scan + per-channel load + recommendation
//	POST  /network/channels/apply  — body { channel: int }, switches the
//	                                  AP. Only valid in wifi-router role
//	                                  (extender mode pins to upstream).
func (s *Server) MountChannels(r chi.Router) {
	r.Get("/network/channels", s.handleChannelScan)
	r.Post("/network/channels/apply", s.handleChannelApply)
}

func (s *Server) handleChannelScan(w http.ResponseWriter, r *http.Request) {
	scan, err := s.backend.Scan(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "scan_failed", err.Error())
		return
	}
	current := 0
	cfg := s.Snapshot()
	if cfg.Network.AP != nil {
		current = cfg.Network.AP.Channel
	}
	report := channelscan.Compute(scan, current)
	writeJSON(w, http.StatusOK, report)
}

type channelApplyRequest struct {
	Channel int `json:"channel"`
}

func (s *Server) handleChannelApply(w http.ResponseWriter, r *http.Request) {
	var body channelApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if body.Channel < 1 || body.Channel > 13 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_channel",
			"channel must be 1..13 (2.4 GHz)")
		return
	}

	cfg := s.Snapshot()
	if cfg.Role != config.RoleWiFiRouter {
		writeError(w, http.StatusConflict, "role_mismatch",
			"channel can only be set in wifi-router role; extender pins to upstream")
		return
	}
	if cfg.Network.AP == nil {
		writeError(w, http.StatusConflict, "no_ap", "AP is not configured")
		return
	}
	// Take a deep-ish copy so the live snapshot doesn't mutate.
	apCopy := *cfg.Network.AP
	apCopy.Channel = body.Channel
	cfg.Network.AP = &apCopy

	if err := s.backend.Apply(r.Context(), cfg); err != nil {
		writeError(w, http.StatusInternalServerError, "apply_failed", err.Error())
		return
	}
	if err := config.SaveWith(s.configPath, cfg, s.sealer); err != nil {
		writeError(w, http.StatusInternalServerError, "save_failed", err.Error())
		return
	}
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
	s.fireConfigApplied(cfg)

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"channel": body.Channel,
	})
}
