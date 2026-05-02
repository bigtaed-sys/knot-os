// Package secrets owns the at-rest encryption of the small set of
// sensitive strings KnotOS persists in /etc/knot/config.yaml — Wi-Fi
// PSKs, primarily. The encryption envelope is AES-256-GCM with a
// random per-secret nonce; the wrap key is derived once at boot from
// a seed file (random 32 bytes, generated on first run) mixed with
// the machine-id (regenerated on every fresh Pi OS Lite flash).
//
// Threat model:
//
//   - In scope: someone with **only** the SD card. Without the
//     machine-id from the running system they can't unwrap.
//     Re-flashing the OS produces a new machine-id, so cloning the
//     boot partition alone is not enough.
//   - Out of scope: an attacker with root on the running daemon,
//     or with both the SD card AND the machine-id (e.g. a memory
//     dump). Per-CPU TPM-backed keys are a v0.5 problem.
//
// On-disk format for an encrypted scalar is the prefix `enc:v1:`
// followed by URL-safe base64(nonce|ciphertext). Plain strings
// without the prefix are treated as legacy plaintext: Unwrap
// returns them as-is, and the next config save will encrypt them.
// This is the migration path from v0.1/v0.2 configs.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// Prefix is the sentinel that marks an encrypted scalar. Public so
// the config package can use it to detect already-encrypted values.
const Prefix = "enc:v1:"

// KeyLen is the wrap-key length (32 = AES-256).
const KeyLen = 32

// Sealer wraps and unwraps secrets with an AES-256-GCM key.
// Construct via New; safe for concurrent use.
type Sealer struct {
	gcm cipher.AEAD
}

// New constructs a Sealer from a 32-byte key.
func New(key []byte) (*Sealer, error) {
	if len(key) != KeyLen {
		return nil, fmt.Errorf("secrets: key must be %d bytes, got %d", KeyLen, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	return &Sealer{gcm: gcm}, nil
}

// Wrap encrypts a plaintext string, returning the on-disk form
// (prefix + base64). Empty input is returned unchanged so a missing
// PSK doesn't accidentally become an encrypted empty string.
func (s *Sealer) Wrap(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	if IsEncrypted(plaintext) {
		// Already encrypted (e.g. round-tripped through a save that
		// didn't decrypt first). Pass through.
		return plaintext, nil
	}
	nonce := make([]byte, s.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("nonce: %w", err)
	}
	ct := s.gcm.Seal(nil, nonce, []byte(plaintext), nil)
	out := make([]byte, 0, len(nonce)+len(ct))
	out = append(out, nonce...)
	out = append(out, ct...)
	return Prefix + base64.RawURLEncoding.EncodeToString(out), nil
}

// Unwrap decrypts the on-disk form back to plaintext. Strings without
// the prefix are returned as-is (legacy migration).
func (s *Sealer) Unwrap(stored string) (string, error) {
	if !IsEncrypted(stored) {
		return stored, nil
	}
	body := strings.TrimPrefix(stored, Prefix)
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	ns := s.gcm.NonceSize()
	if len(raw) < ns {
		return "", errors.New("ciphertext too short")
	}
	nonce, ct := raw[:ns], raw[ns:]
	pt, err := s.gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("open: %w", err)
	}
	return string(pt), nil
}

// IsEncrypted is a cheap check on the prefix. Useful for callers
// that want to decide whether a value has already been wrapped.
func IsEncrypted(s string) bool {
	return strings.HasPrefix(s, Prefix)
}
