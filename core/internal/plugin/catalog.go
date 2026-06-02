package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// CatalogEntry is one plugin listed in the store index. The index is a
// plain JSON file hosted in a GitHub repo (raw URL); knotd fetches it
// and the UI renders it. `Official` is a display hint only — the real
// trust decision happens at install time when the package's signature
// is verified against a trusted key, never from this flag.
type CatalogEntry struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Version     string   `json:"version,omitempty"`
	Author      string   `json:"author,omitempty"`
	Official    bool     `json:"official,omitempty"`
	URL         string   `json:"url"`               // zip package
	SigURL      string   `json:"sig_url,omitempty"` // detached .sig
	Permissions []string `json:"permissions,omitempty"`
}

// Catalog is the store index document.
type Catalog struct {
	Plugins []CatalogEntry `json:"plugins"`
}

// FetchCatalog downloads and parses the store index from url. The
// caller supplies the HTTP client (so the daemon controls timeouts).
func FetchCatalog(ctx context.Context, client *http.Client, url string) (Catalog, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Catalog{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return Catalog{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Catalog{}, fmt.Errorf("store index %s: HTTP %d", url, resp.StatusCode)
	}
	var cat Catalog
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&cat); err != nil {
		return Catalog{}, fmt.Errorf("parse store index: %w", err)
	}
	return cat, nil
}
