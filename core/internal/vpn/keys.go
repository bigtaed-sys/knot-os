// Package vpn implements knotd's built-in WireGuard server: a
// road-warrior endpoint clients reach from outside the LAN. The
// package is platform-neutral — it generates keys, renders
// wg-quick-format configs, and tracks peer state. The actual
// interface lifecycle (`ip link add wg0 type wireguard`,
// `wg syncconf`, NAT rules) lives in core/internal/network/linux
// behind the Linux build tag.
package vpn

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"

	"golang.org/x/crypto/curve25519"
)

// KeySize is the byte length of both private and public keys.
const KeySize = 32

// Key is a 32-byte WireGuard key in raw binary form. JSON / YAML
// representation is base64 (the canonical form `wg`'s userspace
// tools use).
type Key [KeySize]byte

// String returns the base64 encoding used by wg / wg-quick.
func (k Key) String() string {
	return base64.StdEncoding.EncodeToString(k[:])
}

// MarshalText / UnmarshalText make Key transparently round-trip
// through YAML and JSON as base64 strings.
func (k Key) MarshalText() ([]byte, error) {
	return []byte(k.String()), nil
}

func (k *Key) UnmarshalText(b []byte) error {
	parsed, err := ParseKey(string(b))
	if err != nil {
		return err
	}
	*k = parsed
	return nil
}

// ParseKey decodes a base64 string (44 chars, "=" padded) into a Key.
func ParseKey(s string) (Key, error) {
	if s == "" {
		return Key{}, errors.New("vpn: empty key")
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return Key{}, fmt.Errorf("vpn: parse key: %w", err)
	}
	if len(raw) != KeySize {
		return Key{}, fmt.Errorf("vpn: key length %d, want %d", len(raw), KeySize)
	}
	var k Key
	copy(k[:], raw)
	return k, nil
}

// GenerateKeyPair returns (privateKey, publicKey).
//
// WireGuard uses Curve25519. The private key is 32 random bytes
// with the standard "clamping" applied (clears the lowest 3 bits
// and sets the second-highest bit), then the public key is the
// scalar-mult of the basepoint. Same construction every WG
// implementation uses; matches `wg genkey | wg pubkey` exactly.
func GenerateKeyPair() (priv, pub Key, err error) {
	if _, err = rand.Read(priv[:]); err != nil {
		return Key{}, Key{}, fmt.Errorf("vpn: rand: %w", err)
	}
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64

	var privArr, pubArr [32]byte
	copy(privArr[:], priv[:])
	curve25519.ScalarBaseMult(&pubArr, &privArr)
	copy(pub[:], pubArr[:])
	return priv, pub, nil
}

// PublicFor derives the public half of priv. Useful when we have
// only the private key on disk and need to recompute the matching
// public for the conf file.
func PublicFor(priv Key) Key {
	var privArr, pubArr [32]byte
	copy(privArr[:], priv[:])
	curve25519.ScalarBaseMult(&pubArr, &privArr)
	var out Key
	copy(out[:], pubArr[:])
	return out
}
