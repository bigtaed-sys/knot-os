package zapret

import (
	"strings"
	"testing"
)

const sampleBat = "@echo off\r\n" +
	"set \"BIN=%~dp0bin\\\"\r\n" +
	"set \"LISTS=%~dp0lists\\\"\r\n" +
	"start \"zapret: %~n0\" /min \"%BIN%winws.exe\" --wf-tcp=80,443,2053,%GameFilterTCP% --wf-udp=443,19294-19344,%GameFilterUDP% ^\r\n" +
	"--filter-udp=443 --hostlist=\"%LISTS%list-general.txt\" --dpi-desync=fake --dpi-desync-fake-quic=\"%BIN%quic_initial_www_google_com.bin\" --new ^\r\n" +
	"--filter-tcp=80,443 --hostlist=\"%LISTS%list-google.txt\" --dpi-desync=multisplit --dpi-desync-split-pos=1 --new ^\r\n" +
	"--filter-tcp=%GameFilterTCP% --ipset=\"%LISTS%ipset-all.txt\" --dpi-desync=fake\r\n"

func TestConvertBat(t *testing.T) {
	s, err := ConvertBat(sampleBat)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(s.Args, " ")
	// %BIN%/%LISTS% become {BIN}/{LISTS} placeholders.
	if !strings.Contains(joined, "{LISTS}/list-general.txt") {
		t.Errorf("LISTS not converted: %s", joined)
	}
	if !strings.Contains(joined, "{BIN}/quic_initial_www_google_com.bin") {
		t.Errorf("BIN not converted: %s", joined)
	}
	// Windivert filter flags are dropped from the args...
	if strings.Contains(joined, "--wf-tcp") || strings.Contains(joined, "--wf-udp") {
		t.Errorf("wf-* leaked into args: %s", joined)
	}
	// ...but their ports populate the nft sets, minus the game vars.
	if s.TCPPorts != "80,443,2053" {
		t.Errorf("TCPPorts=%q, want 80,443,2053", s.TCPPorts)
	}
	if s.UDPPorts != "443,19294-19344" {
		t.Errorf("UDPPorts=%q, want 443,19294-19344", s.UDPPorts)
	}
	// The game-filter profile (last one) is dropped — no leftover %var%.
	if strings.Contains(joined, "%") {
		t.Errorf("unexpanded var survived: %s", joined)
	}
	if strings.Contains(joined, "ipset-all.txt") {
		t.Errorf("game-filter profile not dropped: %s", joined)
	}
	// Two profiles survive → one --new separator.
	if strings.Count(joined, "--new") != 1 {
		t.Errorf("expected 1 --new, got: %s", joined)
	}
}

func TestBuildInvocationPreset(t *testing.T) {
	// Uses the embedded seed strategies (no disk dir under a temp base).
	base := t.TempDir()
	args, tcp, udp, err := BuildInvocation(Settings{Enabled: true, Strategy: "general"}, base)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.HasPrefix(joined, "--qnum=200") {
		t.Errorf("missing queue num: %s", joined)
	}
	if !strings.Contains(joined, base) {
		t.Errorf("placeholders not expanded to base path: %s", joined)
	}
	if strings.Contains(joined, "{LISTS}") || strings.Contains(joined, "{BIN}") {
		t.Errorf("leftover placeholder: %s", joined)
	}
	if tcp == "" || udp == "" {
		t.Errorf("empty ports: tcp=%q udp=%q", tcp, udp)
	}
}

func TestBuildInvocationCustomOverrides(t *testing.T) {
	base := t.TempDir()
	args, tcp, udp, err := BuildInvocation(Settings{
		Enabled:    true,
		Strategy:   "general",
		CustomArgs: `--filter-tcp=443 --hostlist="{LISTS}/list-google.txt" --dpi-desync=split2`,
	}, base)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--dpi-desync=split2") {
		t.Errorf("custom args ignored: %s", joined)
	}
	if !strings.Contains(joined, base+"/lists/list-google.txt") {
		t.Errorf("placeholder not expanded in custom args: %s", joined)
	}
	if tcp != DefaultTCPPorts || udp != DefaultUDPPorts {
		t.Errorf("custom should use default ports, got tcp=%q udp=%q", tcp, udp)
	}
}

func TestLoadStrategiesSeed(t *testing.T) {
	got := LoadStrategies(t.TempDir())
	if len(got) == 0 {
		t.Fatal("no strategies loaded from embedded seed")
	}
	if got[0].ID != "general" {
		t.Errorf("general should sort first, got %q", got[0].ID)
	}
	for _, s := range got {
		if len(s.Args) == 0 {
			t.Errorf("strategy %q has no args", s.ID)
		}
		if s.TCPPorts == "" || s.UDPPorts == "" {
			t.Errorf("strategy %q has empty ports", s.ID)
		}
	}
}

func TestTokenizeHandlesQuotesAndContinuations(t *testing.T) {
	toks, err := tokenize("--a=1 --b=\"two words\" ^\n  --c=3 \\\n  --d")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--a=1", "--b=two words", "--c=3", "--d"}
	if len(toks) != len(want) {
		t.Fatalf("got %v, want %v", toks, want)
	}
	for i := range want {
		if toks[i] != want[i] {
			t.Errorf("token %d: got %q, want %q", i, toks[i], want[i])
		}
	}
}
