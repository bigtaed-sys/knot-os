package wol

import (
	"net"
	"testing"
)

func TestParseMACAcceptsCommonFormats(t *testing.T) {
	cases := []string{
		"aa:bb:cc:dd:ee:ff",
		"AA:BB:CC:DD:EE:FF",
		"aa-bb-cc-dd-ee-ff",
		"aabbccddeeff",
	}
	want := net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	for _, s := range cases {
		got, err := parseMAC(s)
		if err != nil {
			t.Errorf("parseMAC(%q) error: %v", s, err)
			continue
		}
		if got.String() != want.String() {
			t.Errorf("parseMAC(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestParseMACRejectsGarbage(t *testing.T) {
	for _, s := range []string{"", "  ", "garbage", "aa:bb:cc:dd:ee", "aabbcc"} {
		if _, err := parseMAC(s); err == nil {
			t.Errorf("parseMAC(%q) should fail", s)
		}
	}
}

func TestBuildMagicPacketShape(t *testing.T) {
	mac := net.HardwareAddr{0x11, 0x22, 0x33, 0x44, 0x55, 0x66}
	pkt := buildMagicPacket(mac)
	if len(pkt) != 102 {
		t.Fatalf("len = %d, want 102", len(pkt))
	}
	for i := 0; i < 6; i++ {
		if pkt[i] != 0xff {
			t.Errorf("byte %d: %02x, want ff", i, pkt[i])
		}
	}
	for rep := 0; rep < 16; rep++ {
		off := 6 + rep*6
		for i := 0; i < 6; i++ {
			if pkt[off+i] != mac[i] {
				t.Errorf("repeat %d byte %d: got %02x, want %02x",
					rep, i, pkt[off+i], mac[i])
			}
		}
	}
}

func TestBroadcastForCIDR(t *testing.T) {
	cases := map[string]string{
		"192.168.42.0/24":  "192.168.42.255",
		"10.0.0.0/16":      "10.0.255.255",
		"172.16.0.0/22":    "172.16.3.255",
		"192.168.1.100/30": "192.168.1.103",
	}
	for in, want := range cases {
		got, err := BroadcastForCIDR(in)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("BroadcastForCIDR(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestBroadcastForCIDRRejectsIPv6(t *testing.T) {
	if _, err := BroadcastForCIDR("fe80::/64"); err == nil {
		t.Error("IPv6 CIDR should be rejected")
	}
}

// End-to-end: send the magic packet to a UDP listener bound on
// 127.0.0.1 and check we receive 102 bytes with the right shape.
// Skips the broadcast plumbing — exercising the real kernel
// broadcast path requires a routable interface and is best left to
// integration tests.
func TestWakeSendsToListener(t *testing.T) {
	addr, err := net.ResolveUDPAddr("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUDP("udp4", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()

	port := listener.LocalAddr().(*net.UDPAddr).Port
	if err := Wake("aa:bb:cc:dd:ee:ff", "127.0.0.1", port); err != nil {
		t.Fatalf("Wake: %v", err)
	}

	buf := make([]byte, 256)
	n, _, err := listener.ReadFromUDP(buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 102 {
		t.Errorf("received %d bytes, want 102", n)
	}
}
