package secrets

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/hkdf"
)

// Default file locations on a real Pi OS Lite system.
const (
	DefaultSeedPath      = "/boot/firmware/knot-key-seed"
	DefaultMachineIDPath = "/etc/machine-id"
)

// SeedOptions configures LoadOrCreateKey.
type SeedOptions struct {
	// SeedPath is the random 32-byte file. Generated on first run.
	// Lives on the FAT boot partition so a user can recover it via
	// SD-card access if the daemon is broken.
	SeedPath string
	// MachineIDPath is the systemd machine-id (32 hex chars). Mixed
	// into the wrap key so a stolen seed alone doesn't unwrap.
	// Empty => skip the mix (dev / non-Linux).
	MachineIDPath string
}

// LoadOrCreateKey returns the wrap key, creating the seed file if
// it doesn't exist yet. The derived key is HKDF-SHA256 over
// (seed || machineID), domain-separated by the constant info.
func LoadOrCreateKey(opts SeedOptions) ([]byte, error) {
	if opts.SeedPath == "" {
		return nil, errors.New("secrets: SeedPath is required")
	}

	seed, err := loadOrCreateSeed(opts.SeedPath)
	if err != nil {
		return nil, fmt.Errorf("seed: %w", err)
	}

	var ikm []byte
	ikm = append(ikm, seed...)

	if opts.MachineIDPath != "" {
		mid, err := readMachineID(opts.MachineIDPath)
		if err != nil {
			// On a freshly-built dev image, /etc/machine-id may not
			// exist yet. Soft-fail rather than refusing to start —
			// without the mix the key is still 32 bytes of random
			// from the seed, which is fine for the dev case.
			if !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("machine-id: %w", err)
			}
		} else {
			ikm = append(ikm, mid...)
		}
	}

	r := hkdf.New(sha256.New, ikm, nil, []byte("knot-secrets-v1"))
	out := make([]byte, KeyLen)
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, fmt.Errorf("hkdf: %w", err)
	}
	return out, nil
}

func loadOrCreateSeed(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		if len(b) < 32 {
			return nil, fmt.Errorf("%s: too short (%d bytes)", path, len(b))
		}
		// Use only the first 32 bytes; ignore any trailing newline.
		return b[:32], nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	// First run: generate.
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	seed := make([]byte, 32)
	if _, err := rand.Read(seed); err != nil {
		return nil, fmt.Errorf("rand: %w", err)
	}
	if err := writeSeed(path, seed); err != nil {
		return nil, err
	}
	return seed, nil
}

func writeSeed(path string, seed []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".seed-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(seed); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

func readMachineID(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := strings.TrimSpace(string(b))
	if len(s) == 0 {
		return nil, errors.New("empty machine-id")
	}
	return []byte(s), nil
}
