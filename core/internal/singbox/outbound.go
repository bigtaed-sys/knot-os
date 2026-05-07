package singbox

import (
	"errors"
	"fmt"
)

// OutboundType identifies which protocol an outbound speaks.
// Strings match sing-box's expected `type` field exactly.
type OutboundType string

const (
	OutboundDirect      OutboundType = "direct"
	OutboundBlock       OutboundType = "block"
	OutboundSelector    OutboundType = "selector"
	OutboundURLTest     OutboundType = "urltest"
	OutboundVLESS       OutboundType = "vless"
	OutboundVMess       OutboundType = "vmess"
	OutboundTrojan      OutboundType = "trojan"
	OutboundShadowsocks OutboundType = "shadowsocks"
	OutboundWireGuard   OutboundType = "wireguard"
)

// Outbound is one server-or-policy. Knotd holds these in memory,
// renders them to JSON. Each field is optional unless the type
// makes it required (validated on RenderJSON).
//
// One struct shared across protocols keeps the config-rendering
// code simple — the bool/empty-string defaults turn unused fields
// into JSON-omittable cleanly. Cost is a wider struct than
// strictly necessary; on a couple-dozen outbounds it's noise.
type Outbound struct {
	// Tag is the unique identifier used by route rules + UI.
	// Convention: "<sub-id>:<server-id>" for subscription-derived
	// servers, plain words for built-ins ("direct", "block").
	Tag string

	// Type picks the protocol.
	Type OutboundType

	// DisplayName is what the UI shows for this outbound. Plain
	// text, optional emoji (provider often includes a country
	// flag in the URI fragment). Not used by sing-box; we carry
	// it through for round-trips with the API surface.
	DisplayName string

	// --- transport-layer fields --------------------------------

	// Server is the upstream host (TLS host or IP). Required for
	// every protocol-typed outbound.
	Server string
	// Port is the upstream port. Required.
	Port int

	// --- protocol auth fields ----------------------------------

	// UUID is used by VLESS + VMess. The familiar 8-4-4-4-12 hex.
	UUID string
	// Method is the cipher for Shadowsocks ("2022-blake3-aes-256-gcm",
	// "aes-256-gcm", "chacha20-ietf-poly1305", ...).
	Method string
	// Password is used by Shadowsocks + Trojan.
	Password string
	// AlterID is a legacy VMess field. New configs use 0.
	AlterID int

	// --- TLS / REALITY -----------------------------------------

	// TLS, when non-nil, enables TLS (or REALITY when REALITY
	// inside is set). Most modern VLESS configs use REALITY.
	TLS *TLSOptions

	// --- transport (multiplexing layer) ------------------------

	// Transport is "tcp" (default), "ws", "grpc", "http", "quic".
	// The corresponding fields below are only relevant for the
	// matching transport.
	Transport string
	WSPath    string
	WSHost    string // overrides the Host header for WS
	GRPCName  string // service name for gRPC transport
	HTTPPath  string

	// --- WireGuard --------------------------------------------

	// WGPrivateKey is the peer's private key (base64). Required
	// when Type == OutboundWireGuard.
	WGPrivateKey string
	// WGPeerPublicKey is the server's public key (base64).
	WGPeerPublicKey string
	// WGLocalAddresses are the addresses the wg interface gets
	// (e.g. ["10.66.66.2/32"]).
	WGLocalAddresses []string
	// WGMTU defaults to 1420.
	WGMTU int

	// --- Selector / URLTest ------------------------------------

	// Members are the tags of other outbounds this one chooses
	// from. Required for selector / urltest types.
	Members []string
	// URLTestURL is the probe URL for urltest (HTTP 204 endpoint).
	URLTestURL string
	// URLTestInterval is the seconds between probes.
	URLTestIntervalSec int
	// URLTestTolerance, in milliseconds, prevents flapping when
	// two members have similar latency.
	URLTestToleranceMS int
}

// toJSON converts an Outbound to the map shape sing-box parses.
// Fields are omitted when zero so the output stays compact.
func (o Outbound) toJSON() (map[string]any, error) {
	if o.Tag == "" {
		return nil, errors.New("empty tag")
	}
	out := map[string]any{
		"type": string(o.Type),
		"tag":  o.Tag,
	}

	switch o.Type {
	case OutboundDirect, OutboundBlock:
		// No further config.
		return out, nil

	case OutboundSelector:
		if len(o.Members) == 0 {
			return nil, errors.New("selector outbound requires Members")
		}
		out["outbounds"] = o.Members
		return out, nil

	case OutboundURLTest:
		if len(o.Members) == 0 {
			return nil, errors.New("urltest outbound requires Members")
		}
		out["outbounds"] = o.Members
		if o.URLTestURL != "" {
			out["url"] = o.URLTestURL
		} else {
			out["url"] = "http://www.gstatic.com/generate_204"
		}
		if o.URLTestIntervalSec > 0 {
			out["interval"] = fmt.Sprintf("%ds", o.URLTestIntervalSec)
		} else {
			out["interval"] = "3m"
		}
		if o.URLTestToleranceMS > 0 {
			out["tolerance"] = o.URLTestToleranceMS
		}
		return out, nil
	}

	// Server-bound protocols share these fields.
	if o.Server == "" || o.Port <= 0 {
		return nil, fmt.Errorf("%s outbound requires server + port", o.Type)
	}
	out["server"] = o.Server
	out["server_port"] = o.Port

	switch o.Type {
	case OutboundVLESS:
		if o.UUID == "" {
			return nil, errors.New("vless requires UUID")
		}
		out["uuid"] = o.UUID
		// VLESS supports a "flow" parameter, mostly "xtls-rprx-vision"
		// for REALITY+vision. We pass it through TLSOptions.

	case OutboundVMess:
		if o.UUID == "" {
			return nil, errors.New("vmess requires UUID")
		}
		out["uuid"] = o.UUID
		out["alter_id"] = o.AlterID

	case OutboundTrojan:
		if o.Password == "" {
			return nil, errors.New("trojan requires password")
		}
		out["password"] = o.Password

	case OutboundShadowsocks:
		if o.Method == "" || o.Password == "" {
			return nil, errors.New("shadowsocks requires method + password")
		}
		out["method"] = o.Method
		out["password"] = o.Password

	case OutboundWireGuard:
		if o.WGPrivateKey == "" || o.WGPeerPublicKey == "" {
			return nil, errors.New("wireguard requires keys")
		}
		out["private_key"] = o.WGPrivateKey
		peer := map[string]any{
			"server":      o.Server,
			"server_port": o.Port,
			"public_key":  o.WGPeerPublicKey,
			"allowed_ips": []string{"0.0.0.0/0", "::/0"},
		}
		out["peers"] = []any{peer}
		// `server` / `server_port` at the top level are unused for
		// WG (the peers array carries them); strip them to avoid
		// upstream complaints in newer versions.
		delete(out, "server")
		delete(out, "server_port")
		if len(o.WGLocalAddresses) == 0 {
			return nil, errors.New("wireguard requires local_address")
		}
		out["local_address"] = o.WGLocalAddresses
		if o.WGMTU > 0 {
			out["mtu"] = o.WGMTU
		}

	default:
		return nil, fmt.Errorf("unknown outbound type %q", o.Type)
	}

	// TLS layer (skipped for WireGuard which has its own crypto).
	if o.Type != OutboundWireGuard && o.TLS != nil {
		tls, err := o.TLS.toJSON()
		if err != nil {
			return nil, err
		}
		out["tls"] = tls
		if o.Type == OutboundVLESS && o.TLS.VLESSFlow != "" {
			out["flow"] = o.TLS.VLESSFlow
		}
	}

	// Transport (mux layer above the TCP/TLS stream).
	if o.Type != OutboundWireGuard && o.Transport != "" && o.Transport != "tcp" {
		tr := map[string]any{"type": o.Transport}
		switch o.Transport {
		case "ws":
			if o.WSPath != "" {
				tr["path"] = o.WSPath
			}
			if o.WSHost != "" {
				tr["headers"] = map[string]any{"Host": o.WSHost}
			}
		case "grpc":
			if o.GRPCName == "" {
				return nil, errors.New("grpc transport requires service_name")
			}
			tr["service_name"] = o.GRPCName
		case "http":
			if o.HTTPPath != "" {
				tr["path"] = []string{o.HTTPPath}
			}
		case "quic":
			// QUIC has no extra fields by default.
		default:
			return nil, fmt.Errorf("unknown transport %q", o.Transport)
		}
		out["transport"] = tr
	}

	return out, nil
}
