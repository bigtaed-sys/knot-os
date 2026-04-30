//go:build linux

package linux

import "testing"

func TestFirstUsableIP(t *testing.T) {
	cases := map[string]string{
		"192.168.42.0/24": "192.168.42.1",
		"10.0.0.0/8":      "10.0.0.1",
		"172.16.5.0/24":   "172.16.5.1",
	}
	for cidr, want := range cases {
		got, err := firstUsableIP(cidr)
		if err != nil {
			t.Errorf("firstUsableIP(%q): %v", cidr, err)
			continue
		}
		if got != want {
			t.Errorf("firstUsableIP(%q) = %q, want %q", cidr, got, want)
		}
	}
}

func TestFirstUsableIPRejectsBad(t *testing.T) {
	if _, err := firstUsableIP("not-cidr"); err == nil {
		t.Error("expected error for malformed CIDR")
	}
	if _, err := firstUsableIP("2001:db8::/32"); err == nil {
		t.Error("IPv6 CIDR should be rejected for v0.1")
	}
}

func TestCIDRPrefix(t *testing.T) {
	cases := map[string]string{
		"192.168.42.0/24": "24",
		"10.0.0.0/8":      "8",
		"no-slash":        "24",
	}
	for in, want := range cases {
		if got := cidrPrefix(in); got != want {
			t.Errorf("cidrPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}
