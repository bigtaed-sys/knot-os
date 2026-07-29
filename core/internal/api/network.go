package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/knot-os/knot-os/core/internal/config"
	"github.com/knot-os/knot-os/core/internal/network"
	"github.com/knot-os/knot-os/core/internal/network/capability"
)

// MountNetwork registers the post-setup network settings endpoints —
// the running counterpart to the first-run wizard. Everything the
// wizard decides once (role, where the internet comes from, the Wi-Fi
// AP, wired LAN ports, the LAN subnet) is editable here at any time:
//
//	GET  /network       — current network config + detected hardware
//	GET  /network/scan  — scan for upstream Wi-Fi (for extender uplink)
//	PUT  /network       — apply a full network-settings change
//
// PUT goes through the transactional apply path (snapshot → apply →
// health-check → auto-rollback), so a change that fails to bring the
// AP back up is reverted automatically. WAN source is an explicit
// field, so assigning an Ethernet port can never silently switch a
// working cellular WAN off — the footgun the old ports-only page had.
func (s *Server) MountNetwork(r chi.Router) {
	r.Get("/network", s.handleGetNetwork)
	r.Get("/network/scan", s.handleNetworkScan)
	r.Put("/network", s.handlePutNetwork)
}

// portView is one Ethernet port plus the role the active config gives
// it ("wan" | "lan" | "unused"), for the settings UI.
type portView struct {
	capability.EthAdapter
	Role string `json:"role"`
}

func (s *Server) handleGetNetwork(w http.ResponseWriter, r *http.Request) {
	// Bring wired links up first so carrier reads true on a cabled
	// port (same reason as the setup wizard) — see setup_linkup_*.go.
	bringUSBEthUp()
	rep, _ := capability.Probe{}.Run()

	cfg := s.Snapshot()
	wan := ""
	if cfg.Network.WAN != nil {
		wan = cfg.Network.WAN.Interface
	}
	lanSet := map[string]bool{}
	for _, p := range cfg.Network.LANPorts {
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

	// Live modem presence/state so the WAN tab can show whether a SIM
	// is actually there before the user picks "modem".
	modem := network.ModemStatus{Present: false}
	if mp, ok := s.backend.(modemStatusProvider); ok {
		if got, err := mp.ModemStatus(r.Context()); err == nil {
			modem = got
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"role":             cfg.Role,
		"network":          cfg.Network,
		"ports":            ports,
		"modem":            modem,
		"pi_model_string":  rep.PiModelString,
		"five_ghz_capable": rep.FiveGHzCapable,
	})
}

// handleNetworkScan runs a Wi-Fi scan for the extender uplink picker.
// Authenticated (unlike /setup/scan, which is setup-role only), so the
// settings page can offer a network list when switching to extender.
func (s *Server) handleNetworkScan(w http.ResponseWriter, r *http.Request) {
	networks, err := s.backend.Scan(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "scan_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"networks": networks})
}

// networkSettingsReq is the PUT /network body. Every block is optional;
// only what's present is changed, except on a role switch where the new
// role's required blocks must be supplied (Validate enforces this).
type networkSettingsReq struct {
	// Role: "wifi-router" or "wifi-extender". Empty keeps the current.
	Role string `json:"role"`

	WAN *struct {
		// Mode is the explicit WAN source: "dhcp" (Ethernet) or "modem".
		Mode      string `json:"mode"`
		Interface string `json:"interface"`
		Modem     *struct {
			APN           string `json:"apn"`
			PIN           string `json:"pin"`
			Username      string `json:"username"`
			SIMSlot       int    `json:"sim_slot"`
			DataLimitMB   int    `json:"data_limit_mb"`
			CycleResetDay int    `json:"cycle_reset_day"`
		} `json:"modem"`
	} `json:"wan"`

	LANPorts *[]string `json:"lan_ports"`

	AP *struct {
		SSID    string `json:"ssid"`
		PSK     string `json:"psk"`
		Band    string `json:"band"`
		Channel int    `json:"channel"`
	} `json:"ap"`

	Uplink *struct {
		SSID string `json:"ssid"`
		PSK  string `json:"psk"`
	} `json:"uplink"`

	LAN *struct {
		CIDR string `json:"cidr"`
		DHCP struct {
			PoolStart string `json:"pool_start"`
			PoolEnd   string `json:"pool_end"`
		} `json:"dhcp"`
	} `json:"lan"`
}

func (s *Server) handlePutNetwork(w http.ResponseWriter, r *http.Request) {
	var req networkSettingsReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	incoming := s.Snapshot()
	if incoming.Role != config.RoleWiFiRouter && incoming.Role != config.RoleWiFiExtender {
		writeError(w, http.StatusConflict, "not_configured",
			"network settings are only editable after initial setup")
		return
	}

	role := incoming.Role
	if req.Role != "" {
		role = config.Role(req.Role)
	}
	incoming.Role = role

	// AP / LAN apply to both roles; overwrite only when supplied.
	if req.AP != nil {
		incoming.Network.AP = &config.WiFiAP{
			SSID: req.AP.SSID, PSK: req.AP.PSK, Band: req.AP.Band, Channel: req.AP.Channel,
		}
	}
	if req.LAN != nil {
		incoming.Network.LAN = &config.LAN{
			CIDR: req.LAN.CIDR,
			DHCP: config.DHCP{PoolStart: req.LAN.DHCP.PoolStart, PoolEnd: req.LAN.DHCP.PoolEnd},
		}
	}

	switch role {
	case config.RoleWiFiRouter:
		// Router: no Wi-Fi uplink. WAN is explicit — an Ethernet port
		// or a cellular modem, never inferred, so picking a port can't
		// silently disable a modem WAN.
		incoming.Network.Uplink = nil
		if req.WAN != nil {
			mode := req.WAN.Mode
			if mode == "" {
				mode = "dhcp"
			}
			wan := &config.WAN{Interface: req.WAN.Interface, Mode: mode}
			if mode == "modem" {
				wan.Interface = ""
				if req.WAN.Modem != nil {
					wan.Modem = &config.Modem{
						APN:           req.WAN.Modem.APN,
						PIN:           req.WAN.Modem.PIN,
						Username:      req.WAN.Modem.Username,
						SIMSlot:       req.WAN.Modem.SIMSlot,
						DataLimitMB:   req.WAN.Modem.DataLimitMB,
						CycleResetDay: req.WAN.Modem.CycleResetDay,
					}
				}
			}
			incoming.Network.WAN = wan
		}
		if req.LANPorts != nil {
			var ports []string
			wanIf := ""
			if incoming.Network.WAN != nil {
				wanIf = incoming.Network.WAN.Interface
			}
			for _, p := range *req.LANPorts {
				if p != "" && p != wanIf {
					ports = append(ports, p)
				}
			}
			incoming.Network.LANPorts = ports
		}

	case config.RoleWiFiExtender:
		// Extender: Wi-Fi uplink, no WAN / wired LAN ports.
		incoming.Network.WAN = nil
		incoming.Network.LANPorts = nil
		if req.Uplink != nil {
			incoming.Network.Uplink = &config.WiFiUplink{SSID: req.Uplink.SSID, PSK: req.Uplink.PSK}
		}

	default:
		writeError(w, http.StatusUnprocessableEntity, "invalid_role",
			"role must be \"wifi-router\" or \"wifi-extender\"")
		return
	}

	// Full network apply — re-lays interfaces, the bridge, NAT and the
	// AP — through commitConfig (snapshot + health-check + rollback).
	status, payload := s.commitConfig(r.Context(), incoming, "api:put-network")
	writeJSON(w, status, payload)
}
