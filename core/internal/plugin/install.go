package plugin

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrConfirmationRequired is returned by Install when a package isn't
// signed by a trusted key and the caller hasn't explicitly confirmed.
// The API surfaces it as 409 so the UI can show a "third-party code"
// warning and retry with Confirm=true.
var ErrConfirmationRequired = errors.New("plugin: untrusted package requires explicit confirmation")

// Installer downloads, verifies, and unpacks plugin packages into the
// plugins directory. A package is a zip of the plugin's files
// (plugin.yaml at the root + the executable). An optional detached
// Ed25519 signature over the zip bytes, verifying against one of
// TrustedKeys, marks the package "official" and lets it install
// without confirmation; anything else needs Confirm=true.
type Installer struct {
	// Dir is the install root (e.g. /usr/lib/knot/plugins).
	Dir string
	// TrustedKeys are the public keys whose signature marks a package
	// as trusted/official (typically just the release key).
	TrustedKeys []ed25519.PublicKey
	// HTTPClient fetches packages. nil → a default with a timeout.
	HTTPClient *http.Client
	// MaxZipBytes caps the compressed download + each uncompressed
	// entry. 0 → 32 MiB.
	MaxZipBytes int64
}

// InstallRequest describes one install.
type InstallRequest struct {
	URL     string // zip package URL
	SigURL  string // optional detached .sig URL
	Confirm bool   // operator acknowledged an untrusted package
}

// InstallResult reports what landed on disk.
type InstallResult struct {
	ID      string `json:"id"`
	Trusted bool   `json:"trusted"`
}

const defaultMaxZip = 32 << 20

func (in *Installer) client() *http.Client {
	if in.HTTPClient != nil {
		return in.HTTPClient
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (in *Installer) maxZip() int64 {
	if in.MaxZipBytes > 0 {
		return in.MaxZipBytes
	}
	return defaultMaxZip
}

// Install runs the full flow: download → verify → confirm-gate →
// safe-extract → validate manifest → atomically place into Dir/<id>.
// The returned InstallResult.ID names the freshly-installed plugin so
// the caller can re-Discover and (optionally) enable it.
func (in *Installer) Install(ctx context.Context, req InstallRequest) (InstallResult, error) {
	if req.URL == "" {
		return InstallResult{}, errors.New("plugin: install URL required")
	}
	zipBytes, err := in.download(ctx, req.URL, in.maxZip())
	if err != nil {
		return InstallResult{}, fmt.Errorf("download package: %w", err)
	}

	trusted := false
	if req.SigURL != "" && len(in.TrustedKeys) > 0 {
		sig, err := in.download(ctx, req.SigURL, 1<<16)
		if err != nil {
			return InstallResult{}, fmt.Errorf("download signature: %w", err)
		}
		for _, k := range in.TrustedKeys {
			if len(k) == ed25519.PublicKeySize && ed25519.Verify(k, zipBytes, sig) {
				trusted = true
				break
			}
		}
		if !trusted {
			return InstallResult{}, errors.New("plugin: signature did not verify against any trusted key")
		}
	}

	// Untrusted (unsigned, or no trusted keys configured) → the
	// operator must explicitly confirm running third-party code.
	if !trusted && !req.Confirm {
		return InstallResult{}, ErrConfirmationRequired
	}

	id, err := in.unpack(zipBytes)
	if err != nil {
		return InstallResult{}, err
	}
	return InstallResult{ID: id, Trusted: trusted}, nil
}

// download GETs url, reading at most max bytes.
func (in *Installer) download(ctx context.Context, url string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := in.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("response from %s exceeds %d bytes", url, max)
	}
	return b, nil
}

// unpack safely extracts the zip into a temp dir, validates the
// manifest, then atomically swaps it into Dir/<id>. Returns the id.
//
// Hardened against zip-slip: entry names are cleaned and confined to
// the destination; absolute paths, "..", symlinks, and non-regular
// files are rejected.
func (in *Installer) unpack(zipBytes []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return "", fmt.Errorf("open zip: %w", err)
	}

	if err := os.MkdirAll(in.Dir, 0o755); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(in.Dir, ".install-*")
	if err != nil {
		return "", err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(staging)
		}
	}()

	for _, f := range zr.File {
		if err := extractEntry(staging, f, in.maxZip()); err != nil {
			return "", err
		}
	}

	// Validate the manifest the package brought.
	m, err := loadManifest(filepath.Join(staging, ManifestFile))
	if err != nil {
		return "", fmt.Errorf("package %s: %w", ManifestFile, err)
	}

	// Atomically replace any existing install of this id.
	dest := filepath.Join(in.Dir, m.ID)
	if filepath.Dir(dest) != filepath.Clean(in.Dir) {
		return "", fmt.Errorf("plugin id %q escapes the plugins dir", m.ID)
	}
	_ = os.RemoveAll(dest)
	if err := os.Rename(staging, dest); err != nil {
		return "", fmt.Errorf("install %s: %w", m.ID, err)
	}
	cleanup = false
	// Make any executable bits survive: zip preserves mode, but be
	// defensive — the manifest's exec target needs +x.
	_ = makeExecExecutable(dest, m)
	return m.ID, nil
}

// extractEntry writes one zip entry into dst, refusing anything that
// would escape it.
func extractEntry(dst string, f *zip.File, maxBytes int64) error {
	name := f.Name
	if name == "" {
		return nil
	}
	// Reject absolute paths and any traversal.
	clean := filepath.Clean("/" + name) // forces it under root, collapses ".."
	target := filepath.Join(dst, clean)
	if target != dst && !strings.HasPrefix(target, dst+string(os.PathSeparator)) {
		return fmt.Errorf("zip entry %q escapes destination", name)
	}

	info := f.FileInfo()
	switch {
	case info.IsDir():
		return os.MkdirAll(target, 0o755)
	case !info.Mode().IsRegular():
		// No symlinks, devices, etc.
		return fmt.Errorf("zip entry %q is not a regular file", name)
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	mode := f.Mode().Perm()
	if mode == 0 {
		mode = 0o644
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, io.LimitReader(rc, maxBytes+1)); err != nil {
		return err
	}
	return nil
}

// makeExecExecutable ensures a "./"-relative Exec target is +x after
// extraction, since some zip writers drop the mode bits.
func makeExecExecutable(dir string, m Manifest) error {
	if len(m.Exec) == 0 {
		return nil
	}
	a0 := m.Exec[0]
	if !strings.HasPrefix(a0, "./") {
		return nil
	}
	return os.Chmod(filepath.Join(dir, a0[2:]), 0o755)
}

// Uninstall removes a plugin's directory from disk. The caller is
// responsible for stopping its process and re-Discovering first.
func (in *Installer) Uninstall(id string) error {
	if !idRE.MatchString(id) {
		return fmt.Errorf("plugin: invalid id %q", id)
	}
	dest := filepath.Join(in.Dir, id)
	if filepath.Dir(dest) != filepath.Clean(in.Dir) {
		return fmt.Errorf("plugin id %q escapes the plugins dir", id)
	}
	return os.RemoveAll(dest)
}
