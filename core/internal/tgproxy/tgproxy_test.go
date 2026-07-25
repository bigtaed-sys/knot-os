package tgproxy

import (
	"strings"
	"testing"
)

func TestBuildArgs_MTProto(t *testing.T) {
	args := BuildArgs(Settings{Mode: ModeMTProto, Port: 9443, Secret: "00112233445566778899aabbccddeeff", LinkIP: "192.168.42.1"})
	joined := strings.Join(args, " ")
	for _, want := range []string{"--mode mtproto", "--port 9443", "--secret 00112233445566778899aabbccddeeff", "--link-ip 192.168.42.1", "--host 0.0.0.0"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q: %s", want, joined)
		}
	}
}

func TestBuildArgs_SOCKS5_NoSecret(t *testing.T) {
	args := strings.Join(BuildArgs(Settings{Mode: ModeSOCKS5, Port: 1080}), " ")
	if strings.Contains(args, "--secret") || strings.Contains(args, "--link-ip") {
		t.Errorf("socks5 must not carry mtproto flags: %s", args)
	}
	if !strings.Contains(args, "--mode socks5") {
		t.Errorf("args: %s", args)
	}
}

func TestBuildArgs_Defaults(t *testing.T) {
	args := strings.Join(BuildArgs(Settings{Secret: "x"}), " ")
	if !strings.Contains(args, "--mode mtproto") { // default mode
		t.Errorf("default mode should be mtproto: %s", args)
	}
	if !strings.Contains(args, "--port 8443") { // default port
		t.Errorf("default port should be 8443: %s", args)
	}
}

func TestTGLink(t *testing.T) {
	link := TGLink(Settings{Mode: ModeMTProto, Port: 8443, Secret: "00112233445566778899aabbccddeeff", LinkIP: "1.2.3.4"})
	for _, want := range []string{"tg://proxy?", "server=1.2.3.4", "port=8443", "secret=00112233445566778899aabbccddeeff"} {
		if !strings.Contains(link, want) {
			t.Errorf("link missing %q: %s", want, link)
		}
	}
	// Incomplete / socks5 → no link.
	if TGLink(Settings{Mode: ModeSOCKS5, Secret: "x", LinkIP: "1.2.3.4"}) != "" {
		t.Error("socks5 should have no tg link")
	}
	if TGLink(Settings{Mode: ModeMTProto, Secret: "x"}) != "" {
		t.Error("no LinkIP → no tg link")
	}
}

func TestSecret(t *testing.T) {
	s, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	if !ValidSecret(s) {
		t.Errorf("generated secret invalid: %q", s)
	}
	if ValidSecret("short") || ValidSecret("zz112233445566778899aabbccddeeff") {
		t.Error("bad secrets should be rejected")
	}
}
