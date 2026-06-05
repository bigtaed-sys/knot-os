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
}

// RefreshLists re-downloads the domain lists from the upstream Flowseal
// repo into base. Each file is fetched to a temp path and renamed only
// on success, so a failed refresh never leaves a half-written list.
// Returns the number of lists updated.
func RefreshLists(ctx context.Context, base string) (int, error) {
	if err := os.MkdirAll(ListsDir(base), 0o755); err != nil {
		return 0, err
	}
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	updated := 0
	for _, t := range refreshTargets {
		url := ListsRefreshBase + "/" + t.urlPath
		req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
		if err != nil {
			return updated, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return updated, fmt.Errorf("refresh %s: %w", t.urlPath, err)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return updated, fmt.Errorf("refresh %s: HTTP %d", t.urlPath, resp.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if err != nil {
			return updated, fmt.Errorf("refresh %s: %w", t.urlPath, err)
		}
		dst := t.dstRel(base)
		tmp := dst + ".tmp"
		if err := os.WriteFile(tmp, data, 0o644); err != nil {
			return updated, err
		}
		if err := os.Rename(tmp, dst); err != nil {
			return updated, err
		}
		updated++
	}
	return updated, nil
}
