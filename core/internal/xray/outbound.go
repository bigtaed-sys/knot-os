package xray

import (
	"fmt"

	"github.com/knot-os/knot-os/core/internal/singbox"
)

// renderOutbound translates one parsed server (held in the shared
// singbox.Outbound model) into an Xray-core outbound object. Xray's
// config grammar differs from sing-box's: protocol-specific auth
// lives under `settings.vnext`/`settings.servers`, and the transport
// + TLS layer lives under `streamSettings`.
//
// We only render the protocols the subscription parser can produce
// (vless / vmess / trojan / shadowsocks). The whole point of the
// Xray engine in KnotOS is the transports sing-box refuses — chiefly
// "xhttp" — so the transport switch is the part that earns its keep.
func renderOutbound(o singbox.Outbound, tag string) (map[string]any, error) {
	if o.Server == "" || o.Port <= 0 {
		return nil, fmt.Errorf("xray outbound %q: server + port required", tag)
	}

	out := map[string]any{"tag": tag}

	switch o.Type {
	case singbox.OutboundVLESS:
		if o.UUID == "" {
			return nil, fmt.Errorf("xray vless %q: UUID required", tag)
		}
		user := map[string]any{"id": o.UUID, "encryption": "none"}
		if o.TLS != nil && o.TLS.VLESSFlow != "" {
			user["flow"] = o.TLS.VLESSFlow
		}
		out["protocol"] = "vless"
		out["settings"] = map[string]any{
			"vnext": []any{map[string]any{
				"address": o.Server,
				"port":    o.Port,
				"users":   []any{user},
			}},
		}

	case singbox.OutboundVMess:
		if o.UUID == "" {
			return nil, fmt.Errorf("xray vmess %q: UUID required", tag)
		}
		out["protocol"] = "vmess"
		out["settings"] = map[string]any{
			"vnext": []any{map[string]any{
				"address": o.Server,
				"port":    o.Port,
				"users": []any{map[string]any{
					"id":       o.UUID,
					"alterId":  o.AlterID,
					"security": "auto",
				}},
			}},
		}

	case singbox.OutboundTrojan:
		if o.Password == "" {
			return nil, fmt.Errorf("xray trojan %q: password required", tag)
		}
		out["protocol"] = "trojan"
		out["settings"] = map[string]any{
			"servers": []any{map[string]any{
				"address":  o.Server,
				"port":     o.Port,
				"password": o.Password,
			}},
		}

	case singbox.OutboundShadowsocks:
		if o.Method == "" || o.Password == "" {
			return nil, fmt.Errorf("xray shadowsocks %q: method + password required", tag)
		}
		out["protocol"] = "shadowsocks"
		out["settings"] = map[string]any{
			"servers": []any{map[string]any{
				"address":  o.Server,
				"port":     o.Port,
				"method":   o.Method,
				"password": o.Password,
			}},
		}

	default:
		return nil, fmt.Errorf("xray outbound %q: unsupported protocol %q", tag, o.Type)
	}

	stream, err := renderStream(o, tag)
	if err != nil {
		return nil, err
	}
	out["streamSettings"] = stream
	return out, nil
}

// renderStream builds the Xray `streamSettings` block: the L4/L7
// transport plus the TLS/REALITY camouflage.
func renderStream(o singbox.Outbound, tag string) (map[string]any, error) {
	network := o.Transport
	if network == "" {
		network = "tcp"
	}
	stream := map[string]any{"network": network}

	switch network {
	case "tcp":
		// No transport-specific settings.
	case "ws":
		ws := map[string]any{}
		if o.WSPath != "" {
			ws["path"] = o.WSPath
		}
		if o.WSHost != "" {
			ws["headers"] = map[string]any{"Host": o.WSHost}
		}
		stream["wsSettings"] = ws
	case "grpc":
		if o.GRPCName == "" {
			return nil, fmt.Errorf("xray %q: grpc requires serviceName", tag)
		}
		stream["grpcSettings"] = map[string]any{"serviceName": o.GRPCName}
	case "http", "h2":
		stream["network"] = "http"
		h := map[string]any{}
		if o.HTTPPath != "" {
			h["path"] = o.HTTPPath
		}
		stream["httpSettings"] = h
	case "xhttp":
		x := map[string]any{}
		if o.XHTTPPath != "" {
			x["path"] = o.XHTTPPath
		}
		if o.XHTTPHost != "" {
			x["host"] = o.XHTTPHost
		}
		if o.XHTTPMode != "" {
			x["mode"] = o.XHTTPMode
		}
		stream["xhttpSettings"] = x
	case "httpupgrade":
		h := map[string]any{}
		if o.WSPath != "" {
			h["path"] = o.WSPath
		}
		if o.WSHost != "" {
			h["host"] = o.WSHost
		}
		stream["httpupgradeSettings"] = h
	default:
		return nil, fmt.Errorf("xray %q: unknown transport %q", tag, network)
	}

	// Security layer.
	if o.TLS != nil && o.TLS.Enabled {
		if o.TLS.REALITY != nil && o.TLS.REALITY.Enabled {
			if o.TLS.REALITY.PublicKey == "" || o.TLS.SNI == "" {
				return nil, fmt.Errorf("xray %q: reality requires publicKey + SNI", tag)
			}
			stream["security"] = "reality"
			r := map[string]any{
				"serverName": o.TLS.SNI,
				"publicKey":  o.TLS.REALITY.PublicKey,
			}
			if o.TLS.REALITY.ShortID != "" {
				r["shortId"] = o.TLS.REALITY.ShortID
			}
			if o.TLS.UTLSFingerprint != "" {
				r["fingerprint"] = o.TLS.UTLSFingerprint
			} else {
				// REALITY needs a fingerprint; default to chrome to
				// match what most providers' clients send.
				r["fingerprint"] = "chrome"
			}
			stream["realitySettings"] = r
		} else {
			stream["security"] = "tls"
			t := map[string]any{}
			if o.TLS.SNI != "" {
				t["serverName"] = o.TLS.SNI
			}
			if len(o.TLS.ALPN) > 0 {
				t["alpn"] = o.TLS.ALPN
			}
			if o.TLS.UTLSFingerprint != "" {
				t["fingerprint"] = o.TLS.UTLSFingerprint
			}
			if o.TLS.Insecure {
				t["allowInsecure"] = true
			}
			stream["tlsSettings"] = t
		}
	}

	return stream, nil
}

// CanRender reports nil if Xray can host this outbound, or the
// render error otherwise. The routing layer uses it to decide which
// engine takes a given server.
func CanRender(o singbox.Outbound) error {
	_, err := renderOutbound(o, "probe")
	return err
}
