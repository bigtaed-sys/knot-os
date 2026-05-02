package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/knot-os/knot-os/core/internal/auth"
	"github.com/knot-os/knot-os/core/internal/config"
	knotdns "github.com/knot-os/knot-os/core/internal/dns"
	"github.com/knot-os/knot-os/core/internal/network"
)

func newDNSTestServer(t *testing.T) (*Server, *knotdns.RingLog, *knotdns.Registry) {
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
	ring := knotdns.NewRingLog(64)
	reg := knotdns.NewRegistry()
	srv.SetDNSServices(ring, reg, nil)
	return srv, ring, reg
}

func TestDNSStatsEndpoint(t *testing.T) {
	srv, ring, reg := newDNSTestServer(t)
	cookie := login(t, srv, testPassword)

	now := time.Now()
	ring.Append(knotdns.QueryEvent{When: now, QName: "ok.example", QType: "A"})
	ring.Append(knotdns.QueryEvent{When: now, QName: "ad.tracker.com", QType: "A", Blocked: true, BlockedBy: "ads"})
	ring.Append(knotdns.QueryEvent{When: now, QName: "ad.tracker.com", QType: "A", Blocked: true, BlockedBy: "ads"})

	bl := knotdns.NewBlocklist("ads")
	bl.Add("tracker.com")
	reg.Set("ads", bl)

	req := httptest.NewRequest(http.MethodGet, "/dns/stats", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var got dnsStatsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Queries != 3 || got.Blocked != 2 {
		t.Errorf("queries=%d blocked=%d", got.Queries, got.Blocked)
	}
	if got.BlockedRatio < 0.66 || got.BlockedRatio > 0.67 {
		t.Errorf("blocked_ratio=%f", got.BlockedRatio)
	}
	if len(got.TopBlocked) != 1 || got.TopBlocked[0].Name != "ad.tracker.com" {
		t.Errorf("top_blocked=%+v", got.TopBlocked)
	}
	if got.Blocklists["ads"] != 1 {
		t.Errorf("blocklists=%+v", got.Blocklists)
	}
}

func TestDNSQueriesEndpointLimit(t *testing.T) {
	srv, ring, _ := newDNSTestServer(t)
	cookie := login(t, srv, testPassword)

	now := time.Now()
	for i := 0; i < 5; i++ {
		ring.Append(knotdns.QueryEvent{
			When:  now.Add(time.Duration(i) * time.Second),
			QName: "q.example",
			QType: "A",
		})
	}

	req := httptest.NewRequest(http.MethodGet, "/dns/queries?limit=2", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var body struct {
		Queries []dnsQueryEntry `json:"queries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Queries) != 2 {
		t.Errorf("limit=2: got %d", len(body.Queries))
	}
}

func TestDNSQueriesRejectsBadParams(t *testing.T) {
	srv, _, _ := newDNSTestServer(t)
	cookie := login(t, srv, testPassword)

	for _, q := range []string{"?limit=-1", "?limit=abc", "?since=not-a-date"} {
		req := httptest.NewRequest(http.MethodGet, "/dns/queries"+q, nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("query %q: want 400, got %d", q, rec.Code)
		}
	}
}

func TestDNSEndpointsRequireAuth(t *testing.T) {
	srv, _, _ := newDNSTestServer(t)
	for _, path := range []string{"/dns/stats", "/dns/queries", "/dns/refresh"} {
		method := http.MethodGet
		if path == "/dns/refresh" {
			method = http.MethodPost
		}
		req := httptest.NewRequest(method, path, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: want 401, got %d", path, rec.Code)
		}
	}
}

func TestDNSStatsDisabledWhenUnconfigured(t *testing.T) {
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
	req := httptest.NewRequest(http.MethodGet, "/dns/stats", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("unconfigured: want 503, got %d", rec.Code)
	}
}
