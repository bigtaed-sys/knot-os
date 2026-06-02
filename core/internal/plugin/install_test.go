package plugin

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// makePluginZip builds an in-memory plugin package: plugin.yaml at the
// root plus a dummy executable.
func makePluginZip(t *testing.T, id string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	manifest := "id: " + id + "\nname: " + id + "\nversion: 1.0.0\nexec:\n  - ./bin\n"
	mf, _ := zw.Create("plugin.yaml")
	_, _ = mf.Write([]byte(manifest))
	bf, _ := zw.Create("bin")
	_, _ = bf.Write([]byte("#!/bin/true\n"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// serveBytes returns a test server serving the given bytes at every path.
func serveBytes(b []byte) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(b)
	}))
}

func TestInstallUnsignedRequiresConfirm(t *testing.T) {
	pkg := makePluginZip(t, "third-party")
	srv := serveBytes(pkg)
	defer srv.Close()

	in := &Installer{Dir: t.TempDir()}

	// No confirmation → refused.
	_, err := in.Install(context.Background(), InstallRequest{URL: srv.URL + "/p.zip"})
	if !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("want ErrConfirmationRequired, got %v", err)
	}

	// With confirmation → installed, untrusted.
	res, err := in.Install(context.Background(), InstallRequest{URL: srv.URL + "/p.zip", Confirm: true})
	if err != nil {
		t.Fatalf("confirmed install: %v", err)
	}
	if res.ID != "third-party" || res.Trusted {
		t.Errorf("result = %+v, want id=third-party trusted=false", res)
	}
	if _, err := os.Stat(filepath.Join(in.Dir, "third-party", "plugin.yaml")); err != nil {
		t.Errorf("manifest not installed: %v", err)
	}
	// The exec target is +x. Unix mode bits aren't meaningful on
	// Windows (os.Chmod can't set +x there), so assert only on the
	// platform the daemon actually runs on.
	fi, err := os.Stat(filepath.Join(in.Dir, "third-party", "bin"))
	if err != nil {
		t.Errorf("bin not installed: %v", err)
	} else if runtime.GOOS != "windows" && fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("exec bit not set on bin: mode=%v", fi.Mode())
	}
}

func TestInstallTrustedSignatureSkipsConfirm(t *testing.T) {
	pkg := makePluginZip(t, "official")
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sig := ed25519.Sign(priv, pkg)

	mux := http.NewServeMux()
	mux.HandleFunc("/p.zip", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(pkg) })
	mux.HandleFunc("/p.zip.sig", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(sig) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	in := &Installer{Dir: t.TempDir(), TrustedKeys: []ed25519.PublicKey{pub}}
	// No Confirm — a trusted signature is enough.
	res, err := in.Install(context.Background(), InstallRequest{
		URL: srv.URL + "/p.zip", SigURL: srv.URL + "/p.zip.sig",
	})
	if err != nil {
		t.Fatalf("trusted install: %v", err)
	}
	if res.ID != "official" || !res.Trusted {
		t.Errorf("result = %+v, want id=official trusted=true", res)
	}
}

func TestInstallBadSignatureRejected(t *testing.T) {
	pkg := makePluginZip(t, "evil")
	_, wrongPriv, _ := ed25519.GenerateKey(rand.Reader)
	pub, _, _ := ed25519.GenerateKey(rand.Reader) // unrelated trusted key
	sig := ed25519.Sign(wrongPriv, pkg)

	mux := http.NewServeMux()
	mux.HandleFunc("/p.zip", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(pkg) })
	mux.HandleFunc("/p.zip.sig", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(sig) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	in := &Installer{Dir: t.TempDir(), TrustedKeys: []ed25519.PublicKey{pub}}
	_, err := in.Install(context.Background(), InstallRequest{
		URL: srv.URL + "/p.zip", SigURL: srv.URL + "/p.zip.sig", Confirm: true,
	})
	if err == nil {
		t.Fatal("expected install to reject a signature that doesn't verify")
	}
}

func TestUnpackRejectsZipSlip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	// Malicious entry trying to escape the destination.
	f, _ := zw.Create("../../etc/evil")
	_, _ = f.Write([]byte("pwned"))
	_ = zw.Close()

	in := &Installer{Dir: t.TempDir()}
	_, err := in.unpack(buf.Bytes())
	if err == nil {
		t.Fatal("zip-slip entry should have been rejected")
	}
	if _, statErr := os.Stat(filepath.Join(in.Dir, "..", "..", "etc", "evil")); statErr == nil {
		t.Fatal("zip-slip wrote outside the destination!")
	}
}

func TestUninstallRemovesDir(t *testing.T) {
	in := &Installer{Dir: t.TempDir()}
	dir := filepath.Join(in.Dir, "gone")
	_ = os.MkdirAll(dir, 0o755)
	if err := in.Uninstall("gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("plugin dir still present after uninstall")
	}
	if err := in.Uninstall("../escape"); err == nil {
		t.Error("uninstall should reject an id that escapes the dir")
	}
}
