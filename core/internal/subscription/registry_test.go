package subscription

import (
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"
)

func newRegInTempDir(t *testing.T) *Registry {
	t.Helper()
	return NewRegistry(filepath.Join(t.TempDir(), "subscriptions.yaml"))
}

func TestNewRegistryHasManual(t *testing.T) {
	r := newRegInTempDir(t)
	subs := r.List()
	if len(subs) != 1 {
		t.Fatalf("got %d subs, want 1", len(subs))
	}
	if subs[0].ID != ManualID {
		t.Errorf("first sub id=%q, want %q", subs[0].ID, ManualID)
	}
}

func TestAddRemoveSubscription(t *testing.T) {
	r := newRegInTempDir(t)
	added, err := r.Add(Subscription{
		DisplayName: "My Provider",
		URL:         "https://provider.example.com/sub/abc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if added.ID != "my-provider" {
		t.Errorf("auto-slug: got %q, want my-provider", added.ID)
	}
	if len(r.List()) != 2 {
		t.Errorf("List len after Add: %d", len(r.List()))
	}

	if err := r.Remove("my-provider"); err != nil {
		t.Fatal(err)
	}
	if len(r.List()) != 1 {
		t.Errorf("List len after Remove: %d", len(r.List()))
	}

	if err := r.Remove(ManualID); err == nil {
		t.Error("expected error removing manual subscription")
	}
}

func TestAddRejectsReservedID(t *testing.T) {
	r := newRegInTempDir(t)
	_, err := r.Add(Subscription{
		ID: ManualID, DisplayName: "x", URL: "https://x",
	})
	if err == nil {
		t.Error("expected error for ID=manual")
	}
}

func TestAddRejectsDuplicate(t *testing.T) {
	r := newRegInTempDir(t)
	_, _ = r.Add(Subscription{DisplayName: "Foo", URL: "https://1"})
	_, err := r.Add(Subscription{DisplayName: "Foo", URL: "https://2"})
	if err == nil {
		t.Error("expected error on duplicate ID")
	}
}

func TestUpdateSubscription(t *testing.T) {
	r := newRegInTempDir(t)
	added, _ := r.Add(Subscription{DisplayName: "Foo", URL: "https://old"})

	updated, err := r.Update(added.ID, Subscription{
		DisplayName: "Bar", URL: "https://new",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.DisplayName != "Bar" {
		t.Errorf("DisplayName=%q", updated.DisplayName)
	}
	if updated.URL != "https://new" {
		t.Errorf("URL=%q", updated.URL)
	}
}

func TestApplyFetchedParsesAndSnapshots(t *testing.T) {
	r := newRegInTempDir(t)
	added, _ := r.Add(Subscription{DisplayName: "Foo", URL: "https://foo"})

	uris := []string{
		"vless://uuid-1@a.example.com:443?security=reality&pbk=k&sni=x.com#A",
		"vless://uuid-2@b.example.com:443?security=reality&pbk=k&sni=x.com#B",
		"trojan://pw@c.example.com:443?sni=c.example.com#C",
	}
	body := []byte(base64.StdEncoding.EncodeToString([]byte(strings.Join(uris, "\n"))))

	parseErrs, err := r.ApplyFetched(added.ID, body)
	if err != nil {
		t.Fatalf("ApplyFetched: %v", err)
	}
	if len(parseErrs) != 0 {
		t.Errorf("parseErrs: %v", parseErrs)
	}

	got, _ := r.Get(added.ID)
	if len(got.Servers) != 3 {
		t.Fatalf("got %d servers, want 3", len(got.Servers))
	}
	if got.LastError != "" {
		t.Errorf("LastError=%q", got.LastError)
	}
	if got.LastFetched.IsZero() {
		t.Error("LastFetched zero")
	}
	if got.Servers[0].DisplayName != "A" {
		t.Errorf("first server name=%q", got.Servers[0].DisplayName)
	}
	if got.Servers[0].URI == "" {
		t.Error("URI not preserved")
	}
}

func TestApplyFetchedStableServerIDs(t *testing.T) {
	r := newRegInTempDir(t)
	added, _ := r.Add(Subscription{DisplayName: "Foo", URL: "https://foo"})

	body := func(uris []string) []byte {
		return []byte(base64.StdEncoding.EncodeToString([]byte(strings.Join(uris, "\n"))))
	}

	urisA := []string{
		"vless://uuid-1@a.example.com:443?security=reality&pbk=k&sni=x.com#A",
		"vless://uuid-2@b.example.com:443?security=reality&pbk=k&sni=x.com#B",
	}
	if _, err := r.ApplyFetched(added.ID, body(urisA)); err != nil {
		t.Fatal(err)
	}
	gotA, _ := r.Get(added.ID)
	idA := gotA.Servers[0].ID
	idB := gotA.Servers[1].ID

	// Re-fetch with reversed order; IDs should follow the URI, not
	// the slot index.
	urisB := []string{urisA[1], urisA[0]}
	if _, err := r.ApplyFetched(added.ID, body(urisB)); err != nil {
		t.Fatal(err)
	}
	gotB, _ := r.Get(added.ID)
	if gotB.Servers[0].ID != idB || gotB.Servers[1].ID != idA {
		t.Errorf("IDs not stable across reorder: %v vs %v", gotA.Servers, gotB.Servers)
	}
}

func TestApplyFetchedKeepsSnapshotOnEmptyBundle(t *testing.T) {
	r := newRegInTempDir(t)
	added, _ := r.Add(Subscription{DisplayName: "Foo", URL: "https://foo"})

	body := []byte(base64.StdEncoding.EncodeToString([]byte(
		"vless://uuid-1@a:443?security=reality&pbk=k&sni=x.com#A")))
	if _, err := r.ApplyFetched(added.ID, body); err != nil {
		t.Fatal(err)
	}

	// Now feed garbage. The previous good snapshot should survive.
	if _, err := r.ApplyFetched(added.ID, []byte("garbage no URIs")); err == nil {
		t.Error("expected error on empty bundle")
	}
	got, _ := r.Get(added.ID)
	if len(got.Servers) != 1 {
		t.Errorf("snapshot lost: %d servers", len(got.Servers))
	}
	if got.LastError == "" {
		t.Error("LastError should be set after bad fetch")
	}
}

func TestAddManualURIDeduplicates(t *testing.T) {
	r := newRegInTempDir(t)
	uri := "vless://uuid@h:443?security=reality&pbk=k&sni=x.com#one"

	srv1, err := r.AddManualURI(uri)
	if err != nil {
		t.Fatal(err)
	}
	srv2, _ := r.AddManualURI(uri)
	if srv1.ID != srv2.ID {
		t.Errorf("duplicate URI yielded different IDs: %s vs %s", srv1.ID, srv2.ID)
	}

	got, _ := r.Get(ManualID)
	if len(got.Servers) != 1 {
		t.Errorf("manual servers: %d, want 1 (dedupe)", len(got.Servers))
	}
}

func TestRemoveManualServer(t *testing.T) {
	r := newRegInTempDir(t)
	srv, _ := r.AddManualURI("vless://uuid@h:443?security=reality&pbk=k&sni=x.com#one")
	if err := r.RemoveManualServer(srv.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := r.Get(ManualID)
	if len(got.Servers) != 0 {
		t.Errorf("expected empty manual list after removal")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "subscriptions.yaml")

	r1 := NewRegistry(storePath)
	added, _ := r1.Add(Subscription{DisplayName: "Foo", URL: "https://foo"})
	body := []byte(base64.StdEncoding.EncodeToString([]byte(
		"vless://uuid@a.example.com:443?security=reality&pbk=k&sni=x.com#A")))
	if _, err := r1.ApplyFetched(added.ID, body); err != nil {
		t.Fatal(err)
	}
	if _, err := r1.AddManualURI("trojan://pw@h:443?sni=h#manual1"); err != nil {
		t.Fatal(err)
	}
	if err := r1.Save(); err != nil {
		t.Fatal(err)
	}

	r2 := NewRegistry(storePath)
	if err := r2.Load(); err != nil {
		t.Fatal(err)
	}

	subs := r2.List()
	if len(subs) != 2 {
		t.Fatalf("got %d subs, want 2 (manual + foo)", len(subs))
	}
	got, _ := r2.Get(added.ID)
	if len(got.Servers) != 1 {
		t.Errorf("foo servers: %d", len(got.Servers))
	}
	// Outbound should be re-parsed from URI on load.
	if got.Servers[0].Outbound.UUID != "uuid" {
		t.Errorf("outbound not rebuilt: %+v", got.Servers[0].Outbound)
	}
	manual, _ := r2.Get(ManualID)
	if len(manual.Servers) != 1 {
		t.Errorf("manual lost: %d", len(manual.Servers))
	}
}

func TestAllOutboundsTagsServers(t *testing.T) {
	r := newRegInTempDir(t)
	added, _ := r.Add(Subscription{DisplayName: "Foo", URL: "https://foo"})
	body := []byte(base64.StdEncoding.EncodeToString([]byte(
		"vless://uuid@a:443?security=reality&pbk=k&sni=x.com#alpha\n" +
			"vless://uuid@b:443?security=reality&pbk=k&sni=x.com#bravo")))
	if _, err := r.ApplyFetched(added.ID, body); err != nil {
		t.Fatal(err)
	}
	_, _ = r.AddManualURI("trojan://pw@h:443?sni=h#m1")

	outs := r.AllOutbounds()
	if len(outs) != 3 {
		t.Fatalf("got %d outbounds, want 3", len(outs))
	}
	for _, o := range outs {
		if !strings.Contains(o.Tag, ":") {
			t.Errorf("tag missing namespace: %q", o.Tag)
		}
	}
}

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Hello World":        "hello-world",
		"  трим  ":           "sub", // non-ASCII collapses to empty → fallback
		"My-Provider!":       "my-provider",
		"":                   "sub",
		strings.Repeat("a", 100): strings.Repeat("a", 32),
	}
	for in, want := range cases {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidSubID(t *testing.T) {
	good := []string{"foo", "foo-bar", "abc123", "x"}
	bad := []string{"", "-foo", "Foo", "foo bar", "foo_bar", strings.Repeat("a", 33)}
	for _, s := range good {
		if !validSubID(s) {
			t.Errorf("validSubID(%q)=false, want true", s)
		}
	}
	for _, s := range bad {
		if validSubID(s) {
			t.Errorf("validSubID(%q)=true, want false", s)
		}
	}
}
