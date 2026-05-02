package secrets

import (
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
)

func mustKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, KeyLen)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return k
}

func TestWrapUnwrapRoundtrip(t *testing.T) {
	s, err := New(mustKey(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, pt := range []string{"hunter2", "пароль с пробелами", "p@$$ word!@#"} {
		ct, err := s.Wrap(pt)
		if err != nil {
			t.Fatalf("Wrap %q: %v", pt, err)
		}
		if !IsEncrypted(ct) {
			t.Errorf("ct missing prefix: %q", ct)
		}
		got, err := s.Unwrap(ct)
		if err != nil {
			t.Fatalf("Unwrap %q: %v", ct, err)
		}
		if got != pt {
			t.Errorf("roundtrip: got %q want %q", got, pt)
		}
	}
}

func TestWrapEmptyIsEmpty(t *testing.T) {
	s, _ := New(mustKey(t))
	got, err := s.Wrap("")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("empty wrap = %q", got)
	}
}

func TestUnwrapPlaintextPassthrough(t *testing.T) {
	s, _ := New(mustKey(t))
	for _, pt := range []string{"", "legacy-password"} {
		got, err := s.Unwrap(pt)
		if err != nil {
			t.Fatalf("Unwrap legacy %q: %v", pt, err)
		}
		if got != pt {
			t.Errorf("legacy %q -> %q", pt, got)
		}
	}
}

func TestWrapSecondWrapIsNoOp(t *testing.T) {
	s, _ := New(mustKey(t))
	once, _ := s.Wrap("password")
	twice, err := s.Wrap(once)
	if err != nil {
		t.Fatal(err)
	}
	if once != twice {
		t.Errorf("double wrap mutated value")
	}
}

func TestUnwrapWrongKeyFails(t *testing.T) {
	a, _ := New(mustKey(t))
	b, _ := New(mustKey(t))
	ct, _ := a.Wrap("secret")
	if _, err := b.Unwrap(ct); err == nil {
		t.Error("expected unwrap with wrong key to fail")
	}
}

func TestWrapNoncesDiffer(t *testing.T) {
	s, _ := New(mustKey(t))
	a, _ := s.Wrap("same")
	b, _ := s.Wrap("same")
	if a == b {
		t.Errorf("two wraps of same plaintext produced identical ciphertext (nonce reuse)")
	}
}

func TestLoadOrCreateKeyGeneratesAndReuses(t *testing.T) {
	dir := t.TempDir()
	seed := filepath.Join(dir, "seed")

	first, err := LoadOrCreateKey(SeedOptions{SeedPath: seed})
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreateKey(SeedOptions{SeedPath: seed})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Error("key not stable across calls")
	}
	if len(first) != KeyLen {
		t.Errorf("key length %d", len(first))
	}
}

func TestMachineIDChangesKey(t *testing.T) {
	dir := t.TempDir()
	seed := filepath.Join(dir, "seed")
	mid1 := filepath.Join(dir, "machine-id-1")
	mid2 := filepath.Join(dir, "machine-id-2")
	if err := os.WriteFile(mid1, []byte("abcdef0123456789abcdef0123456789\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mid2, []byte("ffffffffffffffffffffffffffffffff\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a, err := LoadOrCreateKey(SeedOptions{SeedPath: seed, MachineIDPath: mid1})
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreateKey(SeedOptions{SeedPath: seed, MachineIDPath: mid2})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Error("different machine-ids produced identical keys")
	}
	if len(a) != KeyLen {
		t.Errorf("key length %d", len(a))
	}
}
