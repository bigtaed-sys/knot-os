package zapret

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// strategyContentsAPI lists the Flowseal repo root so a refresh can
// discover EVERY strategy .bat upstream ships — the catalogue grows
// (ALT7…ALT12, EXP, FAKE TLS AUTO ALT…) without a knotd change.
const strategyContentsAPI = "https://api.github.com/repos/Flowseal/zapret-discord-youtube/contents/"

// strategyTargets is the fallback set used when the GitHub contents API
// can't be reached (rate-limited / offline). Keeps a refresh useful even
// without live discovery. Maps strategy ID → upstream .bat filename.
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

// strategyFile is one upstream strategy to pull.
type strategyFile struct {
	id     string
	rawURL string
}

// isStrategyBat reports whether an upstream filename is a strategy preset
// (a "general*.bat"), excluding the service installer.
func isStrategyBat(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".bat") &&
		strings.HasPrefix(lower, "general") &&
		lower != "service.bat"
}

// strategyIDFromFilename derives a stable ID from a Flowseal filename:
// "general.bat"→"general", "general (ALT2).bat"→"alt2",
// "general (FAKE TLS AUTO).bat"→"fake-tls-auto". Matches the seed IDs.
func strategyIDFromFilename(name string) string {
	base := strings.TrimSuffix(name, ".bat")
	base = strings.TrimSpace(strings.TrimPrefix(base, "general"))
	base = strings.NewReplacer("(", "", ")", "").Replace(base)
	if strings.TrimSpace(base) == "" {
		return "general"
	}
	return strings.ToLower(strings.Join(strings.Fields(base), "-"))
}

// discoverStrategyFiles enumerates every strategy .bat in the Flowseal
// repo root via the GitHub contents API.
func discoverStrategyFiles(ctx context.Context) ([]strategyFile, error) {
	body, err := fetch(ctx, strategyContentsAPI, 1<<20)
	if err != nil {
		return nil, err
	}
	var entries []struct {
		Name        string `json:"name"`
		DownloadURL string `json:"download_url"`
		Type        string `json:"type"`
	}
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, err
	}
	var out []strategyFile
	for _, e := range entries {
		if e.Type != "file" || !isStrategyBat(e.Name) {
			continue
		}
		url := e.DownloadURL
		if url == "" {
			url = ListsRefreshBase + "/" + urlEscapePath(e.Name)
		}
		out = append(out, strategyFile{id: strategyIDFromFilename(e.Name), rawURL: url})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no strategy .bat files found upstream")
	}
	return out, nil
}

// fallbackStrategyFiles builds the pull list from the hardcoded set.
func fallbackStrategyFiles() []strategyFile {
	out := make([]strategyFile, 0, len(strategyTargets))
	for id, fname := range strategyTargets {
		out = append(out, strategyFile{id: id, rawURL: ListsRefreshBase + "/" + urlEscapePath(fname)})
	}
	return out
}

// RefreshStrategies re-pulls the strategy .bat files from upstream into
// <base>/strategies, where LoadStrategies prefers them over the seed. It
// discovers the full upstream set live (falling back to the known set if
// the listing API is unavailable). Returns the number updated.
func RefreshStrategies(ctx context.Context, base string) (int, error) {
	dir := filepath.Join(base, "strategies")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	files, err := discoverStrategyFiles(cctx)
	if err != nil {
		// Rate-limited/offline listing → still refresh the known set.
		files = fallbackStrategyFiles()
	}

	updated := 0
	var errs []string
	for _, f := range files {
		data, ferr := fetch(cctx, f.rawURL, 256<<10)
		if ferr != nil {
			// Upstream renamed/removed this one — keep the seed/existing
			// copy and carry on rather than aborting the whole refresh.
			errs = append(errs, fmt.Sprintf("%s: %v", f.id, ferr))
			continue
		}
		// Only overwrite when the download actually converts. A format
		// change upstream must never clobber a working strategy with an
		// unparseable file (LoadStrategies would then have to drop it).
		if s, cerr := ConvertBat(string(data)); cerr != nil || len(s.Args) == 0 {
			errs = append(errs, fmt.Sprintf("%s: unparseable after download", f.id))
			continue
		}
		if err := writeAtomic(filepath.Join(dir, f.id+".bat"), data, 0o644); err != nil {
			return updated, err
		}
		updated++
	}
	// Only a hard error when nothing at all could be refreshed.
	if updated == 0 && len(errs) > 0 {
		return 0, fmt.Errorf("no strategies refreshed (%s)", strings.Join(errs, "; "))
	}
	return updated, nil
}

// binContentsAPI lists Flowseal's bin/ dir so a refresh can pull every
// fake-payload .bin. The newer strategies reference bins the seed doesn't
// carry (stun2, ACTIVE_*_UDP, quic_initial_tencent…); without them nfqws
// fails to open the payload and won't start on those strategies.
const binContentsAPI = "https://api.github.com/repos/Flowseal/zapret-discord-youtube/contents/bin"

// RefreshBins re-pulls every fake-payload .bin from upstream into the
// on-disk bin/ dir. Best-effort by design: the caller treats a failure as
// non-fatal (the seed bins cover the common strategies). Returns the
// number updated.
func RefreshBins(ctx context.Context, base string) (int, error) {
	dir := FakeDir(base)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	body, err := fetch(cctx, binContentsAPI, 1<<20)
	if err != nil {
		return 0, err
	}
	var entries []struct {
		Name        string `json:"name"`
		DownloadURL string `json:"download_url"`
		Type        string `json:"type"`
	}
	if err := json.Unmarshal(body, &entries); err != nil {
		return 0, err
	}
	updated := 0
	var errs []string
	for _, e := range entries {
		if e.Type != "file" || !strings.HasSuffix(strings.ToLower(e.Name), ".bin") {
			continue
		}
		url := e.DownloadURL
		if url == "" {
			url = ListsRefreshBase + "/bin/" + urlEscapePath(e.Name)
		}
		data, ferr := fetch(cctx, url, 1<<20)
		if ferr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", e.Name, ferr))
			continue
		}
		if err := writeAtomic(filepath.Join(dir, e.Name), data, 0o644); err != nil {
			return updated, err
		}
		updated++
	}
	if updated == 0 && len(errs) > 0 {
		return 0, fmt.Errorf("no bins refreshed (%s)", strings.Join(errs, "; "))
	}
	return updated, nil
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
	var errs []string
	for _, t := range refreshTargets {
		data, err := fetch(cctx, ListsRefreshBase+"/"+t.urlPath, 4<<20)
		if err != nil {
			// Skip a renamed/removed list rather than aborting the rest.
			errs = append(errs, fmt.Sprintf("%s: %v", t.urlPath, err))
			continue
		}
		if err := writeAtomic(t.dstRel(base), data, 0o644); err != nil {
			return updated, err
		}
		updated++
	}
	if updated == 0 && len(errs) > 0 {
		return 0, fmt.Errorf("no lists refreshed (%s)", strings.Join(errs, "; "))
	}
	return updated, nil
}

// fetch GETs url and returns the body (capped at limit bytes).
func fetch(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// GitHub's API rejects requests with no User-Agent (HTTP 403); harmless
	// on the raw.githubusercontent list/strategy downloads too.
	req.Header.Set("User-Agent", "knot-os")
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
