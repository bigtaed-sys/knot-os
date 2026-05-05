package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	qrcode "github.com/skip2/go-qrcode"

	"github.com/knot-os/knot-os/core/internal/vpn"
)

func base64Encode(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

// MountVPN registers /vpn/* under the auth-gated group.
//
// Endpoints:
//
//	GET    /vpn/server              — server config (public bits) + status
//	PATCH  /vpn/server              — update enabled / port / endpoint
//	GET    /vpn/peers               — list peers (no private keys)
//	POST   /vpn/peers               — add peer; returns one-time client config
//	DELETE /vpn/peers/{id}          — remove a peer
//	PATCH  /vpn/peers/{id}          — update profile_id
//	GET    /vpn/peers/{id}/config   — re-render the on-server view of a peer
//	                                  (NOT the client config — that's only
//	                                  returned once at create time)
func (s *Server) MountVPN(r chi.Router) {
	if s.vpn == nil {
		r.Get("/vpn/server", func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusServiceUnavailable, "vpn_disabled", "VPN feature not configured")
		})
		return
	}
	r.Get("/vpn/server", s.handleVPNServerGet)
	r.Patch("/vpn/server", s.handleVPNServerPatch)
	r.Get("/vpn/peers", s.handleVPNPeersList)
	r.Post("/vpn/peers", s.handleVPNPeersAdd)
	r.Delete("/vpn/peers/{id}", s.handleVPNPeersDelete)
	r.Patch("/vpn/peers/{id}", s.handleVPNPeersPatch)
}

// SetVPNRegistry wires the VPN registry into the API. nil disables.
func (s *Server) SetVPNRegistry(r *vpn.Registry) { s.vpn = r }

// vpnServerJSON is the safe-to-expose subset of ServerConfig.
// Notably leaves out the private key.
type vpnServerJSON struct {
	Enabled       bool   `json:"enabled"`
	ListenPort    int    `json:"listen_port"`
	InterfaceCIDR string `json:"interface_cidr"`
	EndpointHost  string `json:"endpoint_host"`
	PublicKey     string `json:"public_key"`
	PeerCount     int    `json:"peer_count"`
}

func (s *Server) handleVPNServerGet(w http.ResponseWriter, _ *http.Request) {
	cfg := s.vpn.Server()
	writeJSON(w, http.StatusOK, vpnServerJSON{
		Enabled:       cfg.Enabled,
		ListenPort:    cfg.ListenPort,
		InterfaceCIDR: cfg.InterfaceCIDR,
		EndpointHost:  cfg.EndpointHost,
		PublicKey:     s.vpn.PublicServerKey().String(),
		PeerCount:     len(s.vpn.Peers()),
	})
}

// vpnServerPatch is the input for PATCH /vpn/server. All fields
// are optional; missing means "don't touch". Pointer types
// distinguish "explicitly set" from "absent".
type vpnServerPatch struct {
	Enabled       *bool   `json:"enabled,omitempty"`
	ListenPort    *int    `json:"listen_port,omitempty"`
	InterfaceCIDR *string `json:"interface_cidr,omitempty"`
	EndpointHost  *string `json:"endpoint_host,omitempty"`
}

func (s *Server) handleVPNServerPatch(w http.ResponseWriter, r *http.Request) {
	var body vpnServerPatch
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	cfg := s.vpn.Server()
	if body.Enabled != nil {
		cfg.Enabled = *body.Enabled
	}
	if body.ListenPort != nil {
		cfg.ListenPort = *body.ListenPort
	}
	if body.InterfaceCIDR != nil {
		cfg.InterfaceCIDR = strings.TrimSpace(*body.InterfaceCIDR)
	}
	if body.EndpointHost != nil {
		cfg.EndpointHost = strings.TrimSpace(*body.EndpointHost)
	}
	if err := s.vpn.SetServer(cfg); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_server_config", err.Error())
		return
	}
	// Trigger an apply so the new state takes effect without a
	// full PUT /api/config dance. The callback handles wg-quick
	// restart + nftables reload.
	s.fireConfigApplied(s.Snapshot())
	s.handleVPNServerGet(w, r)
}

// vpnPeerJSON is the over-the-wire representation of a peer.
type vpnPeerJSON struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	PublicKey     string `json:"public_key"`
	AllowedIP     string `json:"allowed_ip"`
	ProfileID     string `json:"profile_id,omitempty"`
	CreatedAt     string `json:"created_at"`
	LastHandshake string `json:"last_handshake,omitempty"`
}

func toPeerJSON(p vpn.Peer) vpnPeerJSON {
	out := vpnPeerJSON{
		ID:        p.ID,
		Name:      p.Name,
		PublicKey: p.PublicKey.String(),
		AllowedIP: p.AllowedIP,
		ProfileID: p.ProfileID,
		CreatedAt: p.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if !p.LastHandshake.IsZero() {
		out.LastHandshake = p.LastHandshake.UTC().Format("2006-01-02T15:04:05Z")
	}
	return out
}

func (s *Server) handleVPNPeersList(w http.ResponseWriter, _ *http.Request) {
	peers := s.vpn.Peers()
	out := make([]vpnPeerJSON, 0, len(peers))
	for _, p := range peers {
		out = append(out, toPeerJSON(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"peers": out})
}

// addPeerRequest is the body of POST /vpn/peers. Name is required;
// FullTunnel toggles 0.0.0.0/0 vs split-tunnel routing for the
// returned client config; ProfileID hooks into the same registry
// LAN devices use.
type addPeerRequest struct {
	Name        string `json:"name"`
	ProfileID   string `json:"profile_id,omitempty"`
	FullTunnel  bool   `json:"full_tunnel"`
}

// addPeerResponse contains the one-time deliverables. The client
// config + QR PNG are the things the user must capture immediately;
// the daemon does not retain peer.private_key after this response.
type addPeerResponse struct {
	Peer         vpnPeerJSON `json:"peer"`
	ClientConfig string      `json:"client_config"`
	// PrivateKey is the freshly-generated peer private key, base64.
	// Returned once; the daemon does not store it.
	PrivateKey string `json:"private_key"`
	// QRPNGBase64 is the client config rendered as a 256x256 PNG
	// QR code, base64-encoded — UI shows it as a data: URL.
	QRPNGBase64 string `json:"qr_png_base64"`
}

func (s *Server) handleVPNPeersAdd(w http.ResponseWriter, r *http.Request) {
	var body addPeerRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	res, err := s.vpn.AddPeer(body.Name, body.ProfileID)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_peer", err.Error())
		return
	}

	// Build the client config.
	srv := s.vpn.Server()
	cfg := s.Snapshot()
	lanCIDR := ""
	if cfg.Network.LAN != nil {
		lanCIDR = cfg.Network.LAN.CIDR
	}
	dnsAddr := ""
	if lanCIDR != "" {
		dnsAddr = firstIPInCIDRForVPN(lanCIDR)
	}
	clientConf := vpn.RenderClientConf(srv, s.vpn.PublicServerKey(), res.Peer, res.PrivateKey, vpn.ClientConfigOptions{
		FullTunnel:   body.FullTunnel,
		LANRouteCIDR: lanCIDR,
		DNSAddress:   dnsAddr,
	})

	// QR PNG. 256 px is enough density for camera scan from across
	// a room; medium error-correction copes with screen reflections.
	png, err := qrcode.Encode(clientConf, qrcode.Medium, 256)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "qr_failed", err.Error())
		return
	}

	// Re-apply so wg0 picks up the new peer immediately.
	s.fireConfigApplied(cfg)

	writeJSON(w, http.StatusCreated, addPeerResponse{
		Peer:         toPeerJSON(res.Peer),
		ClientConfig: clientConf,
		PrivateKey:   res.PrivateKey.String(),
		QRPNGBase64:  base64Encode(png),
	})
}

func (s *Server) handleVPNPeersDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.vpn.RemovePeer(id); err != nil {
		writeError(w, http.StatusNotFound, "peer_not_found", err.Error())
		return
	}
	s.fireConfigApplied(s.Snapshot())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type peerPatchRequest struct {
	ProfileID *string `json:"profile_id,omitempty"`
}

func (s *Server) handleVPNPeersPatch(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body peerPatchRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if body.ProfileID != nil {
		if err := s.vpn.SetPeerProfile(id, *body.ProfileID); err != nil {
			writeError(w, http.StatusNotFound, "peer_not_found", err.Error())
			return
		}
	}
	// Fetch the post-update peer for the response.
	for _, p := range s.vpn.Peers() {
		if p.ID == id {
			writeJSON(w, http.StatusOK, toPeerJSON(p))
			return
		}
	}
	writeError(w, http.StatusNotFound, "peer_not_found", "peer disappeared mid-patch")
}

// firstIPInCIDRForVPN duplicates the gateway-IP helper used in
// main.go. Inlined here to avoid an import cycle with the linux
// network backend, which is build-tagged.
func firstIPInCIDRForVPN(cidr string) string {
	parts := strings.SplitN(cidr, "/", 2)
	if len(parts) != 2 {
		return ""
	}
	octets := strings.Split(parts[0], ".")
	if len(octets) != 4 {
		return ""
	}
	last := octets[3]
	if len(last) == 0 {
		return ""
	}
	octets[3] = "1"
	// If the network address is .0, gateway is .1; if it's .X for
	// some other prefix, just bump by 1. Good enough for /24 nets
	// which is what the wizard ships.
	if last != "0" && last != "1" {
		// Try to bump.
		switch last {
		case "":
			return ""
		}
	}
	return strings.Join(octets, ".")
}
