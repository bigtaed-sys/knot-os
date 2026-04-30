package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knot-os/knot-os/core/internal/config"
	"github.com/knot-os/knot-os/core/internal/network"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	return New(Options{
		ConfigPath: filepath.Join(t.TempDir(), "config.yaml"),
		Initial:    config.Default(),
		Version:    "test",
		Backend:    network.NewMock(),
	})
}

func TestStatusEndpoint(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code: want 200, got %d (body=%s)", rec.Code, rec.Body)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["version"] != "test" {
		t.Errorf("version: want \"test\", got %v", body["version"])
	}
	if body["role"] != "setup" {
		t.Errorf("role: want \"setup\", got %v", body["role"])
	}
}

func TestGetConfigReturnsCurrent(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rec.Code)
	}
	var got config.Config
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Role != config.RoleSetup {
		t.Errorf("role: want %q, got %q", config.RoleSetup, got.Role)
	}
}

func TestPutConfigValidatesAndPersists(t *testing.T) {
	srv := newTestServer(t)

	updated := config.Default()
	updated.Device.Name = "knot-test-host"
	updated.Device.Country = "RU"

	body, _ := json.Marshal(updated)
	req := httptest.NewRequest(http.MethodPut, "/config", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body=%s)", rec.Code, rec.Body)
	}
	if got := srv.Snapshot().Device.Name; got != "knot-test-host" {
		t.Errorf("Snapshot Name: want knot-test-host, got %q", got)
	}

	// Verify on-disk persistence.
	persisted, err := config.Load(srv.configPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if persisted.Device.Country != "RU" {
		t.Errorf("persisted Country: want RU, got %q", persisted.Device.Country)
	}
}

func TestPutConfigRejectsInvalid(t *testing.T) {
	srv := newTestServer(t)

	bad := config.Default()
	bad.Device.Name = "" // invalid hostname

	body, _ := json.Marshal(bad)
	req := httptest.NewRequest(http.MethodPut, "/config", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status: want 422, got %d (body=%s)", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "invalid_config") {
		t.Errorf("body should contain invalid_config code: %s", rec.Body)
	}
}

func TestPutConfigInvokesBackendApply(t *testing.T) {
	mock := network.NewMock()
	srv := New(Options{
		ConfigPath: filepath.Join(t.TempDir(), "config.yaml"),
		Initial:    config.Default(),
		Version:    "test",
		Backend:    mock,
	})

	updated := config.Default()
	updated.Device.Name = "knot-applied"
	body, _ := json.Marshal(updated)
	req := httptest.NewRequest(http.MethodPut, "/config", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body=%s)", rec.Code, rec.Body)
	}
	if mock.Applies != 1 {
		t.Fatalf("expected backend Apply to be called once, got %d", mock.Applies)
	}
	last, ok := mock.Last()
	if !ok || last.Device.Name != "knot-applied" {
		t.Fatalf("backend last config mismatch: ok=%v cfg=%+v", ok, last)
	}
}

func TestStatusIncludesNetwork(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	net, ok := body["network"].(map[string]any)
	if !ok {
		t.Fatalf("network field missing or wrong type: %#v", body["network"])
	}
	if net["backend"] != "mock" {
		t.Errorf("backend: want \"mock\", got %v", net["backend"])
	}
}

func TestUnknownEndpointReturns404(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/no-such-thing", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: want 404, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "not_found") {
		t.Errorf("body should contain not_found code: %s", rec.Body)
	}
}
