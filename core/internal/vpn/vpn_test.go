package vpn

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestKeyRoundtrip(t *testing.T) {
	priv, pub, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	encoded := priv.String()
	if len(encoded) != 44 { // 32 bytes base64 = 44 chars with =
		t.Errorf("private key string length = %d, want 44", len(encoded))
	}
	parsed, err := ParseKey(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if parsed != priv {
		t.Error("priv key roundtrip mismatch")
	}
	// Public derives consistently from private.
	derived := PublicFor(priv)
	if derived != pub {
		t.Error("PublicFor(priv) != generated pub")
	}
}

func TestParseKeyRejectsGarbage(t *testing.T) {
	for _, c := range []string{"", "short", "not base64!"} {
		if _, err := ParseKey(c); err == nil {
			t.Errorf("expected error for %q", c)
		}
	}
}

func TestAllocateAllowedIPSkipsServerAndUsed(t *testing.T) {
	cases := []struct {
		serverCIDR string
		used       []string
		want       string
	}{
		{"10.20.30.1/24", nil, "10.20.30.2/32"},
		{"10.20.30.1/24", []string{"10.20.30.2/32"}, "10.20.30.3/32"},
		{"10.20.30.1/24", []string{"10.20.30.2/32", "10.20.30.4/32"}, "10.20.30.3/32"},
		{"10.20.30.1/24", []string{"10.20.30.5/32"}, "10.20.30.2/32"},
	}
	for _, c := range cases {
		got, err := AllocateAllowedIP(c.serverCIDR, c.used)
		if err != nil {
			t.Fatalf("allocate(%v): %v", c, err)
		}
		if got != c.want {
			t.Errorf("allocate(server=%s, used=%v) = %s, want %s",
				c.serverCIDR, c.used, got, c.want)
		}
	}
}

func TestValidatePeerName(t *testing.T) {
	good := []string{"Phone", "anna-laptop", "Иван"} // last one is cyrillic
	for _, n := range good[:2] {
		if err := ValidatePeerName(n); err != nil {
			t.Errorf("name %q rejected: %v", n, err)
		}
	}
	// Cyrillic isn't in our character class — the validator is
	// ASCII-only because the name ends up in `# peer: ...` comment
	// of the wg conf and we want to keep that ASCII-clean. Document
	// this expectation here so the test fails loudly if we widen.
	if err := ValidatePeerName("Иван"); err == nil {
		t.Error("cyrillic name should fail (validator is ASCII-only)")
	}
	bad := []string{"", "  ", "x\nbad", strings.Repeat("a", 41)}
	for _, n := range bad {
		if err := ValidatePeerName(n); err == nil {
			t.Errorf("expected error for %q", n)
		}
	}
}

func TestRegistryBootstrapAndAddRemove(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "wg.yaml")
	r, err := Open(store)
	if err != nil {
		t.Fatal(err)
	}
	if r.Server().PrivateKey == (Key{}) {
		t.Error("bootstrap should have generated a server key")
	}
	if len(r.Peers()) != 0 {
		t.Errorf("fresh registry should have no peers, got %d", len(r.Peers()))
	}

	res, err := r.AddPeer("Phone", "kids")
	if err != nil {
		t.Fatal(err)
	}
	if res.Peer.AllowedIP != "10.20.30.2/32" {
		t.Errorf("first peer IP = %s", res.Peer.AllowedIP)
	}
	if res.PrivateKey == (Key{}) {
		t.Error("AddPeer should return a private key once")
	}
	if PublicFor(res.PrivateKey) != res.Peer.PublicKey {
		t.Error("returned private/public keys do not match each other")
	}

	// Reload from disk: the peer survives, the private key does not.
	r2, err := Open(store)
	if err != nil {
		t.Fatal(err)
	}
	peers := r2.Peers()
	if len(peers) != 1 {
		t.Fatalf("reload peers = %d", len(peers))
	}
	if peers[0].Name != "Phone" {
		t.Errorf("name after reload: %q", peers[0].Name)
	}
	if peers[0].ProfileID != "kids" {
		t.Errorf("profile id lost: %q", peers[0].ProfileID)
	}

	// Remove.
	if err := r2.RemovePeer(peers[0].ID); err != nil {
		t.Fatal(err)
	}
	if len(r2.Peers()) != 0 {
		t.Error("peer not removed")
	}
}

func TestRegistryLookupByAllowedIP(t *testing.T) {
	r, _ := Open(filepath.Join(t.TempDir(), "wg.yaml"))
	res, _ := r.AddPeer("Laptop", "")
	if got, ok := r.LookupByAllowedIP("10.20.30.2"); !ok || got.ID != res.Peer.ID {
		t.Errorf("LookupByAllowedIP: ok=%v got=%+v", ok, got)
	}
	if _, ok := r.LookupByAllowedIP("10.20.30.99"); ok {
		t.Error("unknown IP should not match")
	}
}

func TestRenderServerConfShape(t *testing.T) {
	priv, _, _ := GenerateKeyPair()
	s := ServerConfig{
		Enabled:       true,
		ListenPort:    51820,
		InterfaceCIDR: "10.20.30.1/24",
		PrivateKey:    priv,
	}
	pub1, _ := ParseKey(priv.String()) // round-trip a key
	_ = pub1
	peerPriv, peerPub, _ := GenerateKeyPair()
	_ = peerPriv
	peers := []Peer{{
		ID: "abcd1234", Name: "Phone",
		PublicKey: peerPub, AllowedIP: "10.20.30.2/32",
	}}
	conf := RenderServerConf(s, peers)
	for _, want := range []string{
		"[Interface]",
		"ListenPort = 51820",
		"Address = 10.20.30.1/24",
		"PrivateKey = " + priv.String(),
		"# peer: Phone (abcd1234)",
		"[Peer]",
		"PublicKey = " + peerPub.String(),
		"AllowedIPs = 10.20.30.2/32",
		"SaveConfig = false",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("server conf missing %q\n--- conf ---\n%s", want, conf)
		}
	}
}

func TestRenderClientConfFullVsSplit(t *testing.T) {
	priv, _, _ := GenerateKeyPair()
	srv := ServerConfig{
		ListenPort:    51820,
		InterfaceCIDR: "10.20.30.1/24",
		EndpointHost:  "home.example.com",
		PrivateKey:    priv,
	}
	srvPub := PublicFor(priv)
	pPriv, pPub, _ := GenerateKeyPair()
	peer := Peer{
		ID: "deadbeef", Name: "Phone",
		PublicKey: pPub, AllowedIP: "10.20.30.5/32",
	}

	full := RenderClientConf(srv, srvPub, peer, pPriv, ClientConfigOptions{
		FullTunnel:   true,
		LANRouteCIDR: "192.168.42.0/24",
		DNSAddress:   "192.168.42.1",
	})
	if !strings.Contains(full, "AllowedIPs = 0.0.0.0/0, ::/0") {
		t.Errorf("full-tunnel missing 0.0.0.0/0:\n%s", full)
	}
	if !strings.Contains(full, "DNS = 192.168.42.1") {
		t.Error("full-tunnel missing DNS")
	}
	if !strings.Contains(full, "Endpoint = home.example.com:51820") {
		t.Error("full-tunnel missing endpoint")
	}
	if !strings.Contains(full, "PersistentKeepalive = 25") {
		t.Error("full-tunnel missing keepalive")
	}

	split := RenderClientConf(srv, srvPub, peer, pPriv, ClientConfigOptions{
		FullTunnel:   false,
		LANRouteCIDR: "192.168.42.0/24",
		DNSAddress:   "192.168.42.1",
	})
	if strings.Contains(split, "0.0.0.0/0") {
		t.Errorf("split-tunnel must NOT contain 0.0.0.0/0:\n%s", split)
	}
	if !strings.Contains(split, "AllowedIPs = 10.20.30.1/24, 192.168.42.0/24") {
		t.Errorf("split-tunnel allowed IPs wrong:\n%s", split)
	}
}
