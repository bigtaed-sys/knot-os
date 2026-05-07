package subscription

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetcherGetsAndApplies(t *testing.T) {
	uris := strings.Join([]string{
		"vless://uuid-1@a.example.com:443?security=reality&pbk=k&sni=x.com#A",
		"vless://uuid-2@b.example.com:443?security=reality&pbk=k&sni=x.com#B",
	}, "\n")
	body := base64.StdEncoding.EncodeToString([]byte(uris))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if got := req.Header.Get("User-Agent"); got != DefaultUserAgent {
			t.Errorf("UA=%q, want %q", got, DefaultUserAgent)
		}
		w.Header().Set("subscription-userinfo", "upload=0; download=1234567; total=10000000000; expire=1764576000")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	r := newRegInTempDir(t)
	added, _ := r.Add(Subscription{DisplayName: "Foo", URL: srv.URL})

	f := NewFetcher()
	res, parseErrs, err := f.FetchAndApply(context.Background(), r, added.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(parseErrs) != 0 {
		t.Errorf("parseErrs: %v", parseErrs)
	}
	if !strings.Contains(res.SubscriptionUserinfo, "upload=") {
		t.Errorf("userinfo header lost: %q", res.SubscriptionUserinfo)
	}
	got, _ := r.Get(added.ID)
	if len(got.Servers) != 2 {
		t.Errorf("servers=%d, want 2", len(got.Servers))
	}
}

func TestFetcherUserAgentOverride(t *testing.T) {
	got := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		got = req.Header.Get("User-Agent")
		_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(
			"vless://u@a:443?security=reality&pbk=k&sni=x.com#A"))))
	}))
	defer srv.Close()

	r := newRegInTempDir(t)
	added, _ := r.Add(Subscription{
		DisplayName: "Foo", URL: srv.URL, UserAgent: "v2rayN/6.42",
	})
	f := NewFetcher()
	if _, _, err := f.FetchAndApply(context.Background(), r, added.ID); err != nil {
		t.Fatal(err)
	}
	if got != "v2rayN/6.42" {
		t.Errorf("UA override: got %q", got)
	}
}

func TestFetcherErrorMarksRegistry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer srv.Close()

	r := newRegInTempDir(t)
	added, _ := r.Add(Subscription{DisplayName: "Foo", URL: srv.URL})

	// First, give it a successful snapshot to compare against.
	body := base64.StdEncoding.EncodeToString([]byte(
		"vless://uuid@a:443?security=reality&pbk=k&sni=x.com#A"))
	if _, err := r.ApplyFetched(added.ID, []byte(body)); err != nil {
		t.Fatal(err)
	}

	f := NewFetcher()
	if _, _, err := f.FetchAndApply(context.Background(), r, added.ID); err == nil {
		t.Fatal("expected fetch to fail with 500")
	}

	got, _ := r.Get(added.ID)
	if got.LastError == "" {
		t.Error("LastError not set on fetch failure")
	}
	if len(got.Servers) != 1 {
		t.Errorf("snapshot lost on fetch failure: %d servers", len(got.Servers))
	}
}

func TestFetcherRejectsNonHTTPURL(t *testing.T) {
	r := newRegInTempDir(t)
	r.subs["bad"] = &Subscription{ID: "bad", URL: "ftp://example.com/sub"}
	f := NewFetcher()
	if _, _, err := f.FetchAndApply(context.Background(), r, "bad"); err == nil {
		t.Error("expected error on non-http URL")
	}
}

func TestFetcherRejectsManual(t *testing.T) {
	r := newRegInTempDir(t)
	f := NewFetcher()
	if _, _, err := f.FetchAndApply(context.Background(), r, ManualID); err == nil {
		t.Error("expected error fetching manual")
	}
}

func TestFetcherRejectsTooLargeBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Write MaxBundleBytes+10 of base64 content — sneaks past
		// the looksLikeShareBundle gate by being raw bytes.
		junk := strings.Repeat("a", MaxBundleBytes+10)
		_, _ = w.Write([]byte(junk))
	}))
	defer srv.Close()

	r := newRegInTempDir(t)
	added, _ := r.Add(Subscription{DisplayName: "Big", URL: srv.URL})
	f := NewFetcher()
	if _, _, err := f.FetchAndApply(context.Background(), r, added.ID); err == nil {
		t.Error("expected error on oversize body")
	}
}
