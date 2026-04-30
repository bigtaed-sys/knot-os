package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knot-os/knot-os/core/internal/auth"
	"github.com/knot-os/knot-os/core/internal/config"
	"github.com/knot-os/knot-os/core/internal/network"
)

const testPassword = "test-password-1"

func newTestServer(t *testing.T, withAuth bool) *Server {
	t.Helper()
	cfg := config.Default()
	if withAuth {
		hash, err := auth.HashPassword(testPassword)
		if err != nil {
			t.Fatalf("hash test password: %v", err)
		}
		cfg.Auth.PasswordHash = hash
	}
	return New(Options{
		ConfigPath: filepath.Join(t.TempDir(), "config.yaml"),
		Initial:    cfg,
		Version:    "test",
		Backend:    network.NewMock(),
	})
}

func login(t *testing.T, srv *Server, password string) *http.Cookie {
	t.Helper()
	body, _ := json.Marshal(loginRequest{Password: password})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: want 200, got %d (body=%s)", rec.Code, rec.Body)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.CookieName {
			return c
		}
	}
	t.Fatal("session cookie not set after login")
	return nil
}

func TestStatusEndpointIsPublic(t *testing.T) {
	srv := newTestServer(t, false)

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code: want 200, got %d (body=%s)", rec.Code, rec.Body)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["auth_configured"] != false {
		t.Errorf("auth_configured: want false, got %v", body["auth_configured"])
	}
}

func TestConfigRequiresAuth(t *testing.T) {
	srv := newTestServer(t, true)

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: want 401, got %d (body=%s)", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "unauthorized") {
		t.Errorf("body should contain unauthorized: %s", rec.Body)
	}
}

func TestLoginThenGetConfig(t *testing.T) {
	srv := newTestServer(t, true)
	cookie := login(t, srv, testPassword)

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	req.AddCookie(cookie)
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

func TestLoginRejectsWrongPassword(t *testing.T) {
	srv := newTestServer(t, true)

	body, _ := json.Marshal(loginRequest{Password: "wrong"})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: want 401, got %d", rec.Code)
	}
}

func TestLoginBeforeSetupReturns409(t *testing.T) {
	srv := newTestServer(t, false)

	body, _ := json.Marshal(loginRequest{Password: "anything"})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status: want 409, got %d (body=%s)", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "not_configured") {
		t.Errorf("body should contain not_configured: %s", rec.Body)
	}
}

func TestLogoutRevokesSession(t *testing.T) {
	srv := newTestServer(t, true)
	cookie := login(t, srv, testPassword)

	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("logout: want 200, got %d", rec.Code)
	}

	// The session token should no longer work.
	req2 := httptest.NewRequest(http.MethodGet, "/config", nil)
	req2.AddCookie(cookie)
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Errorf("after logout: want 401, got %d", rec2.Code)
	}
}

func TestPutConfigPreservesAuthHash(t *testing.T) {
	srv := newTestServer(t, true)
	originalHash := srv.cfg.Auth.PasswordHash
	cookie := login(t, srv, testPassword)

	updated := config.Default()
	updated.Device.Name = "knot-renamed"
	body, _ := json.Marshal(updated)
	req := httptest.NewRequest(http.MethodPut, "/config", bytes.NewReader(body))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT: want 200, got %d (body=%s)", rec.Code, rec.Body)
	}
	if got := srv.Snapshot().Auth.PasswordHash; got != originalHash {
		t.Errorf("password hash was modified by PUT /config")
	}
}

func TestPutConfigInvokesBackendApply(t *testing.T) {
	mock := network.NewMock()
	hash, _ := auth.HashPassword(testPassword)
	cfg := config.Default()
	cfg.Auth.PasswordHash = hash
	srv := New(Options{
		ConfigPath: filepath.Join(t.TempDir(), "config.yaml"),
		Initial:    cfg,
		Version:    "test",
		Backend:    mock,
	})
	cookie := login(t, srv, testPassword)

	updated := config.Default()
	updated.Device.Name = "knot-applied"
	body, _ := json.Marshal(updated)
	req := httptest.NewRequest(http.MethodPut, "/config", bytes.NewReader(body))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body=%s)", rec.Code, rec.Body)
	}
	if mock.Applies != 1 {
		t.Fatalf("expected backend Apply once, got %d", mock.Applies)
	}
}

func TestUnknownEndpointReturns404(t *testing.T) {
	srv := newTestServer(t, false)

	req := httptest.NewRequest(http.MethodGet, "/no-such-thing", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: want 404, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "not_found") {
		t.Errorf("body should contain not_found: %s", rec.Body)
	}
}
