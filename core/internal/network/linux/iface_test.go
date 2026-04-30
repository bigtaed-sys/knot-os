//go:build linux

package linux

import "testing"

func TestFlipLocalBit(t *testing.T) {
	cases := []struct {
		in   string
		out  string
		ok   bool
	}{
		{"dc:a6:32:11:22:33", "de:a6:32:11:22:33", true}, // 0xdc XOR 0x02 = 0xde
		{"de:a6:32:11:22:33", "dc:a6:32:11:22:33", true}, // round-trip
		{"00:11:22:33:44:55", "02:11:22:33:44:55", true}, // 0x00 -> 0x02
		{"02:11:22:33:44:55", "00:11:22:33:44:55", true}, // 0x02 -> 0x00
		{"not-a-mac", "", false},
		{"", "", false},
		{"aa:bb:cc:dd", "", false}, // too few octets
	}
	for _, tc := range cases {
		got, ok := flipLocalBit(tc.in)
		if ok != tc.ok || got != tc.out {
			t.Errorf("flipLocalBit(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.out, tc.ok)
		}
	}
}
