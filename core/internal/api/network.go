package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/knot-os/knot-os/core/internal/config"
	"github.com/knot-os/knot-os/core/internal/network/capability"
)

// MountNetwork registers the post-setup Ethernet-port management
// endpoints:
//
//	GET /network/ports — detected ports + their current WAN/LAN role
//	PUT /network/ports — reassign which port is WAN and which are LAN
//
// This is the running counterpart to the first-run wizard's hardware
// step: it lets the user change, without re-running setup, which
// Ethernet port carries the internet (WAN) and which are switched into
// the LAN. Only meaningful in the wifi-router role.
func (s *Server) MountNetwork(r chi.Router) {
	r.Get("/network/ports", s.handleGetNetworkPorts)
	r.Put("/network/ports", s.handlePutNetworkPorts)
}

// portView is one Ethernet port plus the role the current config gives
// it, for the management UI.
type portView struct {
	capability.EthAdapter
	// Role is "wan", "lan", or "unused" under the active config.
	Role string `json:"role"`
}

func (s *Server) handleGetNetworkPorts(w http.ResponseWriter, _ *http.Request) {
	// Bring wired links up first so carrier reads true on a cabled
	// port (same reason as the setup wizard) — see setup_linkup_*.go.
	bringUSBEthUp()
	rep, err := capability.Probe{}.Run()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "probe_failed", err.Error())
		return
	}

	cfg := s.Snapshot()
	wan := ""
	var lanPorts []string
	if cfg.Network.WAN != nil {
		wan = cfg.Network.WAN.Interface
	}
	lanPorts = cfg.Network.LANPorts
	lanSet := map[string]bool{}
	for _, p := range lanPorts {
		lanSet[p] = true
	}

	ports := make([]portView, 0, len(rep.Eth))
	for _, e := range rep.Eth {
		role := "unused"
		switch {
		case e.Interface == wan && wan != "":
			role = "wan"
		case lanSet[e.Interface]:
			role = "lan"
		}
		ports = append(ports, portView{EthAdapter: e, Role: role})
	}
	if lanPorts == nil {
		lanPorts = []string{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"pi_model_string": rep.PiModelString,
		"ports":           ports,
		"wan_interface":   wan,
		"lan_ports":       lanPorts,
		// router_mode tells the UI whether port assignment applies at
		// all — the extender role has no WAN of its own.
		"router_mode": cfg.Role == config.RoleWiFiRouter,
	})
}

func (s *Server) handlePutNetworkPorts(w http.ResponseWriter, r *http.Request) {
	var body struct {
		// WANInterface, when non-empty, becomes the new Ethernet WAN.
		// Empty leaves the current WAN untouched (e.g. a modem WAN, or
		// the UI only editing LAN ports).
		WANInterface string   `json:"wan_interface"`
		LANPorts     []string `json:"lan_ports"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	incoming := s.Snapshot()
	if incoming.Role != config.RoleWiFiRouter {
		writeError(w, http.StatusConflict, "not_router",
			"Ethernet port assignment is only available in the wifi-router role")
		return
	}
	if incoming.Network.WAN == nil {
		// Shouldn't happen in router role (Validate guarantees it), but
		// guard so we never nil-deref.
		writeError(w, http.StatusConflict, "no_wan", "router config has no WAN block")
		return
	}
	if body.WANInterface != "" {
		incoming.Network.WAN.Interface = body.WANInterface
		// Assigning a concrete Ethernet WAN implies dhcp mode; a modem
		// WAN never comes through this endpoint (it has no eth iface).
		if incoming.Network.WAN.Mode == "modem" {
			incoming.Network.WAN.Mode = "dhcp"
			incoming.Network.WAN.Modem = nil
		}
	}
	incoming.Network.LANPorts = body.LANPorts

	// Full network apply — changing WAN/LAN ports re-lays interfaces,
	// the bridge, NAT and the AP, so this goes through backend.Apply
	// (via commitConfig), not the light path.
	status, payload := s.commitConfig(r.Context(), incoming, "api:put-network-ports")
	writeJSON(w, status, payload)
}
