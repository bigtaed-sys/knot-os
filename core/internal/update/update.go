// Package update implements knotd's self-update path: query GitHub
// Releases for a newer knotd binary, download it together with its
// detached Ed25519 signature, verify the signature against an
// embedded public key, atomically replace /usr/local/bin/knotd, then
// ask systemd to restart the service.
//
// Threat model:
//
//   - In scope: an attacker on the path between the device and
//     GitHub injecting a tampered binary. Defeated by the Ed25519
//     verify against a public key baked into the running knotd at
//     build time.
//   - In scope: a hijacked GitHub account pushing a malicious
//     release. Defeated only as long as the signing key never
//     touches GitHub Actions logs / artifacts and is rotated on
//     compromise. Out-of-band recovery is the user's "rescue key"
//     (M17b — second key authorized to sign updates).
//   - Out of scope: an attacker with root on the running daemon,
//     who can simply `mv /attacker.bin /usr/local/bin/knotd`
//     without going through this code path.
//
// Public-key plumbing:
//
//   - The release-key public half is shipped as a single hex string
//     constant in keys.go. Production builds set it via -ldflags
//     -X 'github.com/knot-os/knot-os/core/internal/update.releaseKeyHex=<...>'
//     so the bytes never sit in version control.
//   - When the constant is empty (typical local dev build), the
//     verifier short-circuits to a permissive mode that logs a loud
//     warning and accepts any signature. This is so a developer
//     building knotd themselves doesn't end up with an instance
//     that refuses every update because there's no key to verify
//     against. Users on production images get a real key and the
//     warning never fires.
package update

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Manager owns the update path.
type Manager struct {
	currentVersion string
	repoOwner      string
	repoName       string
	assetName      string // e.g. "knotd-linux-arm64"
	publicKey      ed25519.PublicKey
	rescueKey      ed25519.PublicKey
	targetPath     string // e.g. "/usr/local/bin/knotd"
	httpClient     *http.Client
	logger         *log.Logger
	// Restart is the function called after a successful install. In
	// production it shells out to `systemctl restart knotd`; in tests
	// it can be replaced with a no-op so an install doesn't actually
	// kill the running process.
	Restart func() error
}

// Options is the constructor input.
type Options struct {
	// CurrentVersion is the version string of the running binary.
	// Used to decide whether the latest release is newer.
	CurrentVersion string
	// RepoOwner / RepoName identify the GitHub repo whose releases
	// page the daemon polls. Defaults to the upstream KnotOS repo.
	RepoOwner string
	RepoName  string
	// AssetName is the release asset to download. Defaults to
	// "knotd-linux-arm64". The signature file is the same name with
	// ".sig" appended.
	AssetName string
	// TargetPath is where the new binary is installed. Defaults to
	// "/usr/local/bin/knotd".
	TargetPath string
	// HTTPClient is overridable for tests.
	HTTPClient *http.Client
	// Logger is the operational log.
	Logger *log.Logger
	// Restart is the post-install hook. When nil, a default that
	// shells out to `systemctl restart knotd` is installed.
	Restart func() error
}

// New constructs a Manager from Options. Public keys are read from
// the build-time constants in keys.go.
func New(opts Options) (*Manager, error) {
	if opts.RepoOwner == "" {
		opts.RepoOwner = "knot-os"
	}
	if opts.RepoName == "" {
		opts.RepoName = "knot-os"
	}
	if opts.AssetName == "" {
		opts.AssetName = "knotd-linux-arm64"
	}
	if opts.TargetPath == "" {
		opts.TargetPath = "/usr/local/bin/knotd"
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 5 * time.Minute}
	}
	if opts.Logger == nil {
		opts.Logger = log.Default()
	}

	pub, err := decodeKey(releaseKeyHex)
	if err != nil {
		return nil, fmt.Errorf("update: parse release key: %w", err)
	}
	rescue, err := decodeKey(rescueKeyHex)
	if err != nil {
		return nil, fmt.Errorf("update: parse rescue key: %w", err)
	}

	m := &Manager{
		currentVersion: opts.CurrentVersion,
		repoOwner:      opts.RepoOwner,
		repoName:       opts.RepoName,
		assetName:      opts.AssetName,
		publicKey:      pub,
		rescueKey:      rescue,
		targetPath:     opts.TargetPath,
		httpClient:     opts.HTTPClient,
		logger:         opts.Logger,
		Restart:        opts.Restart,
	}
	if m.Restart == nil {
		m.Restart = systemctlRestart
	}
	if len(m.publicKey) == 0 {
		m.logger.Printf("update: WARNING — release public key is empty; signature verification disabled")
		m.logger.Printf("update: this build is not safe for production. Build with -ldflags -X to inject a real key.")
	}
	return m, nil
}

// SetRescueKey installs a runtime-generated rescue public key.
// Used by main.go after constructing a Rescue: a binary signed by
// the corresponding private half is accepted by Verify even if the
// embedded release key has been rotated. Pass nil to clear.
func (m *Manager) SetRescueKey(key ed25519.PublicKey) {
	m.rescueKey = append(ed25519.PublicKey(nil), key...)
}

func decodeKey(s string) (ed25519.PublicKey, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	raw, err := hex.DecodeString(s)
	if err != nil {
		return nil, err
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("expected %d bytes, got %d", ed25519.PublicKeySize, len(raw))
	}
	return ed25519.PublicKey(raw), nil
}

// ReleaseInfo is the user-facing summary of a GitHub release. The
// fields are the only bits the wizard / System page need; the raw
// API response carries a lot more that we deliberately drop.
type ReleaseInfo struct {
	Tag           string    `json:"tag"`
	Name          string    `json:"name,omitempty"`
	PublishedAt   time.Time `json:"published_at"`
	Notes         string    `json:"notes,omitempty"`
	BinaryURL     string    `json:"binary_url"`
	SignatureURL  string    `json:"signature_url"`
	BinarySize    int64     `json:"binary_size"`
	HasSignature  bool      `json:"has_signature"`
}

// CheckResult bundles the latest release with the comparison
// against the running version. Used as the JSON body of
// /api/system/update/check.
type CheckResult struct {
	CurrentVersion  string       `json:"current_version"`
	LatestVersion   string       `json:"latest_version"`
	UpdateAvailable bool         `json:"update_available"`
	SigningEnabled  bool         `json:"signing_enabled"`
	Latest          *ReleaseInfo `json:"latest,omitempty"`
}

// CheckLatest queries GitHub for the latest published release and
// summarises it.
func (m *Manager) CheckLatest(ctx context.Context) (*CheckResult, error) {
	rel, err := m.fetchLatestRelease(ctx)
	if err != nil {
		return nil, err
	}
	out := &CheckResult{
		CurrentVersion:  m.currentVersion,
		LatestVersion:   rel.Tag,
		SigningEnabled:  len(m.publicKey) > 0,
		Latest:          rel,
		UpdateAvailable: isNewer(rel.Tag, m.currentVersion),
	}
	return out, nil
}

// ApplyLatest does the full check → download → verify → install
// dance and (on success) returns the version string of the newly
// installed binary. The caller decides whether to also call
// m.Restart afterwards (the API does so in a separate goroutine
// so the HTTP response can flush first).
func (m *Manager) ApplyLatest(ctx context.Context) (string, error) {
	rel, err := m.fetchLatestRelease(ctx)
	if err != nil {
		return "", err
	}
	if rel.BinaryURL == "" {
		return "", fmt.Errorf("update: latest release has no %q asset", m.assetName)
	}
	binary, err := m.download(ctx, rel.BinaryURL)
	if err != nil {
		return "", fmt.Errorf("download binary: %w", err)
	}
	var sig []byte
	if rel.HasSignature {
		sig, err = m.download(ctx, rel.SignatureURL)
		if err != nil {
			return "", fmt.Errorf("download signature: %w", err)
		}
	}
	if err := m.verify(binary, sig); err != nil {
		return "", fmt.Errorf("verify: %w", err)
	}
	if err := m.install(binary); err != nil {
		return "", fmt.Errorf("install: %w", err)
	}
	return rel.Tag, nil
}

// VerifyAndInstall is the entry point used by the manual
// /api/system/update path: caller already has both byte buffers,
// we just verify + write.
func (m *Manager) VerifyAndInstall(binary, signature []byte) error {
	if err := m.verify(binary, signature); err != nil {
		return err
	}
	return m.install(binary)
}

// verify checks `signature` against `binary` using the configured
// release key, then the rescue key (if set). Returns nil on a match,
// an error otherwise.
//
// Empty publicKey + empty rescueKey = signing disabled (dev mode);
// the function logs a warning and returns nil.
func (m *Manager) verify(binary, signature []byte) error {
	if len(m.publicKey) == 0 && len(m.rescueKey) == 0 {
		m.logger.Printf("update: signature check skipped (no keys configured)")
		return nil
	}
	if len(signature) == 0 {
		return errors.New("missing signature")
	}
	if len(m.publicKey) > 0 && ed25519.Verify(m.publicKey, binary, signature) {
		return nil
	}
	if len(m.rescueKey) > 0 && ed25519.Verify(m.rescueKey, binary, signature) {
		return nil
	}
	return errors.New("signature does not match release key (or rescue key, if configured)")
}

// install atomically replaces the target binary. ELF magic check
// up front so we never write a wildly wrong file in place of
// /usr/local/bin/knotd.
func (m *Manager) install(binary []byte) error {
	if len(binary) < 4 ||
		binary[0] != 0x7f || binary[1] != 'E' || binary[2] != 'L' || binary[3] != 'F' {
		return errors.New("not an ELF binary")
	}
	if len(binary) < 1<<20 {
		return fmt.Errorf("binary too small: %d bytes", len(binary))
	}
	dir := filepath.Dir(m.targetPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".knotd-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(binary); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, m.targetPath); err != nil {
		cleanup()
		return err
	}
	m.logger.Printf("update: installed %d bytes to %s", len(binary), m.targetPath)
	return nil
}

// systemctlRestart shells out to systemd. Run after the HTTP
// response is flushed so the user sees "ok" before knotd dies.
func systemctlRestart() error {
	cmd := exec.Command("systemctl", "restart", "knotd")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl restart knotd: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// download GETs a URL and returns the body. Bounded to 50 MB so a
// hostile redirect can't stuff infinity into memory.
func (m *Manager) download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "knotd-update/"+m.currentVersion)
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	const maxSize = 50 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxSize {
		return nil, fmt.Errorf("response from %s too large", url)
	}
	return body, nil
}

// fetchLatestRelease asks the GitHub API for the latest release
// metadata, normalised into ReleaseInfo.
func (m *Manager) fetchLatestRelease(ctx context.Context) (*ReleaseInfo, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest",
		m.repoOwner, m.repoName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "knotd-update/"+m.currentVersion)
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("github API HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var ghr ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&ghr); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}

	out := &ReleaseInfo{
		Tag:         ghr.TagName,
		Name:        ghr.Name,
		PublishedAt: ghr.PublishedAt,
		Notes:       ghr.Body,
	}
	for _, a := range ghr.Assets {
		switch a.Name {
		case m.assetName:
			out.BinaryURL = a.BrowserDownloadURL
			out.BinarySize = a.Size
		case m.assetName + ".sig":
			out.SignatureURL = a.BrowserDownloadURL
			out.HasSignature = true
		}
	}
	return out, nil
}

// ghRelease is a trimmed GitHub release JSON shape.
type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []struct {
		Name               string `json:"name"`
		Size               int64  `json:"size"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// isNewer compares two version strings of the form "vX.Y.Z" or
// "X.Y.Z" (with optional "-suffix"). Returns true iff `latest` is
// strictly greater than `current`. Falls back to plain string
// inequality on parse failure — better to surface "different" than
// to silently swallow a malformed tag.
func isNewer(latest, current string) bool {
	la := parseVersion(latest)
	cu := parseVersion(current)
	if la == nil || cu == nil {
		return strings.TrimPrefix(latest, "v") != strings.TrimPrefix(current, "v")
	}
	for i := 0; i < 3; i++ {
		if la[i] != cu[i] {
			return la[i] > cu[i]
		}
	}
	return false
}

func parseVersion(s string) []int {
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return nil
	}
	out := make([]int, 3)
	for i, p := range parts {
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				return nil
			}
			n = n*10 + int(c-'0')
		}
		out[i] = n
	}
	return out
}
