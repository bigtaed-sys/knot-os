package zapret

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LocateBinary returns the path to a usable nfqws binary, preferring
// the trusted image-staged copy over a previously downloaded one.
// ok=false when neither exists.
func LocateBinary(base string) (string, bool) {
	if fi, err := os.Stat(ImageBinPath); err == nil && !fi.IsDir() {
		return ImageBinPath, true
	}
	dl := DownloadedBinPath(base)
	if fi, err := os.Stat(dl); err == nil && !fi.IsDir() {
		return dl, true
	}
	return "", false
}

// EnsureBinary returns a usable nfqws path, downloading + verifying it
// into base when neither the image copy nor a cached download exists.
// The download is sha256-pinned against the release manifest, so an
// unverified binary is never written or executed.
func EnsureBinary(ctx context.Context, base string) (string, error) {
	if p, ok := LocateBinary(base); ok {
		return p, nil
	}
	dst := DownloadedBinPath(base)
	if err := downloadNfqws(ctx, dst); err != nil {
		return "", err
	}
	return dst, nil
}

// downloadNfqws fetches the pinned release tarball, extracts the arm64
// nfqws member, verifies its sha256, and installs it 0755 at dst.
func downloadNfqws(ctx context.Context, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, DownloadURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download nfqws: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download nfqws: HTTP %d", resp.StatusCode)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("gunzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("nfqws member %q not found in tarball", NfqwsTarMember)
		}
		if err != nil {
			return fmt.Errorf("read tarball: %w", err)
		}
		if filepath.ToSlash(hdr.Name) != NfqwsTarMember {
			continue
		}
		// Read the member fully so we can hash before writing.
		data, err := io.ReadAll(io.LimitReader(tr, 8<<20)) // 8 MiB cap
		if err != nil {
			return fmt.Errorf("extract nfqws: %w", err)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != NfqwsSHA256 {
			return fmt.Errorf("nfqws sha256 mismatch: got %s, want %s", got, NfqwsSHA256)
		}
		tmp := dst + ".tmp"
		if err := os.WriteFile(tmp, data, 0o755); err != nil {
			return err
		}
		return os.Rename(tmp, dst)
	}
}

// listFiles are the on-disk assets the refresh action re-pulls from
// upstream. Domain lists change often; the fakes rarely, but pulling
// them too keeps a single refresh authoritative.
var refreshTargets = []struct {
	urlPath string // relative to ListsRefreshBase
	dstRel  func(base string) string
}{
	{"lists/list-general.txt", func(b string) string { return filepath.Join(ListsDir(b), "list-general.txt") }},
	{"lists/list-google.txt", func(b string) string { return filepath.Join(ListsDir(b), "list-google.txt") }},
	{"lists/list-exclude.txt", func(b string) string { return filepath.Join(ListsDir(b), "list-exclude.txt") }},
	{"lists/ipset-exclude.txt", func(b string) string { return filepath.Join(ListsDir(b), "ipset-exclude.txt") }},
	{"lists/ipset-all.txt", func(b string) string { return filepath.Join(ListsDir(b), "ipset-all.txt") }},
}

// strategyTargets maps each strategy ID to its upstream Flowseal .bat
// filename. Refreshing re-pulls these so the catalogue tracks upstream
// without a new knotd build (LoadStrategies converts them on load).
var strategyTargets = map[string]string{
	"general":       "general.bat",
	"alt":           "general (ALT).bat",
	"alt2":          "general (ALT2).bat",
	"alt3":          "general (ALT3).bat",
	"alt4":          "general (ALT4).bat",
	"alt5":          "general (ALT5).bat",
	"alt6":          "general (ALT6).bat",
	"fake-tls-auto": "general (FAKE TLS AUTO).bat",
	"simple-fake":   "general (SIMPLE FAKE).bat",
}

// RefreshStrategies re-pulls the strategy .bat files from upstream into
// <base>/strategies, where LoadStrategies prefers them over the seed.
// Returns the number updated.
func RefreshStrategies(ctx context.Context, base string) (int, error) {
	dir := filepath.Join(base, "strategies")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	updated := 0
	for id, fname := range strategyTargets {
		url := ListsRefreshBase + "/" + urlEscapePath(fname)
		data, err := fetch(cctx, url, 256<<10)
		if err != nil {
			return updated, fmt.Errorf("refresh strategy %s: %w", id, err)
		}
		dst := filepath.Join(dir, id+".bat")
		if err := writeAtomic(dst, data, 0o644); err != nil {
			return updated, err
		}
		updated++
	}
	return updated, nil
}

// Refresh re-pulls both the domain lists and the strategy .bat files
// from upstream Flowseal. Returns the total number of files updated.
func Refresh(ctx context.Context, base string) (int, error) {
	n, err := RefreshLists(ctx, base)
	if err != nil {
		return n, err
	}
	m, err := RefreshStrategies(ctx, base)
	return n + m, err
}

// RefreshLists re-downloads the domain lists from the upstream Flowseal
// repo into base. Each file is written atomically, so a failed refresh
// never leaves a half-written list. Returns the number updated.
func RefreshLists(ctx context.Context, base string) (int, error) {
	if err := os.MkdirAll(ListsDir(base), 0o755); err != nil {
		return 0, err
	}
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	updated := 0
	for _, t := range refreshTargets {
		data, err := fetch(cctx, ListsRefreshBase+"/"+t.urlPath, 4<<20)
		if err != nil {
			return updated, fmt.Errorf("refresh %s: %w", t.urlPath, err)
		}
		if err := writeAtomic(t.dstRel(base), data, 0o644); err != nil {
			return updated, err
		}
		updated++
	}
	return updated, nil
}

// fetch GETs url and returns the body (capped at limit bytes).
func fetch(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// writeAtomic writes data to a temp file and renames it into place.
func writeAtomic(dst string, data []byte, mode os.FileMode) error {
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// urlEscapePath percent-encodes spaces and parentheses in a GitHub raw
// path segment (Flowseal strategy filenames contain both).
func urlEscapePath(p string) string {
	r := strings.NewReplacer(" ", "%20", "(", "%28", ")", "%29")
	return r.Replace(p)
}
