package subscription

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultUserAgent is the UA we send if a subscription doesn't
// specify one. Some providers gate features ("subscription-userinfo"
// header with usage limits) on UAs they recognise — knotd identifies
// itself by default but providers may want a v2rayN-shape string.
const DefaultUserAgent = "knotd-subscription/0.5"

// DefaultFetchTimeout is the per-request deadline. Subscription
// endpoints are usually fast (a few KB of base64); 20s is generous.
const DefaultFetchTimeout = 20 * time.Second

// MaxBundleBytes caps how much we read off the wire. Real
// subscriptions are tens of kilobytes at most; anything bigger is
// almost certainly a misconfiguration or a hostile endpoint.
const MaxBundleBytes = 5 << 20 // 5 MiB

// FetchResult bundles the response body with provider metadata.
//
// Most providers stuff usage limits into the `subscription-userinfo`
// header (RFC-ish format: "upload=...; download=...; total=...;
// expire=..."). We surface the raw header so the UI can render it
// without re-fetching.
type FetchResult struct {
	Body                []byte
	SubscriptionUserinfo string // raw `subscription-userinfo` header
	ProfileTitle         string // raw `profile-title` (often base64-decoded)
	ContentType          string
}

// Fetcher pulls subscription bodies. It's a thin wrapper around an
// http.Client so tests can swap a stub in via the Client field.
type Fetcher struct {
	// Client is the HTTP client used. Nil falls back to a default
	// with timeout + sensible TLS.
	Client *http.Client

	// UserAgent is the default UA. Per-subscription UA overrides this.
	UserAgent string

	// AllowInsecureTLS, when true, skips cert verification. Only set
	// for testing — production should never need it (subscription
	// providers all use real certs).
	AllowInsecureTLS bool
}

// NewFetcher builds a Fetcher with sensible defaults.
func NewFetcher() *Fetcher {
	return &Fetcher{
		Client: &http.Client{
			Timeout: DefaultFetchTimeout,
		},
		UserAgent: DefaultUserAgent,
	}
}

// Fetch makes the HTTP GET. ctx-aware. Returns the response body
// (capped at MaxBundleBytes) plus the subscription-info headers.
func (f *Fetcher) Fetch(ctx context.Context, sub Subscription) (FetchResult, error) {
	if sub.URL == "" {
		return FetchResult{}, errors.New("subscription: empty URL")
	}
	if !strings.HasPrefix(sub.URL, "http://") && !strings.HasPrefix(sub.URL, "https://") {
		return FetchResult{}, fmt.Errorf("subscription: URL must be http(s): %q", sub.URL)
	}

	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: DefaultFetchTimeout}
	}
	if f.AllowInsecureTLS {
		// Clone so we don't mutate a shared transport.
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		client = &http.Client{Timeout: client.Timeout, Transport: tr}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sub.URL, nil)
	if err != nil {
		return FetchResult{}, err
	}
	ua := sub.UserAgent
	if ua == "" {
		ua = f.UserAgent
	}
	if ua == "" {
		ua = DefaultUserAgent
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "*/*")

	resp, err := client.Do(req)
	if err != nil {
		return FetchResult{}, fmt.Errorf("subscription: GET %s: %w", sub.URL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return FetchResult{}, fmt.Errorf("subscription: GET %s: HTTP %d", sub.URL, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBundleBytes+1))
	if err != nil {
		return FetchResult{}, fmt.Errorf("subscription: read body: %w", err)
	}
	if len(body) > MaxBundleBytes {
		return FetchResult{}, fmt.Errorf("subscription: response > %d bytes (capped)", MaxBundleBytes)
	}

	return FetchResult{
		Body:                 body,
		SubscriptionUserinfo: resp.Header.Get("subscription-userinfo"),
		ProfileTitle:         resp.Header.Get("profile-title"),
		ContentType:          resp.Header.Get("Content-Type"),
	}, nil
}

// FetchAndApply runs Fetch and pipes the body into Registry.ApplyFetched.
// On Fetch error, the registry's LastError is updated and the previous
// snapshot is preserved.
func (f *Fetcher) FetchAndApply(ctx context.Context, r *Registry, id string) (FetchResult, []error, error) {
	sub, ok := r.Get(id)
	if !ok {
		return FetchResult{}, nil, fmt.Errorf("subscription: %q not found", id)
	}
	if id == ManualID {
		return FetchResult{}, nil, errors.New("subscription: cannot fetch 'manual'")
	}

	res, err := f.Fetch(ctx, sub)
	if err != nil {
		r.MarkFetchError(id, err)
		return FetchResult{}, nil, err
	}
	parseErrs, applyErr := r.ApplyFetched(id, res.Body)
	if applyErr != nil {
		return res, parseErrs, applyErr
	}
	return res, parseErrs, nil
}
