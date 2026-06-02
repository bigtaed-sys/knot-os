package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"time"

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
	// Plugin store: browse a GitHub-hosted catalog, install (signed →
	// trusted; otherwise explicit confirmation), and uninstall.
	r.Get("/plugins/store", s.handlePluginStore)
	r.Post("/plugins/install", s.handlePluginInstall)
	r.Delete("/plugins/{id}", s.handlePluginUninstall)
	// Reverse-proxy a running plugin's own HTTP server (its UI + API).
	// All methods; the wildcard carries the sub-path. Auth-gated like
	// the rest of /api, so only logged-in operators reach plugin UIs.
	r.Handle("/plugins/{id}/proxy/*", http.HandlerFunc(s.handlePluginProxy))
}

// handlePluginProxy forwards a request to the Unix socket of a running
// plugin process, stripping the /plugins/{id}/proxy prefix. Returns
// 502 when the plugin isn't currently up so the UI can show a clear
// "plugin not running" state instead of a confusing dead page.
func (s *Server) handlePluginProxy(w http.ResponseWriter, r *http.Request) {
	if s.pluginSup == nil {
		writeError(w, http.StatusServiceUnavailable, "host_disabled", "plugin host not configured")
		return
	}
	id := chi.URLParam(r, "id")
	st, ok := s.pluginSup.Status(id)
	if !ok || st.State != plugin.StateRunning || st.Socket == "" {
		writeError(w, http.StatusBadGateway, "plugin_not_running",
			"plugin "+id+" is not running")
		return
	}

	rest := chi.URLParam(r, "*")
	proxy := &httputil.ReverseProxy{
		Transport: s.pluginTransport(st.Socket),
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			req.URL.Host = "plugin" // ignored — the transport dials the socket
			req.URL.Path = "/" + rest
			// Tell the plugin where it's mounted, so it can build
			// correct absolute links back to itself if it wants.
			req.Header.Set("X-Knot-Plugin-Base", "/api/plugins/"+id+"/proxy/")
		},
	}
	proxy.ServeHTTP(w, r)
}

// pluginTransport returns a connection-reusing http.Transport that
// dials the given Unix socket, cached per socket path.
func (s *Server) pluginTransport(sock string) *http.Transport {
	if v, ok := s.pluginRTs.Load(sock); ok {
		return v.(*http.Transport)
	}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
		MaxIdleConns:        4,
		IdleConnTimeout:     90 * 1e9,
		DisableCompression:  true,
	}
	actual, _ := s.pluginRTs.LoadOrStore(sock, tr)
	return actual.(*http.Transport)
}

func (s *Server) handleListPlugins(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"plugins": s.pluginsWithRuntime(),
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

	// Reconcile running processes: start the just-enabled plugin or
	// stop the just-disabled one.
	if s.pluginSync != nil {
		s.pluginSync()
	}

	p, _ := s.plugins.Get(id)
	pj := pluginJSON{Plugin: p}
	if s.pluginSup != nil {
		if st, ok := s.pluginSup.Status(id); ok {
			pj.Runtime = &st
		}
	}
	writeJSON(w, http.StatusOK, pj)
}

// SetPluginRegistry attaches a plugin registry to the server. Called
// from main during startup.
func (s *Server) SetPluginRegistry(p *plugin.Registry) {
	s.plugins = p
}

// SetPluginSyncFn registers the callback that reconciles running
// plugin processes with the registry's enabled set (main wires it to
// supervisor.Sync). Fired after a plugin is toggled.
func (s *Server) SetPluginSyncFn(fn func()) { s.pluginSync = fn }

// SetPluginStore wires the store: an installer (download/verify/unpack)
// and the catalog index URL. Pass a nil installer to leave the store
// endpoints 503.
func (s *Server) SetPluginStore(in *plugin.Installer, indexURL string) {
	s.pluginInstaller = in
	s.pluginIndexURL = indexURL
}

// handlePluginStore fetches the catalog and marks which entries are
// already installed so the UI can show Install vs Installed.
func (s *Server) handlePluginStore(w http.ResponseWriter, r *http.Request) {
	if s.pluginIndexURL == "" {
		writeError(w, http.StatusServiceUnavailable, "store_disabled", "plugin store not configured")
		return
	}
	cat, err := plugin.FetchCatalog(r.Context(), &http.Client{Timeout: 15 * time.Second}, s.pluginIndexURL)
	if err != nil {
		writeError(w, http.StatusBadGateway, "store_unreachable", err.Error())
		return
	}
	type entry struct {
		plugin.CatalogEntry
		Installed bool `json:"installed"`
	}
	out := make([]entry, 0, len(cat.Plugins))
	for _, e := range cat.Plugins {
		_, installed := s.plugins.Get(e.ID)
		out = append(out, entry{CatalogEntry: e, Installed: installed})
	}
	writeJSON(w, http.StatusOK, map[string]any{"plugins": out})
}

// handlePluginInstall downloads + verifies + unpacks a package. An
// untrusted (unsigned / not-our-key) package returns 409 with
// needs_confirmation:true; the UI then re-POSTs with confirm:true
// after the operator acknowledges running third-party code.
func (s *Server) handlePluginInstall(w http.ResponseWriter, r *http.Request) {
	if s.pluginInstaller == nil {
		writeError(w, http.StatusServiceUnavailable, "store_disabled", "plugin installer not configured")
		return
	}
	var body struct {
		URL     string `json:"url"`
		SigURL  string `json:"sig_url"`
		Confirm bool   `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	res, err := s.pluginInstaller.Install(r.Context(), plugin.InstallRequest{
		URL: body.URL, SigURL: body.SigURL, Confirm: body.Confirm,
	})
	if err != nil {
		if errors.Is(err, plugin.ErrConfirmationRequired) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": map[string]any{
					"code":    "confirmation_required",
					"message": "this package is not signed by a trusted key — confirm to install third-party code",
				},
				"needs_confirmation": true,
			})
			return
		}
		writeError(w, http.StatusUnprocessableEntity, "install_failed", err.Error())
		return
	}

	// Pick up the freshly-installed plugin (starts disabled) and
	// reconcile processes.
	if err := s.plugins.Discover(); err != nil {
		// Non-fatal: the install landed; bad siblings are just skipped.
		_ = err
	}
	if s.pluginSync != nil {
		s.pluginSync()
	}
	p, _ := s.plugins.Get(res.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"installed": res.ID,
		"trusted":   res.Trusted,
		"plugin":    p,
	})
}

// handlePluginUninstall stops the plugin, removes its directory, and
// re-discovers so it drops off the list.
func (s *Server) handlePluginUninstall(w http.ResponseWriter, r *http.Request) {
	if s.pluginInstaller == nil {
		writeError(w, http.StatusServiceUnavailable, "store_disabled", "plugin installer not configured")
		return
	}
	id := chi.URLParam(r, "id")
	if _, ok := s.plugins.Get(id); !ok {
		writeError(w, http.StatusNotFound, "plugin_not_found", "no such plugin: "+id)
		return
	}
	// Disable first so the supervisor stops the process, then remove.
	s.plugins.SetEnabled(id, false)
	if s.pluginSync != nil {
		s.pluginSync()
	}
	if err := s.pluginInstaller.Uninstall(id); err != nil {
		writeError(w, http.StatusInternalServerError, "uninstall_failed", err.Error())
		return
	}
	if err := s.plugins.Discover(); err != nil {
		_ = err
	}
	// Persist the now-smaller enabled set.
	s.mu.Lock()
	cfg := s.cfg
	enabledMap := s.plugins.EnabledMap()
	cfg.Plugins = make(map[string]config.PluginConfig, len(enabledMap))
	for pid := range enabledMap {
		cfg.Plugins[pid] = config.PluginConfig{Enabled: true}
	}
	s.cfg = cfg
	s.mu.Unlock()
	_ = config.SaveWith(s.configPath, cfg, s.sealer)

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "uninstalled": id})
}

// pluginJSON is a Plugin enriched with its live process state for the
// /api/plugins list, so the UI can show running / crashed badges.
type pluginJSON struct {
	plugin.Plugin
	Runtime *plugin.ProcStatus `json:"runtime,omitempty"`
}

func (s *Server) pluginsWithRuntime() []pluginJSON {
	list := s.plugins.List()
	out := make([]pluginJSON, 0, len(list))
	for _, p := range list {
		pj := pluginJSON{Plugin: p}
		if s.pluginSup != nil {
			if st, ok := s.pluginSup.Status(p.ID); ok {
				pj.Runtime = &st
			}
		}
		out = append(out, pj)
	}
	return out
}
