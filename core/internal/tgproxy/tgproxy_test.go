package tgproxy

import (
	"strings"
	"testing"
)

func TestBuildArgs(t *testing.T) {
	args := BuildArgs(Settings{Port: 9443, Secret: "00112233445566778899aabbccddeeff", LinkIP: "192.168.42.1"})
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--mode mtproto", "--port 9443",
		"--secret 00112233445566778899aabbccddeeff",
		"--link-ip 192.168.42.1", "--host 0.0.0.0",
		"--cf-proxy", "--cf-proxy-first",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q: %s", want, joined)
		}
	}
}

func TestBuildArgs_Defaults(t *testing.T) {
	args := strings.Join(BuildArgs(Settings{Secret: "x"}), " ")
	if !strings.Contains(args, "--port 8443") { // default port
		t.Errorf("default port should be 8443: %s", args)
	}
	// No LinkIP → no --link-ip flag.
	if strings.Contains(args, "--link-ip") {
		t.Errorf("no LinkIP should omit --link-ip: %s", args)
	}
}

func TestTGLink(t *testing.T) {
	link := TGLink(Settings{Port: 8443, Secret: "00112233445566778899aabbccddeeff", LinkIP: "1.2.3.4"})
	for _, want := range []string{"tg://proxy?", "server=1.2.3.4", "port=8443", "secret=00112233445566778899aabbccddeeff"} {
		if !strings.Contains(link, want) {
			t.Errorf("link missing %q: %s", want, link)
		}
	}
	// Incomplete → no link.
	if TGLink(Settings{Secret: "x"}) != "" {
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
