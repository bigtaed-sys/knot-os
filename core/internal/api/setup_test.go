package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/knot-os/knot-os/core/internal/auth"
	"github.com/knot-os/knot-os/core/internal/config"
	"github.com/knot-os/knot-os/core/internal/network"
)

func TestSetupScanReturnsNetworks(t *testing.T) {
	srv := newTestServer(t, false)

	req := httptest.NewRequest(http.MethodGet, "/setup/scan", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body=%s)", rec.Code, rec.Body)
	}
	var body struct {
		Networks []network.ScannedNetwork `json:"networks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Networks) == 0 {
		t.Fatal("expected at least one scanned network")
	}
}

func TestSetupScanGoneAfterCompletion(t *testing.T) {
	// Bootstrap a server already past setup.
	hash, _ := auth.HashPassword(testPassword)
	cfg := config.Default()
	cfg.Role = config.RoleWiFiExtender
	cfg.Auth.PasswordHash = hash
	cfg.Network.Uplink = &config.WiFiUplink{SSID: "u"}
	cfg.Network.AP = &config.WiFiAP{SSID: "a", Band: "2.4"}

	srv := New(Options{
		ConfigPath: filepath.Join(t.TempDir(), "config.yaml"),
		Initial:    cfg,
		Version:    "test",
		Backend:    network.NewMock(),
	})

	req := httptest.NewRequest(http.MethodGet, "/setup/scan", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusGone {
		t.Errorf("scan after setup: want 410, got %d", rec.Code)
	}
}

func TestSetupCompleteHappyPath(t *testing.T) {
	srv := newTestServer(t, false)

	body := completeRequest{}
	body.Device.Name = "knot-test"
	body.Device.Country = "RU"
	body.Password = "good-password-9"
	body.Uplink.SSID = "HomeWiFi"
	body.Uplink.PSK = "uplink-secret"
	body.AP.SSID = "KnotNet"
	body.AP.PSK = "ap-secret"
	body.AP.Band = "2.4"

	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/setup/complete", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body=%s)", rec.Code, rec.Body)
	}

	// Server state moved to wifi-extender, password is set, session issued.
	if got := srv.Snapshot().Role; got != config.RoleWiFiExtender {
		t.Errorf("role: want %q, got %q", config.RoleWiFiExtender, got)
	}
	if srv.Snapshot().Auth.PasswordHash == "" {
		t.Error("password hash not set after setup")
	}

	cookieFound := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.CookieName && c.Value != "" {
			cookieFound = true
		}
	}
	if !cookieFound {
		t.Error("session cookie not set after setup completion")
	}

	// And /api/setup/scan now returns 410.
	req2 := httptest.NewRequest(http.MethodGet, "/setup/scan", nil)
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusGone {
		t.Errorf("scan after complete: want 410, got %d", rec2.Code)
	}
}

func TestSetupCompleteRejectsWeakPassword(t *testing.T) {
	srv := newTestServer(t, false)

	body := completeRequest{}
	body.Device.Name = "knot"
	body.Device.Country = "RU"
	body.Password = "short"
	body.Uplink.SSID = "x"
	body.AP.SSID = "y"
	body.AP.Band = "2.4"

	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/setup/complete", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status: want 422, got %d (body=%s)", rec.Code, rec.Body)
	}
	if srv.Snapshot().Role != config.RoleSetup {
		t.Error("role should remain setup after weak password rejection")
	}
}
