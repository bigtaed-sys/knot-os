package tgproxy

import (
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

// LocateBinary returns a usable tg-ws-proxy path, preferring the trusted
// image-staged copy over a previously downloaded one. ok=false when
// neither exists.
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

// DownloadedBinPath is where a fetched binary is cached under base.
func DownloadedBinPath(base string) string { return filepath.Join(base, "tg-ws-proxy") }

// EnsureBinary returns a usable proxy path, downloading + verifying it
// into base when neither the image copy nor a cached download exists.
// The download is sha256-pinned, so an unverified binary is never written
// or executed.
func EnsureBinary(ctx context.Context, base string) (string, error) {
	if p, ok := LocateBinary(base); ok {
		return p, nil
	}
	dst := DownloadedBinPath(base)
	if err := downloadBinary(ctx, dst); err != nil {
		return "", err
	}
	return dst, nil
}

// downloadBinary fetches the pinned static binary, verifies its sha256,
// and installs it 0755 at dst.
func downloadBinary(ctx context.Context, dst string) error {
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
		return fmt.Errorf("download tg-ws-proxy: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download tg-ws-proxy: HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20)) // 32 MiB cap
	if err != nil {
		return fmt.Errorf("read tg-ws-proxy: %w", err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != BinSHA256 {
		return fmt.Errorf("tg-ws-proxy sha256 mismatch: got %s, want %s", got, BinSHA256)
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
