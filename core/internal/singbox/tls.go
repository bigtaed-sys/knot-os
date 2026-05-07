package singbox

import (
	"errors"
	"fmt"
)

// TLSOptions configures both vanilla TLS and REALITY-camouflaged
// TLS for outbounds that ride on top of TCP. The two are mutually
// exclusive at sing-box level — set either Insecure-style fields
// OR the REALITY block, not both.
//
// Most modern VLESS providers in 2026 use REALITY + Vision flow.
// The shape we render mirrors what they put in their VLESS URI:
//
//   vless://uuid@host:443?security=reality&pbk=...&fp=chrome&sni=microsoft.com&flow=xtls-rprx-vision&type=tcp&headerType=none#name
//
// → TLS{Enabled: true, SNI: "microsoft.com", UTLSFingerprint: "chrome",
//      VLESSFlow: "xtls-rprx-vision",
//      REALITY: &REALITY{Enabled: true, PublicKey: "..."}}
type TLSOptions struct {
	// Enabled is the master switch. False renders no `tls` block.
	Enabled bool

	// SNI is the TLS server-name. Mandatory for REALITY (it's the
	// camouflage target — sing-box mimics this domain's TLS
	// fingerprint).
	SNI string

	// ALPN is the protocol-negotiation list. Empty defaults to
	// sing-box's choice based on transport (h2 for ws, http/1.1
	// for plain TCP). Common explicit values: ["h2"], ["h2",
	// "http/1.1"].
	ALPN []string

	// Insecure skips certificate verification. For REALITY this
	// is implicit (the camouflage target's cert isn't actually
	// validated). For plain TLS it's a security regression and
	// we surface a warning when set.
	Insecure bool

	// UTLSFingerprint mimics a specific browser's TLS Client Hello
	// to defeat fingerprinting. Common values: "chrome", "firefox",
	// "safari", "ios", "edge", "random".
	UTLSFingerprint string

	// REALITY, when non-nil + Enabled, switches TLS to REALITY mode.
	REALITY *REALITYOptions

	// VLESSFlow is the VLESS-specific flow control parameter.
	// "xtls-rprx-vision" is the modern default. Empty for non-VLESS.
	VLESSFlow string
}

// REALITYOptions are the REALITY-specific keys.
type REALITYOptions struct {
	Enabled bool

	// PublicKey is the REALITY x25519 public key (base64) the
	// provider gives in their VLESS URI as `pbk=`.
	PublicKey string

	// ShortID is the optional REALITY short ID (`sid=` in the URI).
	// Some providers use empty; some use 4-16 hex chars.
	ShortID string
}

func (t *TLSOptions) toJSON() (map[string]any, error) {
	if t == nil || !t.Enabled {
		return nil, nil
	}
	out := map[string]any{
		"enabled": true,
	}
	if t.SNI != "" {
		out["server_name"] = t.SNI
	}
	if len(t.ALPN) > 0 {
		out["alpn"] = t.ALPN
	}
	if t.Insecure && t.REALITY == nil {
		// Only honour Insecure for plain TLS. REALITY's "insecure"
		// is an inherent property of the protocol, not an opt-in.
		out["insecure"] = true
	}
	if t.UTLSFingerprint != "" {
		out["utls"] = map[string]any{
			"enabled":     true,
			"fingerprint": t.UTLSFingerprint,
		}
	}
	if t.REALITY != nil && t.REALITY.Enabled {
		if t.REALITY.PublicKey == "" {
			return nil, errors.New("reality requires public_key")
		}
		if t.SNI == "" {
			return nil, errors.New("reality requires SNI")
		}
		r := map[string]any{
			"enabled":    true,
			"public_key": t.REALITY.PublicKey,
		}
		if t.REALITY.ShortID != "" {
			r["short_id"] = t.REALITY.ShortID
		}
		out["reality"] = r
	}
	return out, nil
}

// Validate sanity-checks the TLS options outside RenderJSON for
// callers that want to surface a problem at API layer (paste-conf
// validation) before hitting the rendering path.
func (t *TLSOptions) Validate() error {
	if t == nil || !t.Enabled {
		return nil
	}
	if t.REALITY != nil && t.REALITY.Enabled {
		if t.REALITY.PublicKey == "" {
			return fmt.Errorf("REALITY requires public_key")
		}
		if t.SNI == "" {
			return fmt.Errorf("REALITY requires SNI (server_name)")
		}
	}
	return nil
}
