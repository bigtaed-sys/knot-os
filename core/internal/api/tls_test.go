package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/knot-os/knot-os/core/internal/auth"
	"github.com/knot-os/knot-os/core/internal/config"
	"github.com/knot-os/knot-os/core/internal/network"
	knottls "github.com/knot-os/knot-os/core/internal/tls"
)

func newTLSTestServer(t *testing.T) (*Server, *knottls.Materials) {
	t.Helper()
	hash, _ := auth.HashPassword(testPassword)
	cfg := config.Default()
	cfg.Auth.PasswordHash = hash
	srv := New(Options{
		ConfigPath: filepath.Join(t.TempDir(), "config.yaml"),
		Initial:    cfg,
		Version:    "test",
		Backend:    network.NewMock(),
	})
	m, err := knottls.Open(knottls.Options{
		Dir:     t.TempDir(),
		Subject: knottls.BuildLeafSubject("knot", nil, nil),
	})
	if err != nil {
		t.Fatalf("tls.Open: %v", err)
	}
	srv.SetTLSMaterials(m, func() knottls.LeafSubject {
		return knottls.BuildLeafSubject("knot", nil, nil)
	})
	return srv, m
}

func TestTLSInfoEndpoint(t *testing.T) {
	srv, _ := newTLSTestServer(t)
	cookie := login(t, srv, testPassword)

	req := httptest.NewRequest(http.MethodGet, "/tls/info", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var got knottls.Info
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.RootFingerprint == "" || got.LeafFingerprint == "" {
		t.Errorf("missing fingerprints: %+v", got)
	}
}

func TestTLSRegenerateRotatesLeafKeepsRoot(t *testing.T) {
	srv, _ := newTLSTestServer(t)
	cookie := login(t, srv, testPassword)

	get := func() knottls.Info {
		req := httptest.NewRequest(http.MethodGet, "/tls/info", nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		var i knottls.Info
		if err := json.Unmarshal(rec.Body.Bytes(), &i); err != nil {
			t.Fatal(err)
		}
		return i
	}

	before := get()

	regen := httptest.NewRequest(http.MethodPost, "/tls/regenerate", nil)
	regen.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, regen)
	if rec.Code != http.StatusOK {
		t.Fatalf("regenerate status=%d body=%s", rec.Code, rec.Body)
	}

	after := get()
	if before.RootFingerprint != after.RootFingerprint {
		t.Error("root rotated on regenerate (must not)")
	}
	if before.LeafFingerprint == after.LeafFingerprint {
		t.Error("leaf did not rotate on regenerate")
	}
}

func TestTLSEndpointsRequireAuth(t *testing.T) {
	srv, _ := newTLSTestServer(t)
	for _, p := range []struct {
		method, path string
	}{
		{http.MethodGet, "/tls/info"},
		{http.MethodPost, "/tls/regenerate"},
	} {
		req := httptest.NewRequest(p.method, p.path, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: want 401, got %d", p.method, p.path, rec.Code)
		}
	}
}

func TestTLSDisabledWhenUnconfigured(t *testing.T) {
	hash, _ := auth.HashPassword(testPassword)
	cfg := config.Default()
	cfg.Auth.PasswordHash = hash
	srv := New(Options{
		ConfigPath: filepath.Join(t.TempDir(), "config.yaml"),
		Initial:    cfg,
		Version:    "test",
		Backend:    network.NewMock(),
	})
	cookie := login(t, srv, testPassword)
	req := httptest.NewRequest(http.MethodGet, "/tls/info", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("unconfigured: want 503, got %d", rec.Code)
	}
}
