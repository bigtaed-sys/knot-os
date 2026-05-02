package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/knot-os/knot-os/core/internal/config"
	"github.com/knot-os/knot-os/core/internal/plugin"
)

// MountPlugins registers /plugins/* routes on the given router.
// Plugins endpoints require an authenticated session.
//
// Routes:
//
//	GET  /plugins         — list all discovered plugins.
//	PUT  /plugins/{id}    — body { "enabled": true|false }
func (s *Server) MountPlugins(r chi.Router) {
	if s.plugins == nil {
		// Plugin registry not configured; mount stub that 503s so
		// callers get a clear error instead of a silent 404.
		r.Get("/plugins", func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusServiceUnavailable, "plugins_disabled", "plugin registry is not configured")
		})
		return
	}

	r.Get("/plugins", s.handleListPlugins)
	r.Put("/plugins/{id}", s.handleSetPluginEnabled)
}

func (s *Server) handleListPlugins(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"plugins": s.plugins.List(),
	})
}

func (s *Server) handleSetPluginEnabled(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	if !s.plugins.SetEnabled(id, body.Enabled) {
		writeError(w, http.StatusNotFound, "plugin_not_found", "no such plugin: "+id)
		return
	}

	// Persist into config.yaml. We rebuild the plugins map from the
	// registry so the config file always matches reality.
	s.mu.Lock()
	cfg := s.cfg
	if cfg.Plugins == nil {
		cfg.Plugins = make(map[string]config.PluginConfig)
	}
	enabledMap := s.plugins.EnabledMap()
	cfg.Plugins = make(map[string]config.PluginConfig, len(enabledMap))
	for pid := range enabledMap {
		cfg.Plugins[pid] = config.PluginConfig{Enabled: true}
	}
	s.cfg = cfg
	s.mu.Unlock()

	if err := config.SaveWith(s.configPath, cfg, s.sealer); err != nil {
		writeError(w, http.StatusInternalServerError, "save_failed", err.Error())
		return
	}

	p, _ := s.plugins.Get(id)
	writeJSON(w, http.StatusOK, p)
}

// SetPluginRegistry attaches a plugin registry to the server. Called
// from main during startup.
func (s *Server) SetPluginRegistry(p *plugin.Registry) {
	s.plugins = p
}
