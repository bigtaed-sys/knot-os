package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knot-os/knot-os/core/internal/auth"
	"github.com/knot-os/knot-os/core/internal/config"
	"github.com/knot-os/knot-os/core/internal/network"
)

// routerModemServer boots an authenticated server in the wifi-router
// role whose WAN is a cellular modem — the exact starting state where
// the old ports page's footgun bit (assigning an Ethernet port silently
// dropped the modem).
func routerModemServer(t *testing.T) (*Server, *http.Cookie) {
	t.Helper()
	hash, err := auth.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	cfg := config.Default()
	cfg.Role = config.RoleWiFiRouter
	cfg.Auth.PasswordHash = hash
	cfg.Network.WAN = &config.WAN{Mode: "modem", Modem: &config.Modem{APN: "internet"}}
	cfg.Network.AP = &config.WiFiAP{SSID: "KnotNet", Band: "2.4"}
	srv := New(Options{
		ConfigPath: filepath.Join(t.TempDir(), "config.yaml"),
		Initial:    cfg,
		Version:    "test",
		Backend:    network.NewMock(),
	})
	return srv, login(t, srv, testPassword)
}

func putNetwork(t *testing.T, srv *Server, cookie *http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/network", strings.NewReader(body))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// TestPutNetworkLANPortsKeepsModemWAN is the regression for the footgun:
// editing LAN ports must NOT touch the WAN. A modem WAN stays a modem WAN.
func TestPutNetworkLANPortsKeepsModemWAN(t *testing.T) {
	srv, cookie := routerModemServer(t)

	rec := putNetwork(t, srv, cookie, `{"lan_ports":["eth1"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("put: want 200, got %d (%s)", rec.Code, rec.Body)
	}
	got := srv.Snapshot()
	if got.Network.WAN == nil || got.Network.WAN.Mode != "modem" {
		t.Fatalf("modem WAN must be preserved when only lan_ports change, got %+v", got.Network.WAN)
	}
	if got.Network.WAN.Modem == nil || got.Network.WAN.Modem.APN != "internet" {
		t.Errorf("modem settings must be preserved, got %+v", got.Network.WAN.Modem)
	}
	if len(got.Network.LANPorts) != 1 || got.Network.LANPorts[0] != "eth1" {
		t.Errorf("lan_ports not applied: %+v", got.Network.LANPorts)
	}
}

// TestPutNetworkExplicitEthernetSwitch verifies that switching the WAN to
// Ethernet only happens when the client explicitly says so — and then the
// modem block is cleared.
func TestPutNetworkExplicitEthernetSwitch(t *testing.T) {
	srv, cookie := routerModemServer(t)

	rec := putNetwork(t, srv, cookie, `{"wan":{"mode":"dhcp","interface":"eth0"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("put: want 200, got %d (%s)", rec.Code, rec.Body)
	}
	got := srv.Snapshot()
	if got.Network.WAN.Mode != "dhcp" || got.Network.WAN.Interface != "eth0" {
		t.Fatalf("explicit ethernet WAN not applied: %+v", got.Network.WAN)
	}
	if got.Network.WAN.Modem != nil {
		t.Errorf("modem block must be cleared on explicit ethernet switch, got %+v", got.Network.WAN.Modem)
	}
}

// TestPutNetworkRoleSwitchToExtender checks a full role change: router →
// extender clears WAN/lan_ports and installs the uplink.
func TestPutNetworkRoleSwitchToExtender(t *testing.T) {
	srv, cookie := routerModemServer(t)

	body := `{"role":"wifi-extender","uplink":{"ssid":"HomeWiFi","psk":"secret12"},"ap":{"ssid":"KnotNet","psk":"ap-secret","band":"2.4"}}`
	rec := putNetwork(t, srv, cookie, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("put: want 200, got %d (%s)", rec.Code, rec.Body)
	}
	got := srv.Snapshot()
	if got.Role != config.RoleWiFiExtender {
		t.Fatalf("role not switched: %q", got.Role)
	}
	if got.Network.WAN != nil || len(got.Network.LANPorts) != 0 {
		t.Errorf("extender must have no WAN/lan_ports, got wan=%+v ports=%+v", got.Network.WAN, got.Network.LANPorts)
	}
	if got.Network.Uplink == nil || got.Network.Uplink.SSID != "HomeWiFi" {
		t.Errorf("uplink not set: %+v", got.Network.Uplink)
	}
}
