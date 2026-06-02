package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/knot-os/knot-os/core/internal/plugin"
)

// fakeRuntime is a stand-in for *plugin.Supervisor in tests.
type fakeRuntime struct {
	tokenID string                  // token → this id
	status  map[string]plugin.ProcStatus
}

func (f fakeRuntime) PluginForToken(tok string) (string, bool) {
	if tok == "tok" {
		return f.tokenID, true
	}
	return "", false
}
func (f fakeRuntime) Status(id string) (plugin.ProcStatus, bool) {
	st, ok := f.status[id]
	return st, ok
}

// pluginServerWith installs a plugin registry from a temp dir holding
// one manifest with the given permissions, enabled.
func serverWithPlugin(t *testing.T, perms []string) *Server {
	t.Helper()
	srv := newTestServer(t, false)
	dir := t.TempDir()
	pdir := filepath.Join(dir, "p1")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "id: p1\nname: P1\nversion: 1.0.0\n"
	if len(perms) > 0 {
		manifest += "permissions:\n"
		for _, p := range perms {
			manifest += "  - " + p + "\n"
		}
	}
	if err := os.WriteFile(filepath.Join(pdir, "plugin.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := plugin.NewRegistry(dir)
	if err := reg.Discover(); err != nil {
		t.Fatalf("discover: %v", err)
	}
	reg.SetEnabled("p1", true)
	srv.SetPluginRegistry(reg)
	return srv
}

func TestHostAPIPermissionGate(t *testing.T) {
	srv := serverWithPlugin(t, []string{"devices:read"})
	srv.pluginSup = fakeRuntime{tokenID: "p1"}
	h := srv.HostAPIHandler()

	// Granted: devices:read → 200.
	req := httptest.NewRequest(http.MethodGet, "/host/v1/devices", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("devices: want 200, got %d (%s)", rec.Code, rec.Body)
	}

	// Not granted: status:read → 403.
	req = httptest.NewRequest(http.MethodGet, "/host/v1/status", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status without permission: want 403, got %d", rec.Code)
	}

	// Bad token → 401.
	req = httptest.NewRequest(http.MethodGet, "/host/v1/devices", nil)
	req.Header.Set("Authorization", "Bearer nope")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("bad token: want 401, got %d", rec.Code)
	}
}

func TestHostAPIWhoamiNoPermNeeded(t *testing.T) {
	srv := serverWithPlugin(t, nil)
	srv.pluginSup = fakeRuntime{tokenID: "p1"}
	req := httptest.NewRequest(http.MethodGet, "/host/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	srv.HostAPIHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("whoami: want 200, got %d (%s)", rec.Code, rec.Body)
	}
	var body struct{ PluginID string `json:"plugin_id"` }
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.PluginID != "p1" {
		t.Errorf("whoami plugin_id = %q, want p1", body.PluginID)
	}
}

func TestPluginProxyForwardsToSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		// AF_UNIX exists on modern Windows but is flaky in CI sandboxes;
		// the proxy logic is platform-independent, so skip the socket
		// dance here and rely on Linux CI for the wire test.
		t.Skip("unix socket proxy test runs on non-Windows")
	}
	srv := newTestServer(t, false)

	sock := filepath.Join(t.TempDir(), "p.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer ln.Close()
	go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("plugin saw " + r.URL.Path))
	}))

	srv.pluginSup = fakeRuntime{status: map[string]plugin.ProcStatus{
		"p1": {State: plugin.StateRunning, Socket: sock},
	}}

	req := httptest.NewRequest(http.MethodGet, "/api/plugins/p1/proxy/hello", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "p1")
	rctx.URLParams.Add("*", "hello")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	srv.handlePluginProxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("proxy: want 200, got %d (%s)", rec.Code, rec.Body)
	}
	if got := rec.Body.String(); got != "plugin saw /hello" {
		t.Errorf("proxy body = %q, want %q", got, "plugin saw /hello")
	}
}

func TestPluginProxyNotRunning(t *testing.T) {
	srv := newTestServer(t, false)
	srv.pluginSup = fakeRuntime{status: map[string]plugin.ProcStatus{}}

	req := httptest.NewRequest(http.MethodGet, "/api/plugins/p1/proxy/x", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "p1")
	rctx.URLParams.Add("*", "x")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	srv.handlePluginProxy(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("not-running proxy: want 502, got %d", rec.Code)
	}
}
