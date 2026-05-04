package update

import (
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRescueGeneratesOnFirstRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rescue.json")
	r, err := LoadOrCreateRescue(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.PublicKey()) != ed25519.PublicKeySize {
		t.Errorf("public key size = %d", len(r.PublicKey()))
	}
	if !r.HasPrivateKey() {
		t.Error("private key should be present immediately after generation")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected rescue file to be created: %v", err)
	}
}

func TestRescuePersistsAcrossReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rescue.json")
	first, err := LoadOrCreateRescue(path)
	if err != nil {
		t.Fatal(err)
	}
	pub := first.PublicKey()

	second, err := LoadOrCreateRescue(path)
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.PublicKey(pub).Equal(second.PublicKey()) {
		t.Error("public key changed across reload (must be stable)")
	}
	if !second.HasPrivateKey() {
		t.Error("private key should still be present pre-reveal")
	}
}

func TestRescueRevealOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rescue.json")
	r, err := LoadOrCreateRescue(path)
	if err != nil {
		t.Fatal(err)
	}
	priv, err := r.RevealPrivateOnce()
	if err != nil {
		t.Fatal(err)
	}
	if len(priv) != ed25519.PrivateKeySize*2 { // hex doubles size
		t.Errorf("private key hex length = %d", len(priv))
	}
	if r.HasPrivateKey() {
		t.Error("HasPrivateKey should be false after reveal")
	}
	// Second call must fail with the sentinel.
	if _, err := r.RevealPrivateOnce(); !errors.Is(err, ErrAlreadyRevealed) {
		t.Errorf("second reveal: want ErrAlreadyRevealed, got %v", err)
	}

	// And on disk: revealed=true, no private bytes.
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), `"revealed": true`) {
		t.Errorf("expected revealed=true on disk:\n%s", body)
	}
	if strings.Contains(string(body), `"private"`) {
		t.Errorf("private payload should be absent on disk after reveal:\n%s", body)
	}

	// Reload after reveal: still gives correct public key, no private.
	reloaded, err := LoadOrCreateRescue(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.HasPrivateKey() {
		t.Error("reload after reveal should leave HasPrivateKey=false")
	}
	if !ed25519.PublicKey(reloaded.PublicKey()).Equal(r.PublicKey()) {
		t.Error("public key shouldn't change after reveal")
	}
}

func TestRescueSignVerifyRoundtrip(t *testing.T) {
	// End-to-end: generate rescue, RevealPrivateOnce, parse the hex
	// back into ed25519.PrivateKey, sign a payload, verify with the
	// rescue public key. This is the contract a self-build user
	// depends on.
	path := filepath.Join(t.TempDir(), "rescue.json")
	r, err := LoadOrCreateRescue(path)
	if err != nil {
		t.Fatal(err)
	}
	pub := r.PublicKey()

	privHex, err := r.RevealPrivateOnce()
	if err != nil {
		t.Fatal(err)
	}
	privBytes := mustHex(t, privHex)
	priv := ed25519.PrivateKey(privBytes)

	payload := []byte("self-built knotd binary")
	sig := ed25519.Sign(priv, payload)
	if !ed25519.Verify(pub, payload, sig) {
		t.Error("signature did not verify under rescue public key")
	}
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b := make([]byte, len(s)/2)
	for i := 0; i < len(b); i++ {
		hi, lo := hexDigit(t, s[i*2]), hexDigit(t, s[i*2+1])
		b[i] = byte(hi<<4 | lo)
	}
	return b
}

func hexDigit(t *testing.T, c byte) int {
	t.Helper()
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	t.Fatalf("bad hex digit %q", c)
	return 0
}
