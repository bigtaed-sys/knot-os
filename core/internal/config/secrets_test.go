package config

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knot-os/knot-os/core/internal/secrets"
)

func mustSealer(t *testing.T) *secrets.Sealer {
	t.Helper()
	k := make([]byte, secrets.KeyLen)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	s, err := secrets.New(k)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func sampleConfigWithSecrets() Config {
	return Config{
		Device: Device{Name: "knot", Country: "RU"},
		Role:   RoleWiFiExtender,
		Auth:   Auth{PasswordHash: "$2y$10$dummy"},
		Network: Network{
			Uplink: &WiFiUplink{SSID: "homewifi", PSK: "uplink-pass"},
			AP:     &WiFiAP{SSID: "knot-ap", PSK: "ap-password", Band: "2.4"},
			LAN: &LAN{
				CIDR: "192.168.42.0/24",
				DHCP: DHCP{PoolStart: "192.168.42.100", PoolEnd: "192.168.42.200"},
			},
		},
	}
}

func TestSaveWithEncryptsRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	s := mustSealer(t)

	in := sampleConfigWithSecrets()
	if err := SaveWith(path, in, s); err != nil {
		t.Fatalf("SaveWith: %v", err)
	}

	// On-disk should NOT contain the cleartext PSK.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "uplink-pass") || strings.Contains(string(raw), "ap-password") {
		t.Errorf("plaintext PSK leaked to disk:\n%s", raw)
	}
	if !strings.Contains(string(raw), secrets.Prefix) {
		t.Errorf("expected enc:v1: prefix on disk:\n%s", raw)
	}

	// Round-trip should produce the original cleartext.
	out, err := LoadWith(path, s)
	if err != nil {
		t.Fatal(err)
	}
	if out.Network.Uplink.PSK != "uplink-pass" {
		t.Errorf("uplink PSK: %q", out.Network.Uplink.PSK)
	}
	if out.Network.AP.PSK != "ap-password" {
		t.Errorf("ap PSK: %q", out.Network.AP.PSK)
	}
}

func TestSaveWithDoesNotMutateInput(t *testing.T) {
	dir := t.TempDir()
	s := mustSealer(t)
	in := sampleConfigWithSecrets()

	if err := SaveWith(filepath.Join(dir, "c.yaml"), in, s); err != nil {
		t.Fatal(err)
	}
	if in.Network.Uplink.PSK != "uplink-pass" {
		t.Errorf("input mutated: uplink PSK=%q", in.Network.Uplink.PSK)
	}
}

func TestLoadWithMigrationFlagsLegacy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	s := mustSealer(t)

	// Write a v0.2-style cleartext config (Save without sealer).
	if err := Save(path, sampleConfigWithSecrets()); err != nil {
		t.Fatal(err)
	}

	cfg, needs, err := LoadWithMigration(path, s)
	if err != nil {
		t.Fatalf("LoadWithMigration: %v", err)
	}
	if !needs {
		t.Error("legacy plaintext should set needsMigration=true")
	}
	if cfg.Network.Uplink.PSK != "uplink-pass" {
		t.Errorf("plaintext should pass through Unwrap: %q", cfg.Network.Uplink.PSK)
	}

	// Saving with the sealer migrates the on-disk file.
	if err := SaveWith(path, cfg, s); err != nil {
		t.Fatal(err)
	}

	// Second load: needsMigration should now be false.
	_, needs, err = LoadWithMigration(path, s)
	if err != nil {
		t.Fatal(err)
	}
	if needs {
		t.Error("after migration save, needsMigration should be false")
	}
}

func TestSaveWithoutSealerStillCleartext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := Save(path, sampleConfigWithSecrets()); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "uplink-pass") {
		t.Error("Save without sealer should keep cleartext (back-compat)")
	}
}
