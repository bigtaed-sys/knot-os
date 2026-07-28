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

func TestValidateRouterAccepts(t *testing.T) {
	c := Default()
	c.Role = RoleWiFiRouter
	c.Auth = Auth{PasswordHash: "$2a$12$placeholder"}
	c.Network.WAN = &WAN{Interface: "eth0", Mode: "dhcp"}
	c.Network.AP = &WiFiAP{SSID: "knot-ap", Band: "2.4", Channel: 11}
	c.Network.LAN = &LAN{CIDR: "192.168.42.0/24", DHCP: DHCP{PoolStart: "192.168.42.100", PoolEnd: "192.168.42.200"}}
	if err := c.Validate(); err != nil {
		t.Fatalf("router with all required fields should validate: %v", err)
	}
}

func validRouter() Config {
	c := Default()
	c.Role = RoleWiFiRouter
	c.Auth = Auth{PasswordHash: "$2a$12$placeholder"}
	c.Network.WAN = &WAN{Interface: "eth0", Mode: "dhcp"}
	c.Network.AP = &WiFiAP{SSID: "knot-ap", Band: "2.4", Channel: 11}
	c.Network.LAN = &LAN{CIDR: "192.168.42.0/24", DHCP: DHCP{PoolStart: "192.168.42.100", PoolEnd: "192.168.42.200"}}
	return c
}

func TestValidateLANPorts(t *testing.T) {
	// A distinct set of wired LAN ports (none equal to the WAN) is fine.
	good := validRouter()
	good.Network.LANPorts = []string{"eth1", "eth2"}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid lan_ports rejected: %v", err)
	}

	bad := []struct {
		name  string
		ports []string
		sub   string
	}{
		{"is the WAN", []string{"eth0"}, "WAN"},          // eth0 is validRouter's WAN
		{"duplicate", []string{"eth1", "eth1"}, "duplicate"},
		{"bad name", []string{"eth 1"}, "not a valid"},
	}
	for _, tc := range bad {
		c := validRouter()
		c.Network.LANPorts = tc.ports
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.sub) {
			t.Errorf("%s: got err=%v, want substring %q", tc.name, err, tc.sub)
		}
	}

	// lan_ports is router-only: the extender role must reject a non-empty
	// list rather than silently ignore it.
	ext := Default()
	ext.Role = RoleWiFiExtender
	ext.Auth = Auth{PasswordHash: "$2a$12$placeholder"}
	ext.Network.Uplink = &WiFiUplink{SSID: "up"}
	ext.Network.AP = &WiFiAP{SSID: "knot-ap", Band: "2.4"}
	ext.Network.LAN = &LAN{CIDR: "192.168.42.0/24", DHCP: DHCP{PoolStart: "192.168.42.100", PoolEnd: "192.168.42.200"}}
	ext.Network.LANPorts = []string{"eth1"}
	if err := ext.Validate(); err == nil || !strings.Contains(err.Error(), "wifi-router") {
		t.Errorf("extender with lan_ports should be rejected, got %v", err)
	}
}

func TestValidatePortForwards(t *testing.T) {
	good := validRouter()
	good.Network.PortForwards = []PortForward{
		{ID: "game", Proto: "tcp", WANPort: 25565, DestIP: "192.168.42.50", Enabled: true},
		{ID: "dns", Proto: "tcp/udp", WANPort: 5353, DestIP: "192.168.42.51", DestPort: 53},
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid port forwards rejected: %v", err)
	}

	bad := []struct {
		name string
		pf   PortForward
		sub  string
	}{
		{"bad proto", PortForward{ID: "a", Proto: "icmp", WANPort: 80, DestIP: "192.168.42.5"}, "proto"},
		{"port range", PortForward{ID: "a", Proto: "tcp", WANPort: 0, DestIP: "192.168.42.5"}, "wan_port"},
		{"bad ip", PortForward{ID: "a", Proto: "tcp", WANPort: 80, DestIP: "not-an-ip"}, "dest_ip"},
		{"bad id", PortForward{ID: "Bad ID", Proto: "tcp", WANPort: 80, DestIP: "192.168.42.5"}, "id"},
	}
	for _, tc := range bad {
		c := validRouter()
		c.Network.PortForwards = []PortForward{tc.pf}
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.sub) {
			t.Errorf("%s: got err=%v, want substring %q", tc.name, err, tc.sub)
		}
	}

	// Duplicate (proto, wan_port) is rejected.
	dup := validRouter()
	dup.Network.PortForwards = []PortForward{
		{ID: "a", Proto: "tcp", WANPort: 443, DestIP: "192.168.42.5"},
		{ID: "b", Proto: "tcp/udp", WANPort: 443, DestIP: "192.168.42.6"},
	}
	if err := dup.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("duplicate wan port should be rejected, got %v", err)
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
			name: "extender without auth hash",
			mutate: func(c *Config) {
				c.Role = RoleWiFiExtender
				c.Network.Uplink = &WiFiUplink{SSID: "u"}
				c.Network.AP = &WiFiAP{SSID: "a", Band: "2.4"}
			},
			wantSub: "auth.password_hash",
		},
		{
			name: "extender without uplink",
			mutate: func(c *Config) {
				c.Role = RoleWiFiExtender
				c.Auth = Auth{PasswordHash: "$2a$12$placeholder"}
				c.Network.AP = &WiFiAP{SSID: "x", Band: "2.4"}
			},
			wantSub: "uplink.ssid",
		},
		{
			name: "extender bad CIDR",
			mutate: func(c *Config) {
				c.Role = RoleWiFiExtender
				c.Auth = Auth{PasswordHash: "$2a$12$placeholder"}
				c.Network.Uplink = &WiFiUplink{SSID: "u"}
				c.Network.AP = &WiFiAP{SSID: "a", Band: "2.4"}
				c.Network.LAN = &LAN{CIDR: "not-a-cidr", DHCP: DHCP{PoolStart: "1.1.1.1", PoolEnd: "1.1.1.2"}}
			},
			wantSub: "lan.cidr",
		},
		{
			name: "router without WAN",
			mutate: func(c *Config) {
				c.Role = RoleWiFiRouter
				c.Auth = Auth{PasswordHash: "$2a$12$placeholder"}
				c.Network.AP = &WiFiAP{SSID: "a", Band: "2.4"}
				c.Network.LAN = &LAN{CIDR: "192.168.42.0/24", DHCP: DHCP{PoolStart: "192.168.42.100", PoolEnd: "192.168.42.200"}}
			},
			wantSub: "wan is required",
		},
		{
			name: "router unsupported WAN mode",
			mutate: func(c *Config) {
				c.Role = RoleWiFiRouter
				c.Auth = Auth{PasswordHash: "$2a$12$placeholder"}
				c.Network.WAN = &WAN{Interface: "eth0", Mode: "pppoe"}
				c.Network.AP = &WiFiAP{SSID: "a", Band: "2.4"}
				c.Network.LAN = &LAN{CIDR: "192.168.42.0/24", DHCP: DHCP{PoolStart: "192.168.42.100", PoolEnd: "192.168.42.200"}}
			},
			wantSub: "wan.mode",
		},
		{
			name: "router channel out of range",
			mutate: func(c *Config) {
				c.Role = RoleWiFiRouter
				c.Auth = Auth{PasswordHash: "$2a$12$placeholder"}
				c.Network.WAN = &WAN{Interface: "eth0"}
				c.Network.AP = &WiFiAP{SSID: "a", Band: "2.4", Channel: 200}
				c.Network.LAN = &LAN{CIDR: "192.168.42.0/24", DHCP: DHCP{PoolStart: "192.168.42.100", PoolEnd: "192.168.42.200"}}
			},
			wantSub: "ap.channel",
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
