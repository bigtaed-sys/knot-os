package dns

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Source is one named blocklist URL. The Downloader fetches each
// configured Source on a schedule, parses it as a hosts file, and
// publishes the resulting Blocklist into the Registry under Name.
type Source struct {
	// Name is the blocklist identifier referenced by profiles
	// (e.g. "ads"). Stays stable across refreshes.
	Name string
	// URL is fetched on every refresh.
	URL string
}

// DefaultSources is the v0.2 ship-built-in list. One source is
// enough — it's already 150k+ domains spanning ads, trackers,
// telemetry, and known-bad domains, so per-list separation buys
// little in v0.2 scope. Per-category lists are a v0.3 follow-up.
var DefaultSources = []Source{
	{
		Name: "ads",
		// StevenBlack/hosts unified (ads + tracker + malware).
		// The /hosts file at HEAD is regenerated daily upstream.
		URL: "https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts",
	},
}

// Downloader fetches Sources and publishes them into a Registry.
type Downloader struct {
	registry  *Registry
	sources   []Source
	cacheDir  string
	logger    *log.Logger
	client    *http.Client
	refreshCh chan struct{}

	mu    sync.Mutex
	stats map[string]SourceStats
}

// SourceStats describes the most recent fetch attempt per source.
type SourceStats struct {
	URL         string    `json:"url"`
	LastFetch   time.Time `json:"last_fetch,omitempty"`
	LastSuccess time.Time `json:"last_success,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	EntriesAdded int      `json:"entries_added,omitempty"`
}

// DownloaderOptions configures NewDownloader.
type DownloaderOptions struct {
	// Registry is the destination for parsed blocklists. Required.
	Registry *Registry
	// Sources is the list to download. Empty => DefaultSources.
	Sources []Source
	// CacheDir is where raw downloaded hosts files are kept so we
	// don't re-fetch on every boot. Defaults to /var/lib/knot/blocklists.
	CacheDir string
	// HTTPClient lets tests inject a stub. Default has 30s timeout.
	HTTPClient *http.Client
	// Logger receives operational messages.
	Logger *log.Logger
}

// NewDownloader constructs a Downloader.
func NewDownloader(opts DownloaderOptions) *Downloader {
	if opts.Registry == nil {
		panic("dns.Downloader: Registry required")
	}
	if len(opts.Sources) == 0 {
		opts.Sources = append([]Source(nil), DefaultSources...)
	}
	if opts.CacheDir == "" {
		opts.CacheDir = "/var/lib/knot/blocklists"
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if opts.Logger == nil {
		opts.Logger = log.Default()
	}
	return &Downloader{
		registry:  opts.Registry,
		sources:   opts.Sources,
		cacheDir:  opts.CacheDir,
		logger:    opts.Logger,
		client:    opts.HTTPClient,
		refreshCh: make(chan struct{}, 1),
		stats:     make(map[string]SourceStats),
	}
}

// Stats returns a snapshot of fetch state per source. Safe for the
// HTTP API to expose.
func (d *Downloader) Stats() map[string]SourceStats {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(map[string]SourceStats, len(d.stats))
	for k, v := range d.stats {
		out[k] = v
	}
	return out
}

// LoadCachedNow reads any cached hosts files into the registry
// without making network calls. Suitable for an early-boot warmup
// so DNS filtering works before the first refresh completes.
func (d *Downloader) LoadCachedNow() {
	for _, src := range d.sources {
		path := d.cachePath(src.Name)
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		bl := NewBlocklist(src.Name)
		added, perr := ParseHostsFile(f, bl)
		_ = f.Close()
		if perr != nil {
			d.logger.Printf("dns: cached %s parse: %v", src.Name, perr)
			continue
		}
		d.registry.Set(src.Name, bl)
		d.logger.Printf("dns: loaded %d cached entries for %q from %s", added, src.Name, path)
	}
}

// RefreshAll fetches every source once. Errors per-source are
// recorded in stats but do not abort the others.
func (d *Downloader) RefreshAll(ctx context.Context) {
	if err := os.MkdirAll(d.cacheDir, 0o755); err != nil {
		d.logger.Printf("dns: mkdir %s: %v", d.cacheDir, err)
		// Carry on — the parser will still publish an in-memory list,
		// we just won't have an on-disk cache.
	}
	for _, src := range d.sources {
		d.refreshOne(ctx, src)
	}
}

func (d *Downloader) refreshOne(ctx context.Context, src Source) {
	stats := SourceStats{URL: src.URL, LastFetch: time.Now()}
	defer func() {
		d.mu.Lock()
		d.stats[src.Name] = stats
		d.mu.Unlock()
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		stats.LastError = err.Error()
		return
	}
	resp, err := d.client.Do(req)
	if err != nil {
		stats.LastError = err.Error()
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		stats.LastError = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return
	}

	// Stream into a temp file so we don't hold ~5 MB of hosts text in
	// memory just to parse it. Then atomically swap the cache file.
	cachePath := d.cachePath(src.Name)
	tmp, err := os.CreateTemp(d.cacheDir, ".blocklist-*.tmp")
	if err != nil {
		stats.LastError = err.Error()
		return
	}
	tmpName := tmp.Name()
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		stats.LastError = err.Error()
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		stats.LastError = err.Error()
		return
	}

	// Parse the freshly written file.
	f, err := os.Open(tmpName)
	if err != nil {
		_ = os.Remove(tmpName)
		stats.LastError = err.Error()
		return
	}
	bl := NewBlocklist(src.Name)
	added, perr := ParseHostsFile(f, bl)
	_ = f.Close()
	if perr != nil && !errors.Is(perr, io.EOF) {
		_ = os.Remove(tmpName)
		stats.LastError = "parse: " + perr.Error()
		return
	}

	// Promote into the registry and replace the cache atomically.
	d.registry.Set(src.Name, bl)
	if err := os.Rename(tmpName, cachePath); err != nil {
		// Non-fatal — we already published in-memory.
		d.logger.Printf("dns: cache rename %s: %v", cachePath, err)
		_ = os.Remove(tmpName)
	}

	stats.LastSuccess = time.Now()
	stats.EntriesAdded = added
	stats.LastError = ""
	d.logger.Printf("dns: refreshed blocklist %q from %s: %d entries",
		src.Name, src.URL, added)
}

// Run blocks until ctx is cancelled, refreshing every 24h. The first
// refresh runs immediately (after a short delay so other startup
// noise doesn't compete with the download).
func (d *Downloader) Run(ctx context.Context) {
	d.LoadCachedNow()
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.refreshCh:
			d.RefreshAll(ctx)
		case <-timer.C:
			d.RefreshAll(ctx)
		case <-ticker.C:
			d.RefreshAll(ctx)
		}
	}
}

// RefreshNow asks the run loop to run a refresh as soon as possible.
// Non-blocking; safe to call from HTTP handlers.
func (d *Downloader) RefreshNow() {
	select {
	case d.refreshCh <- struct{}{}:
	default:
		// A refresh is already queued; drop this one.
	}
}

func (d *Downloader) cachePath(name string) string {
	// Hash the name so unusual characters don't blow up paths.
	h := sha256.Sum256([]byte(name))
	short := hex.EncodeToString(h[:6])
	return filepath.Join(d.cacheDir, name+"-"+short+".hosts")
}
