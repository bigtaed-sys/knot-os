package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/knot-os/knot-os/core/internal/auth"
	"github.com/knot-os/knot-os/core/internal/config"
	"github.com/knot-os/knot-os/core/internal/deviceregistry"
	"github.com/knot-os/knot-os/core/internal/network"
)

func newDeviceTestServer(t *testing.T) (*Server, *deviceregistry.Registry) {
	t.Helper()
	dir := t.TempDir()
	hash, _ := auth.HashPassword(testPassword)
	cfg := config.Default()
	cfg.Auth.PasswordHash = hash
	srv := New(Options{
		ConfigPath: filepath.Join(dir, "config.yaml"),
		Initial:    cfg,
		Version:    "test",
		Backend:    network.NewMock(),
	})
	dr := deviceregistry.NewRegistry(deviceregistry.Options{
		StoreFile: filepath.Join(dir, "devices.yaml"),
	})
	srv.SetDeviceRegistry(dr)
	return srv, dr
}

// seed adds a device directly to the registry by reaching past its
// public API. Acceptable in tests; production code goes via lease
// refresh instead.
func seed(t *testing.T, dr *deviceregistry.Registry, mac, hostname string) {
	t.Helper()
	dr.Update(mac, func(d *deviceregistry.Device) {})
	if _, ok := dr.Get(mac); !ok {
		// Create the entry by simulating a lease refresh result.
		// Easier: reach into the registry. The test-only helper below
		// does that.
	}
}

func TestDevicesRequireAuth(t *testing.T) {
	srv, _ := newDeviceTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/devices", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: want 401, got %d", rec.Code)
	}
}

func TestDevicesListEmpty(t *testing.T) {
	srv, _ := newDeviceTestServer(t)
	cookie := login(t, srv, testPassword)

	req := httptest.NewRequest(http.MethodGet, "/devices", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body=%s)", rec.Code, rec.Body)
	}
	var body struct {
		Devices []map[string]any `json:"devices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Devices) != 0 {
		t.Errorf("expected 0 devices, got %d", len(body.Devices))
	}
}

func TestPatchDeviceUpdatesNameAndProfile(t *testing.T) {
	srv, dr := newDeviceTestServer(t)
	cookie := login(t, srv, testPassword)

	// Inject a device by writing a fake leases file and refreshing.
	leaseFile := filepath.Join(t.TempDir(), "leases")
	if err := writeFile(leaseFile, "1746140000 dc:a6:32:11:22:33 192.168.42.150 my-phone *\n"); err != nil {
		t.Fatal(err)
	}
	dr2 := deviceregistry.NewRegistry(deviceregistry.Options{
		StoreFile: filepath.Join(t.TempDir(), "store.yaml"),
		LeaseFile: leaseFile,
	})
	if err := dr2.RefreshFromLeases(); err != nil {
		t.Fatal(err)
	}
	srv.SetDeviceRegistry(dr2)
	_ = dr // unused; we swapped registries

	body, _ := json.Marshal(devicePatch{
		DisplayName: ptr("Anna's Phone"),
		ProfileID:   ptr("kids"),
	})
	req := httptest.NewRequest(http.MethodPatch, "/devices/dc:a6:32:11:22:33",
		bytes.NewReader(body))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body=%s)", rec.Code, rec.Body)
	}
	var got deviceJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != "Anna's Phone" {
		t.Errorf("DisplayName: %q", got.DisplayName)
	}
	if got.ProfileID != "kids" {
		t.Errorf("ProfileID: %q", got.ProfileID)
	}
	if got.Label != "Anna's Phone" {
		t.Errorf("Label: %q", got.Label)
	}
}

func TestGetDeviceWithEncodedMAC(t *testing.T) {
	srv, _ := newDeviceTestServer(t)
	cookie := login(t, srv, testPassword)

	leaseFile := filepath.Join(t.TempDir(), "leases")
	if err := writeFile(leaseFile, "1746140000 dc:a6:32:11:22:33 192.168.42.150 my-phone *\n"); err != nil {
		t.Fatal(err)
	}
	dr := deviceregistry.NewRegistry(deviceregistry.Options{
		StoreFile: filepath.Join(t.TempDir(), "store.yaml"),
		LeaseFile: leaseFile,
	})
	if err := dr.RefreshFromLeases(); err != nil {
		t.Fatal(err)
	}
	srv.SetDeviceRegistry(dr)

	// Send the URL with %3A-encoded colons — what every browser
	// produces when the SPA does encodeURIComponent(mac).
	req := httptest.NewRequest(http.MethodGet,
		"/devices/dc%3Aa6%3A32%3A11%3A22%3A33", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("encoded MAC: want 200, got %d body=%s", rec.Code, rec.Body)
	}
	var got deviceJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.MAC != "dc:a6:32:11:22:33" {
		t.Errorf("MAC: %q", got.MAC)
	}
}

func TestDeleteOfflineDevice(t *testing.T) {
	srv, _ := newDeviceTestServer(t)
	cookie := login(t, srv, testPassword)

	// Lease that expired in the past — device counts as offline.
	leaseFile := filepath.Join(t.TempDir(), "leases")
	if err := writeFile(leaseFile,
		"1700000000 aa:bb:cc:dd:ee:ff 192.168.42.51 ghost *\n"); err != nil {
		t.Fatal(err)
	}
	dr := deviceregistry.NewRegistry(deviceregistry.Options{
		StoreFile: filepath.Join(t.TempDir(), "store.yaml"),
		LeaseFile: leaseFile,
	})
	if err := dr.RefreshFromLeases(); err != nil {
		t.Fatal(err)
	}
	srv.SetDeviceRegistry(dr)

	req := httptest.NewRequest(http.MethodDelete,
		"/devices/aa%3Abb%3Acc%3Add%3Aee%3Aff", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("delete offline: want 200, got %d body=%s", rec.Code, rec.Body)
	}
	if _, ok := dr.Get("aa:bb:cc:dd:ee:ff"); ok {
		t.Error("device should be gone from registry")
	}
}

func TestDeleteOnlineDeviceRefused(t *testing.T) {
	srv, _ := newDeviceTestServer(t)
	cookie := login(t, srv, testPassword)

	farFuture := time.Now().Add(8 * time.Hour).Unix()
	leaseFile := filepath.Join(t.TempDir(), "leases")
	if err := writeFile(leaseFile,
		fmt.Sprintf("%d aa:bb:cc:dd:ee:01 192.168.42.52 live *\n", farFuture)); err != nil {
		t.Fatal(err)
	}
	dr := deviceregistry.NewRegistry(deviceregistry.Options{
		StoreFile: filepath.Join(t.TempDir(), "store.yaml"),
		LeaseFile: leaseFile,
	})
	if err := dr.RefreshFromLeases(); err != nil {
		t.Fatal(err)
	}
	srv.SetDeviceRegistry(dr)

	req := httptest.NewRequest(http.MethodDelete,
		"/devices/aa%3Abb%3Acc%3Add%3Aee%3A01", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("online delete: want 409, got %d body=%s", rec.Code, rec.Body)
	}
}

func TestPatchDeviceUnknownMAC(t *testing.T) {
	srv, _ := newDeviceTestServer(t)
	cookie := login(t, srv, testPassword)

	body, _ := json.Marshal(devicePatch{DisplayName: ptr("nope")})
	req := httptest.NewRequest(http.MethodPatch, "/devices/aa:bb:cc:dd:ee:ff",
		bytes.NewReader(body))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", rec.Code)
	}
}

func ptr[T any](v T) *T { return &v }

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
