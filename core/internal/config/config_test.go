package config

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDefaultIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("default config must validate, got: %v", err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	in := Default()
	in.Device.Name = "knot-test"
	in.Device.Country = "RU"

	if err := Save(path, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round-trip mismatch:\nin  = %#v\nout = %#v", in, out)
	}
}

func TestLoadMissingReturnsDefault(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("missing file should not error, got: %v", err)
	}
	if !reflect.DeepEqual(cfg, Default()) {
		t.Fatalf("missing file should return Default(), got %#v", cfg)
	}
}

func TestValidateRejectsBadConfigs(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantSub string
	}{
		{
			name:    "empty hostname",
			mutate:  func(c *Config) { c.Device.Name = "" },
			wantSub: "device.name",
		},
		{
			name:    "bad country",
			mutate:  func(c *Config) { c.Device.Country = "Russia" },
			wantSub: "device.country",
		},
		{
			name:    "unknown role",
			mutate:  func(c *Config) { c.Role = Role("god-mode") },
			wantSub: "role",
		},
		{
			name: "extender without uplink",
			mutate: func(c *Config) {
				c.Role = RoleWiFiExtender
				c.Network.AP = &WiFiAP{SSID: "x", Band: "2.4"}
			},
			wantSub: "uplink.ssid",
		},
		{
			name: "extender bad CIDR",
			mutate: func(c *Config) {
				c.Role = RoleWiFiExtender
				c.Network.Uplink = &WiFiUplink{SSID: "u"}
				c.Network.AP = &WiFiAP{SSID: "a", Band: "2.4"}
				c.Network.LAN = &LAN{CIDR: "not-a-cidr", DHCP: DHCP{PoolStart: "1.1.1.1", PoolEnd: "1.1.1.2"}}
			},
			wantSub: "lan.cidr",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			tc.mutate(&c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("expected error containing %q, got: %v", tc.wantSub, err)
			}
		})
	}
}
