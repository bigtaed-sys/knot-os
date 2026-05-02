package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Sealer wraps and unwraps individual secret strings (Wi-Fi PSKs).
// The config package depends only on this two-method interface so
// secrets/ can change its on-disk encoding without dragging the
// config package along.
//
// The contract:
//   - Wrap("") MUST return ("", nil) — empty input is empty output,
//     so a missing PSK doesn't accidentally become an encrypted
//     empty string.
//   - Unwrap on a plaintext (legacy) string MUST return it unchanged
//     so a v0.1/v0.2 config still loads. The next save then
//     re-emits the value encrypted; that's the migration path.
type Sealer interface {
	Wrap(plaintext string) (string, error)
	Unwrap(stored string) (string, error)
}

// Load reads and parses the YAML config at path. Wraps LoadWith with
// a nil sealer for callers that don't need at-rest encryption (tests,
// dev mode without a key derived yet).
func Load(path string) (Config, error) {
	return LoadWith(path, nil)
}

// LoadWith reads and parses the YAML config at path, decrypting any
// `enc:v1:` secrets through the supplied Sealer. A nil Sealer is
// accepted; in that case encrypted scalars are returned to the
// caller verbatim (which they typically don't want — Validate will
// flag a Wi-Fi PSK that looks like an encrypted blob as malformed).
//
// If the file does not exist, Load returns the Default config and a
// nil error — first-boot uses the default config.
func LoadWith(path string, s Sealer) (Config, error) {
	cfg, _, err := loadWith(path, s)
	return cfg, err
}

// LoadWithMigration is LoadWith plus a "needsMigration" flag that
// reports whether the on-disk file contained at least one
// secret-bearing field in legacy cleartext form. Callers (typically
// main.go) use it to trigger a one-time re-save that encrypts those
// values without changing anything else.
func LoadWithMigration(path string, s Sealer) (Config, bool, error) {
	return loadWith(path, s)
}

func loadWith(path string, s Sealer) (Config, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), false, nil
	}
	if err != nil {
		return Config{}, false, fmt.Errorf("read %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, false, fmt.Errorf("parse %s: %w", path, err)
	}

	needsMigration := false
	if s != nil {
		needsMigration = cfg.hasCleartextSecrets()
		if err := cfg.unwrapSecrets(s); err != nil {
			return Config{}, false, fmt.Errorf("unwrap %s: %w", path, err)
		}
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, false, fmt.Errorf("validate %s: %w", path, err)
	}
	return cfg, needsMigration, nil
}

// Save writes cfg to path atomically without secret-wrapping. Kept
// for tests and callers that explicitly hold cleartext.
func Save(path string, cfg Config) error {
	return SaveWith(path, cfg, nil)
}

// SaveWith writes cfg to path atomically, encrypting Wi-Fi PSKs
// through the supplied Sealer first if non-nil. The cfg passed in
// is NOT mutated — wrap happens on a deep copy of the secret-bearing
// fields so the in-memory state stays cleartext.
//
// Atomic via temp+rename so a crash mid-save can't brick boot with
// a half-written file.
func SaveWith(path string, cfg Config, s Sealer) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	out := cfg
	if s != nil {
		// Deep-copy the pointer-bearing sub-structs so wrap doesn't
		// mutate the caller's in-memory state.
		if cfg.Network.Uplink != nil {
			u := *cfg.Network.Uplink
			out.Network.Uplink = &u
		}
		if cfg.Network.AP != nil {
			ap := *cfg.Network.AP
			out.Network.AP = &ap
		}
		if err := out.wrapSecrets(s); err != nil {
			return fmt.Errorf("wrap: %w", err)
		}
	}

	data, err := yaml.Marshal(&out)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".config-*.yaml.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()

	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("fsync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("rename %s -> %s: %w", tmpName, path, err)
	}
	return nil
}
