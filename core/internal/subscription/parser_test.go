package subscription

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/knot-os/knot-os/core/internal/singbox"
)

func TestParseVLESSREALITY(t *testing.T) {
	uri := "vless://12345678-1234-1234-1234-123456789012@tokyo.example.com:443" +
		"?security=reality&pbk=PUBKEY123&fp=chrome&sni=www.microsoft.com" +
		"&flow=xtls-rprx-vision&type=tcp&sid=abcd1234&headerType=none#Tokyo%20%F0%9F%87%AF%F0%9F%87%B5"

	o, err := ParseURI(uri)
	if err != nil {
		t.Fatalf("ParseURI: %v", err)
	}
	if o.Type != singbox.OutboundVLESS {
		t.Errorf("Type=%q, want vless", o.Type)
	}
	if o.UUID != "12345678-1234-1234-1234-123456789012" {
		t.Errorf("UUID mismatch: %s", o.UUID)
	}
	if o.Server != "tokyo.example.com" || o.Port != 443 {
		t.Errorf("server/port mismatch: %s:%d", o.Server, o.Port)
	}
	if o.TLS == nil || !o.TLS.Enabled {
		t.Fatal("TLS not enabled")
	}
	if o.TLS.SNI != "www.microsoft.com" {
		t.Errorf("SNI mismatch: %s", o.TLS.SNI)
	}
	if o.TLS.UTLSFingerprint != "chrome" {
		t.Errorf("fp mismatch: %s", o.TLS.UTLSFingerprint)
	}
	if o.TLS.VLESSFlow != "xtls-rprx-vision" {
		t.Errorf("flow mismatch: %s", o.TLS.VLESSFlow)
	}
	if o.TLS.REALITY == nil || o.TLS.REALITY.PublicKey != "PUBKEY123" {
		t.Errorf("REALITY pbk mismatch: %+v", o.TLS.REALITY)
	}
	if o.TLS.REALITY.ShortID != "abcd1234" {
		t.Errorf("REALITY sid mismatch: %s", o.TLS.REALITY.ShortID)
	}
	if !strings.HasPrefix(o.DisplayName, "Tokyo") {
		t.Errorf("name not decoded: %q", o.DisplayName)
	}
}

func TestParseVLESSWS(t *testing.T) {
	uri := "vless://uuid-1@example.com:443" +
		"?security=tls&type=ws&path=%2Fws&host=cdn.example.com&sni=cdn.example.com#srv"
	o, err := ParseURI(uri)
	if err != nil {
		t.Fatal(err)
	}
	if o.Transport != "ws" {
		t.Errorf("Transport=%q, want ws", o.Transport)
	}
	if o.WSPath != "/ws" {
		t.Errorf("WSPath=%q", o.WSPath)
	}
	if o.WSHost != "cdn.example.com" {
		t.Errorf("WSHost=%q", o.WSHost)
	}
}

func TestParseVMessV2rayN(t *testing.T) {
	jsonBody := `{"v":"2","ps":"Frankfurt","add":"fra.example.com","port":"443",` +
		`"id":"abcdef01-2345-6789-abcd-ef0123456789","aid":"0","net":"ws",` +
		`"path":"/api","host":"www.example.com","tls":"tls","sni":"www.example.com","fp":"firefox"}`
	enc := base64.StdEncoding.EncodeToString([]byte(jsonBody))

	o, err := ParseURI("vmess://" + enc)
	if err != nil {
		t.Fatal(err)
	}
	if o.Type != singbox.OutboundVMess {
		t.Errorf("Type=%q", o.Type)
	}
	if o.Server != "fra.example.com" || o.Port != 443 {
		t.Errorf("server/port: %s:%d", o.Server, o.Port)
	}
	if o.UUID != "abcdef01-2345-6789-abcd-ef0123456789" {
		t.Errorf("UUID: %s", o.UUID)
	}
	if o.Transport != "ws" || o.WSPath != "/api" || o.WSHost != "www.example.com" {
		t.Errorf("ws fields: %+v", o)
	}
	if o.TLS == nil || o.TLS.SNI != "www.example.com" {
		t.Errorf("TLS not parsed: %+v", o.TLS)
	}
	if o.TLS.UTLSFingerprint != "firefox" {
		t.Errorf("fp: %q", o.TLS.UTLSFingerprint)
	}
	if o.DisplayName != "Frankfurt" {
		t.Errorf("name: %q", o.DisplayName)
	}
}

func TestParseTrojan(t *testing.T) {
	o, err := ParseURI("trojan://secret-pw@trojan.example.com:443?sni=trojan.example.com&alpn=h2,http%2F1.1#Trojan")
	if err != nil {
		t.Fatal(err)
	}
	if o.Type != singbox.OutboundTrojan {
		t.Errorf("Type=%q", o.Type)
	}
	if o.Password != "secret-pw" {
		t.Errorf("pw=%q", o.Password)
	}
	if o.Server != "trojan.example.com" || o.Port != 443 {
		t.Errorf("server/port: %s:%d", o.Server, o.Port)
	}
	if o.TLS == nil || !o.TLS.Enabled {
		t.Fatal("trojan TLS not enabled")
	}
	if o.TLS.SNI != "trojan.example.com" {
		t.Errorf("SNI: %q", o.TLS.SNI)
	}
	if len(o.TLS.ALPN) != 2 || o.TLS.ALPN[0] != "h2" {
		t.Errorf("ALPN: %v", o.TLS.ALPN)
	}
}

func TestParseShadowsocksSIP002(t *testing.T) {
	userInfo := base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:hunter2"))
	uri := "ss://" + userInfo + "@ss.example.com:8388#SS-Tokyo"
	o, err := ParseURI(uri)
	if err != nil {
		t.Fatal(err)
	}
	if o.Method != "aes-256-gcm" {
		t.Errorf("method: %q", o.Method)
	}
	if o.Password != "hunter2" {
		t.Errorf("password: %q", o.Password)
	}
	if o.Server != "ss.example.com" || o.Port != 8388 {
		t.Errorf("server/port: %s:%d", o.Server, o.Port)
	}
	if o.DisplayName != "SS-Tokyo" {
		t.Errorf("name: %q", o.DisplayName)
	}
}

func TestParseShadowsocksLegacy(t *testing.T) {
	body := base64.StdEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:pw@host.example.com:9999"))
	o, err := ParseURI("ss://" + body + "#name")
	if err != nil {
		t.Fatal(err)
	}
	if o.Method != "chacha20-ietf-poly1305" || o.Password != "pw" {
		t.Errorf("auth: %+v", o)
	}
	if o.Server != "host.example.com" || o.Port != 9999 {
		t.Errorf("server: %s:%d", o.Server, o.Port)
	}
}

func TestParseURIRejectsUnknownScheme(t *testing.T) {
	if _, err := ParseURI("ssr://garbage"); err == nil {
		t.Error("expected error for unknown scheme")
	}
	if _, err := ParseURI(""); err == nil {
		t.Error("expected error for empty input")
	}
}

func TestParseURIRejectsBadPort(t *testing.T) {
	if _, err := ParseURI("vless://uuid@host:9999999"); err == nil {
		t.Error("expected error for OOB port")
	}
}

func TestParseBundleBase64(t *testing.T) {
	uris := []string{
		"vless://aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa@a.example.com:443?security=reality&pbk=K1&sni=x.com#A",
		"vless://bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb@b.example.com:443?security=reality&pbk=K2&sni=x.com#B",
		"trojan://pw@c.example.com:443?sni=c.example.com#C",
	}
	body := strings.Join(uris, "\n")
	enc := base64.StdEncoding.EncodeToString([]byte(body))

	outs, errs := ParseBundle([]byte(enc))
	if len(errs) > 0 {
		t.Fatalf("ParseBundle errors: %v", errs)
	}
	if len(outs) != 3 {
		t.Fatalf("got %d outbounds, want 3", len(outs))
	}
	if outs[0].DisplayName != "A" || outs[2].DisplayName != "C" {
		t.Errorf("ordering broken: %+v", outs)
	}
}

func TestParseBundlePlainText(t *testing.T) {
	body := "# v0.5 subscription banner\nvless://uuid@h:443?security=reality&pbk=k&sni=x.com#one\n\n"
	outs, errs := ParseBundle([]byte(body))
	if len(errs) > 0 {
		t.Fatalf("ParseBundle errors: %v", errs)
	}
	if len(outs) != 1 {
		t.Fatalf("got %d outbounds, want 1", len(outs))
	}
}

func TestParseBundlePartialFailure(t *testing.T) {
	body := "vless://uuid@h:443?security=reality&pbk=k&sni=x.com#good\n" +
		"vless://broken-uri-no-host\n"
	outs, errs := ParseBundle([]byte(body))
	if len(outs) != 1 {
		t.Errorf("good URIs: %d, want 1", len(outs))
	}
	if len(errs) != 1 {
		t.Errorf("errors: %d, want 1", len(errs))
	}
}

func TestParseBundleEmpty(t *testing.T) {
	if _, errs := ParseBundle([]byte("")); len(errs) == 0 {
		t.Error("expected error on empty bundle")
	}
	if _, errs := ParseBundle([]byte("just a banner, no URIs")); len(errs) == 0 {
		t.Error("expected error on no-URI body")
	}
}

// Sanity: parsed outbound should round-trip through singbox.Config.
func TestParsedVLESSRendersClean(t *testing.T) {
	o, err := ParseURI("vless://12345678-1234-1234-1234-123456789012@h:443" +
		"?security=reality&pbk=k&sni=x.com&flow=xtls-rprx-vision&type=tcp#A")
	if err != nil {
		t.Fatal(err)
	}
	o.Tag = "sub:0"
	cfg := singbox.Config{Outbounds: []singbox.Outbound{o}}
	js, err := cfg.RenderJSON()
	if err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	for _, want := range []string{`"reality"`, `"public_key": "k"`, `"flow": "xtls-rprx-vision"`} {
		if !strings.Contains(string(js), want) {
			t.Errorf("rendered config missing %q\n%s", want, js)
		}
	}
}
