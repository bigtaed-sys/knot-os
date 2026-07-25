package api

import (
	"encoding/json"
	"net"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/knot-os/knot-os/core/internal/config"
	"github.com/knot-os/knot-os/core/internal/tgproxy"
)

// SetTGProxyManager attaches the Telegram-bypass proxy manager.
func (s *Server) SetTGProxyManager(m *tgproxy.Manager) { s.tgproxy = m }

// MountTGProxy registers the Telegram-proxy endpoints:
//
//	GET  /tgproxy         — settings + status + the tg:// link
//	PUT  /tgproxy         — enable/disable, mode, port, secret
//	POST /tgproxy/secret  — generate a fresh MTProto secret
func (s *Server) MountTGProxy(r chi.Router) {
	r.Get("/tgproxy", s.handleGetTGProxy)
	r.Put("/tgproxy", s.handlePutTGProxy)
	r.Post("/tgproxy/secret", s.handleTGProxySecret)
}

// tgSettings builds the effective proxy Settings from config + the LAN
// gateway (used both to run the proxy and to render the tg:// link).
func (s *Server) tgSettings(cfg config.Config) tgproxy.Settings {
	t := cfg.Network.TGProxy
	if t == nil {
		t = &config.TGProxy{}
	}
	return tgproxy.Settings{
		Enabled: t.Enabled,
		Port:    t.Port,
		Secret:  t.Secret,
		LinkIP:  lanGatewayIP(cfg),
	}
}

func (s *Server) handleGetTGProxy(w http.ResponseWriter, _ *http.Request) {
	cfg := s.Snapshot()
	set := s.tgSettings(cfg)

	running, binaryPresent := false, false
	if s.tgproxy != nil {
		running = s.tgproxy.Running()
		binaryPresent = s.tgproxy.BinaryPresent()
	}
	port := set.Port
	if port == 0 {
		port = tgproxy.DefaultPort
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":    set.Enabled,
		"port":       port,
		"has_secret": set.Secret != "",
		"lan_ip":   set.LinkIP,
		"tg_link":  tgproxy.TGLink(set),
		"status": map[string]any{
			"running":        running,
			"binary_present": binaryPresent,
			"router_mode":    cfg.Role == config.RoleWiFiRouter,
		},
	})
}

func (s *Server) handlePutTGProxy(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool    `json:"enabled"`
		Port    int     `json:"port"`
		Secret  *string `json:"secret"` // nil = keep
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	incoming := s.Snapshot()
	t := config.TGProxy{}
	if incoming.Network.TGProxy != nil {
		t = *incoming.Network.TGProxy
	}
	t.Enabled = body.Enabled
	t.Mode = "mtproto"
	t.Port = body.Port
	if body.Secret != nil {
		t.Secret = *body.Secret
	}
	// MTProto always needs a secret — generate one if enabling without it.
	if t.Enabled && !tgproxy.ValidSecret(t.Secret) {
		sec, err := tgproxy.GenerateSecret()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "secret_failed", err.Error())
			return
		}
		t.Secret = sec
	}
	incoming.Network.TGProxy = &t

	// The proxy only drives a userspace listener, so persist + reconcile
	// without a full network re-apply (no Wi-Fi bounce).
	status, payload := s.persistConfigLight(incoming)
	writeJSON(w, status, payload)
}

func (s *Server) handleTGProxySecret(w http.ResponseWriter, _ *http.Request) {
	sec, err := tgproxy.GenerateSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "secret_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"secret": sec})
}

// lanGatewayIP returns the router's first-usable LAN IPv4 (the gateway
// clients connect to), or "" when the LAN CIDR is unset/invalid.
func lanGatewayIP(cfg config.Config) string {
	if cfg.Network.LAN == nil {
		return ""
	}
	ip, ipnet, err := net.ParseCIDR(cfg.Network.LAN.CIDR)
	if err != nil || ipnet == nil {
		return ""
	}
	v4 := ip.Mask(ipnet.Mask).To4()
	if v4 == nil {
		return ""
	}
	v4[3]++ // first usable host
	return v4.String()
}
