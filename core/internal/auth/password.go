// Package auth implements local admin authentication for knotd: bcrypt
// password hashing, in-memory session tokens, and an HTTP middleware that
// gates protected endpoints.
package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// MinPasswordLength is the lower bound enforced by HashPassword. Tuned for
// a router admin password that the user types into a UI on a phone — too
// short is a real risk, too long is friction.
const MinPasswordLength = 8

// bcryptCost is the work factor used for hashing. 12 is a reasonable
// 2025-era default that still completes in well under a second on a
// Raspberry Pi Zero 2W.
const bcryptCost = 12

// HashPassword returns a bcrypt hash suitable for persisting in
// config.yaml. It enforces MinPasswordLength and surfaces bcrypt errors
// (e.g. password too long — bcrypt itself caps at 72 bytes).
func HashPassword(password string) (string, error) {
	if len(password) < MinPasswordLength {
		return "", fmt.Errorf("password must be at least %d characters", MinPasswordLength)
	}
	if len(password) > 72 {
		return "", fmt.Errorf("password must be at most 72 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("bcrypt: %w", err)
	}
	return string(hash), nil
}

// CheckPassword reports whether candidate matches hash. A nil error means
// the password is correct. ErrMismatchedHashAndPassword is returned for
// wrong passwords; other errors indicate a malformed hash.
func CheckPassword(hash, candidate string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(candidate))
}

// generateToken returns a 32-byte random opaque session token, base64-url
// encoded (43 characters, no padding). Generated values are never
// predictable from each other.
func generateToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
