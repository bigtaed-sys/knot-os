// Package subscription parses VPN-share URIs (vless://, vmess://,
// trojan://, ss://) and base64 subscription bundles into the
// internal singbox.Outbound shape. Pure data — no network calls,
// no disk. The fetcher (fetcher.go) and registry (registry.go)
// build on top of this.
//
// The parser is intentionally permissive: it accepts the subset of
// fields commonly emitted by Russian-market VLESS providers in 2026
// (REALITY + uTLS + Vision), and ignores unknown query parameters
// rather than rejecting the whole URI. Strict validation happens at
// singbox.Config.RenderJSON.
package subscription

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/knot-os/knot-os/core/internal/singbox"
)

// Errors returned by ParseURI / ParseBundle.
var (
	ErrEmptyURI         = errors.New("subscription: empty URI")
	ErrUnknownScheme    = errors.New("subscription: unknown scheme")
	ErrMalformedURI     = errors.New("subscription: malformed URI")
	ErrEmptyBundle      = errors.New("subscription: bundle is empty")
)

// ParseURI converts one share URI into a singbox.Outbound. The Tag
// is left empty — the caller is responsible for namespacing it
// (typically "<sub-id>:<n>").
func ParseURI(raw string) (singbox.Outbound, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return singbox.Outbound{}, ErrEmptyURI
	}
	switch {
	case strings.HasPrefix(raw, "vless://"):
		return parseVLESS(raw)
	case strings.HasPrefix(raw, "vmess://"):
		return parseVMess(raw)
	case strings.HasPrefix(raw, "trojan://"):
		return parseTrojan(raw)
	case strings.HasPrefix(raw, "ss://"):
		return parseShadowsocks(raw)
	default:
		return singbox.Outbound{}, fmt.Errorf("%w: %.16s...", ErrUnknownScheme, raw)
	}
}

// ParseBundle decodes a subscription body. Two formats are accepted:
//
//   1. base64 (with or without padding) of newline-separated URIs —
//      the de-facto standard for v2ray / sing-box providers.
//   2. plain text, newline-separated URIs (some providers skip the
//      base64 layer when serving from a custom endpoint).
//
// Lines that don't start with a recognised scheme are skipped
// silently — providers sometimes prepend a banner / motd.
//
// Returns the parsed outbounds in source order. Unparseable URIs
// are returned as a non-nil error slice alongside the successful
// outbounds, so the UI can show a "5 of 8 servers loaded" hint.
func ParseBundle(body []byte) ([]singbox.Outbound, []error) {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return nil, []error{ErrEmptyBundle}
	}

	// Try base64 first. We accept both standard and URL-safe alphabets
	// with or without padding — providers are inconsistent.
	if decoded, ok := tryBase64(text); ok {
		text = decoded
	}

	var outs []singbox.Outbound
	var errs []error
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !looksLikeShareURI(line) {
			continue
		}
		o, err := ParseURI(line)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		outs = append(outs, o)
	}
	if len(outs) == 0 && len(errs) == 0 {
		errs = append(errs, ErrEmptyBundle)
	}
	return outs, errs
}

// tryBase64 returns (decoded, true) if text decodes cleanly under
// any of the four common base64 variants. We test all four because
// providers split roughly evenly between them.
func tryBase64(text string) (string, bool) {
	cleaned := strings.Map(func(r rune) rune {
		// Strip whitespace inside the base64 blob — some providers
		// soft-wrap the body at 76 cols.
		switch r {
		case ' ', '\n', '\r', '\t':
			return -1
		}
		return r
	}, text)
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		if dec, err := enc.DecodeString(cleaned); err == nil && len(dec) > 0 {
			s := string(dec)
			// Sanity check: the decoded body should look like one or
			// more share URIs. Otherwise we probably matched random
			// bytes and the input was actually plain text.
			if looksLikeShareBundle(s) {
				return s, true
			}
		}
	}
	return "", false
}

// tryBase64Raw is tryBase64 without the share-URI sanity check.
// Used by VMess where the decoded body is JSON, not URIs.
func tryBase64Raw(text string) (string, bool) {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\n', '\r', '\t':
			return -1
		}
		return r
	}, text)
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		if dec, err := enc.DecodeString(cleaned); err == nil && len(dec) > 0 {
			return string(dec), true
		}
	}
	return "", false
}

func looksLikeShareBundle(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		if looksLikeShareURI(strings.TrimSpace(line)) {
			return true
		}
	}
	return false
}

func looksLikeShareURI(s string) bool {
	for _, p := range []string{"vless://", "vmess://", "trojan://", "ss://"} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// ---- per-protocol parsers --------------------------------------------------

// parseVLESS handles
//   vless://uuid@host:port?security=reality&pbk=...&fp=chrome&sni=microsoft.com&flow=xtls-rprx-vision&type=tcp&sid=abcd&headerType=none#name
//
// The fragment is treated as the display name. URL-decoded.
func parseVLESS(raw string) (singbox.Outbound, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return singbox.Outbound{}, fmt.Errorf("%w: %v", ErrMalformedURI, err)
	}
	if u.User == nil || u.User.Username() == "" {
		return singbox.Outbound{}, fmt.Errorf("%w: vless URI missing UUID", ErrMalformedURI)
	}
	host, port, err := splitHostPort(u.Host)
	if err != nil {
		return singbox.Outbound{}, err
	}
	q := u.Query()
	o := singbox.Outbound{
		Type:        singbox.OutboundVLESS,
		DisplayName: decodeFrag(u.Fragment),
		Server:      host,
		Port:        port,
		UUID:        u.User.Username(),
	}
	applyTransport(&o, q)
	applyTLS(&o, q)
	return o, nil
}

// parseVMess handles two URI shapes:
//
//   1. v2rayN-style:  vmess://<base64-of-json>
//   2. RFC-style:     vmess://uuid@host:port?... (less common)
func parseVMess(raw string) (singbox.Outbound, error) {
	body := strings.TrimPrefix(raw, "vmess://")

	// Try v2rayN base64-JSON shape first. We can't use tryBase64
	// here — it gates on the decoded body looking like share URIs,
	// but here we want JSON.
	if decoded, ok := tryBase64Raw(body); ok {
		if t := strings.TrimSpace(decoded); strings.HasPrefix(t, "{") {
			return parseVMessJSON([]byte(t))
		}
	}

	// Fallback: treat as URL.
	u, err := url.Parse(raw)
	if err != nil {
		return singbox.Outbound{}, fmt.Errorf("%w: %v", ErrMalformedURI, err)
	}
	if u.User == nil || u.User.Username() == "" {
		return singbox.Outbound{}, fmt.Errorf("%w: vmess URI missing UUID", ErrMalformedURI)
	}
	host, port, err := splitHostPort(u.Host)
	if err != nil {
		return singbox.Outbound{}, err
	}
	q := u.Query()
	alter, _ := strconv.Atoi(q.Get("aid"))
	o := singbox.Outbound{
		Type:        singbox.OutboundVMess,
		DisplayName: decodeFrag(u.Fragment),
		Server:      host,
		Port:        port,
		UUID:        u.User.Username(),
		AlterID:     alter,
	}
	applyTransport(&o, q)
	applyTLS(&o, q)
	return o, nil
}

// vmessShareJSON is the v2rayN share-URI body shape. Most fields are
// strings (even when they hold integers) because v2rayN emits JSON
// straight from a JS object.
type vmessShareJSON struct {
	V    string `json:"v"`
	PS   string `json:"ps"`   // display name
	Add  string `json:"add"`  // server
	Port any    `json:"port"` // can be string or number
	ID   string `json:"id"`   // UUID
	Aid  any    `json:"aid"`  // alter id
	Net  string `json:"net"`  // transport
	Type string `json:"type"` // header type — usually "none"
	Host string `json:"host"` // ws/h2 Host header
	Path string `json:"path"` // ws/h2 path
	TLS  string `json:"tls"`  // "tls" or "" or "reality"
	SNI  string `json:"sni"`
	Alpn string `json:"alpn"`
	FP   string `json:"fp"`
}

func parseVMessJSON(body []byte) (singbox.Outbound, error) {
	var j vmessShareJSON
	if err := json.Unmarshal(body, &j); err != nil {
		return singbox.Outbound{}, fmt.Errorf("%w: vmess json: %v", ErrMalformedURI, err)
	}
	if j.ID == "" || j.Add == "" {
		return singbox.Outbound{}, fmt.Errorf("%w: vmess json missing id/add", ErrMalformedURI)
	}
	port := anyToInt(j.Port)
	alter := anyToInt(j.Aid)
	if port <= 0 {
		return singbox.Outbound{}, fmt.Errorf("%w: vmess json missing port", ErrMalformedURI)
	}
	o := singbox.Outbound{
		Type:        singbox.OutboundVMess,
		DisplayName: j.PS,
		Server:      j.Add,
		Port:        port,
		UUID:        j.ID,
		AlterID:     alter,
	}
	if j.Net != "" && j.Net != "tcp" {
		o.Transport = j.Net
		switch j.Net {
		case "ws":
			o.WSPath = j.Path
			o.WSHost = j.Host
		case "grpc":
			o.GRPCName = j.Path
		}
	}
	if j.TLS == "tls" || j.TLS == "reality" {
		o.TLS = &singbox.TLSOptions{
			Enabled:         true,
			SNI:             firstNonEmpty(j.SNI, j.Host, j.Add),
			UTLSFingerprint: j.FP,
		}
		if j.Alpn != "" {
			o.TLS.ALPN = strings.Split(j.Alpn, ",")
		}
	}
	return o, nil
}

// parseTrojan handles
//   trojan://password@host:port?sni=...&alpn=h2&type=tcp#name
func parseTrojan(raw string) (singbox.Outbound, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return singbox.Outbound{}, fmt.Errorf("%w: %v", ErrMalformedURI, err)
	}
	if u.User == nil {
		return singbox.Outbound{}, fmt.Errorf("%w: trojan URI missing password", ErrMalformedURI)
	}
	host, port, err := splitHostPort(u.Host)
	if err != nil {
		return singbox.Outbound{}, err
	}
	pw := u.User.Username()
	if pw == "" {
		return singbox.Outbound{}, fmt.Errorf("%w: trojan URI missing password", ErrMalformedURI)
	}
	q := u.Query()
	o := singbox.Outbound{
		Type:        singbox.OutboundTrojan,
		DisplayName: decodeFrag(u.Fragment),
		Server:      host,
		Port:        port,
		Password:    pw,
	}
	applyTransport(&o, q)
	// Trojan is always TLS.
	if o.TLS == nil {
		o.TLS = &singbox.TLSOptions{Enabled: true}
	}
	if sni := q.Get("sni"); sni != "" {
		o.TLS.SNI = sni
	} else if peer := q.Get("peer"); peer != "" {
		o.TLS.SNI = peer
	} else {
		o.TLS.SNI = host
	}
	if alpn := q.Get("alpn"); alpn != "" {
		o.TLS.ALPN = strings.Split(alpn, ",")
	}
	if fp := q.Get("fp"); fp != "" {
		o.TLS.UTLSFingerprint = fp
	}
	return o, nil
}

// parseShadowsocks handles two URI shapes:
//
//   1. SIP002:  ss://base64(method:password)@host:port#name
//   2. legacy:  ss://base64(method:password@host:port)#name
func parseShadowsocks(raw string) (singbox.Outbound, error) {
	body := strings.TrimPrefix(raw, "ss://")

	// Split off optional fragment.
	frag := ""
	if i := strings.LastIndex(body, "#"); i >= 0 {
		frag = body[i+1:]
		body = body[:i]
	}

	// SIP002 if there's an `@` outside any base64 part.
	if i := strings.LastIndex(body, "@"); i > 0 {
		userInfoB64 := body[:i]
		hostPort := body[i+1:]
		// Strip any trailing query.
		queryStr := ""
		if j := strings.IndexByte(hostPort, '?'); j >= 0 {
			queryStr = hostPort[j+1:]
			hostPort = hostPort[:j]
		}
		userInfo, err := decodeAnyBase64(userInfoB64)
		if err != nil {
			// Some SIP002 emitters URL-encode the userinfo instead.
			if dec, derr := url.QueryUnescape(userInfoB64); derr == nil {
				userInfo = dec
			} else {
				return singbox.Outbound{}, fmt.Errorf("%w: ss userinfo: %v", ErrMalformedURI, err)
			}
		}
		method, pw, ok := strings.Cut(userInfo, ":")
		if !ok || method == "" || pw == "" {
			return singbox.Outbound{}, fmt.Errorf("%w: ss method:password", ErrMalformedURI)
		}
		host, port, err := splitHostPort(hostPort)
		if err != nil {
			return singbox.Outbound{}, err
		}
		_ = queryStr // ss query params (e.g. plugin=) are ignored for v0.5.
		return singbox.Outbound{
			Type:        singbox.OutboundShadowsocks,
			DisplayName: decodeFrag(frag),
			Server:      host,
			Port:        port,
			Method:      method,
			Password:    pw,
		}, nil
	}

	// Legacy: full body is base64 of method:password@host:port.
	dec, err := decodeAnyBase64(body)
	if err != nil {
		return singbox.Outbound{}, fmt.Errorf("%w: ss legacy: %v", ErrMalformedURI, err)
	}
	atIdx := strings.LastIndex(dec, "@")
	if atIdx <= 0 {
		return singbox.Outbound{}, fmt.Errorf("%w: ss legacy malformed", ErrMalformedURI)
	}
	method, pw, ok := strings.Cut(dec[:atIdx], ":")
	if !ok {
		return singbox.Outbound{}, fmt.Errorf("%w: ss legacy method:password", ErrMalformedURI)
	}
	host, port, err := splitHostPort(dec[atIdx+1:])
	if err != nil {
		return singbox.Outbound{}, err
	}
	return singbox.Outbound{
		Type:        singbox.OutboundShadowsocks,
		DisplayName: decodeFrag(frag),
		Server:      host,
		Port:        port,
		Method:      method,
		Password:    pw,
	}, nil
}

// ---- helpers --------------------------------------------------------------

// applyTransport reads `type`/`path`/`host`/`serviceName` from the
// query string and populates the matching Outbound fields.
func applyTransport(o *singbox.Outbound, q url.Values) {
	t := q.Get("type")
	if t == "" || t == "tcp" {
		return
	}
	o.Transport = t
	switch t {
	case "ws":
		o.WSPath = q.Get("path")
		o.WSHost = firstNonEmpty(q.Get("host"), q.Get("Host"))
	case "grpc":
		o.GRPCName = firstNonEmpty(q.Get("serviceName"), q.Get("path"))
	case "http":
		o.HTTPPath = q.Get("path")
	case "xhttp", "splithttp":
		// xhttp is Xray-only; normalize the older "splithttp" alias to
		// the current name so the engine selector sees one transport.
		o.Transport = "xhttp"
		o.XHTTPPath = q.Get("path")
		o.XHTTPHost = firstNonEmpty(q.Get("host"), q.Get("Host"))
		o.XHTTPMode = q.Get("mode")
	}
}

// applyTLS reads `security`, `sni`, `pbk`, `fp`, `flow`, `sid`, `alpn`
// from the query string and builds the TLSOptions.
func applyTLS(o *singbox.Outbound, q url.Values) {
	sec := q.Get("security")
	if sec == "" || sec == "none" {
		return
	}
	o.TLS = &singbox.TLSOptions{Enabled: true}
	if sni := q.Get("sni"); sni != "" {
		o.TLS.SNI = sni
	}
	if alpn := q.Get("alpn"); alpn != "" {
		o.TLS.ALPN = strings.Split(alpn, ",")
	}
	if fp := q.Get("fp"); fp != "" {
		o.TLS.UTLSFingerprint = fp
	}
	if flow := q.Get("flow"); flow != "" {
		o.TLS.VLESSFlow = flow
	}
	if sec == "reality" {
		o.TLS.REALITY = &singbox.REALITYOptions{
			Enabled:   true,
			PublicKey: q.Get("pbk"),
			ShortID:   q.Get("sid"),
		}
	}
}

func splitHostPort(hp string) (string, int, error) {
	host, portStr, ok := strings.Cut(hp, ":")
	if !ok || host == "" || portStr == "" {
		return "", 0, fmt.Errorf("%w: bad host:port %q", ErrMalformedURI, hp)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("%w: bad port %q", ErrMalformedURI, portStr)
	}
	return host, port, nil
}

func decodeFrag(s string) string {
	if dec, err := url.QueryUnescape(s); err == nil {
		return dec
	}
	return s
}

func decodeAnyBase64(s string) (string, error) {
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		if dec, err := enc.DecodeString(s); err == nil {
			return string(dec), nil
		}
	}
	return "", errors.New("not valid base64")
}

func firstNonEmpty(strs ...string) string {
	for _, s := range strs {
		if s != "" {
			return s
		}
	}
	return ""
}

func anyToInt(v any) int {
	switch x := v.(type) {
	case nil:
		return 0
	case float64:
		return int(x)
	case int:
		return x
	case string:
		n, _ := strconv.Atoi(x)
		return n
	}
	return 0
}
