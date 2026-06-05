package zapret

import (
	"strings"
	"testing"
)

func TestRenderArgsPresetExpandsAssets(t *testing.T) {
	args, err := RenderArgs(Settings{Enabled: true, Strategy: "general"}, "/var/lib/knot/zapret")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.HasPrefix(joined, "--qnum=200") {
		t.Errorf("missing queue num: %s", joined)
	}
	if !strings.Contains(joined, "/var/lib/knot/zapret/lists/list-google.txt") {
		t.Errorf("{LISTS} not expanded: %s", joined)
	}
	if !strings.Contains(joined, "/var/lib/knot/zapret/bin/quic_initial_www_google_com.bin") {
		t.Errorf("{BIN} not expanded: %s", joined)
	}
	if !strings.Contains(joined, "--new") {
		t.Errorf("profiles not joined with --new: %s", joined)
	}
	// No unexpanded placeholders must survive.
	if strings.Contains(joined, "{LISTS}") || strings.Contains(joined, "{BIN}") {
		t.Errorf("leftover placeholder: %s", joined)
	}
}

func TestRenderArgsUnknownPresetFallsBackToGeneral(t *testing.T) {
	a1, _ := RenderArgs(Settings{Enabled: true, Strategy: "does-not-exist"}, "/base")
	a2, _ := RenderArgs(Settings{Enabled: true, Strategy: "general"}, "/base")
	if strings.Join(a1, " ") != strings.Join(a2, " ") {
		t.Error("unknown preset should fall back to general")
	}
}

func TestRenderArgsCustomOverridesPreset(t *testing.T) {
	args, err := RenderArgs(Settings{
		Enabled:    true,
		Strategy:   "general",
		CustomArgs: `--filter-tcp=443 --hostlist="{LISTS}/list-google.txt" --dpi-desync=split2`,
	}, "/base")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--dpi-desync=split2") {
		t.Errorf("custom args ignored: %s", joined)
	}
	if strings.Contains(joined, "multisplit") {
		t.Errorf("preset leaked through despite custom args: %s", joined)
	}
	if !strings.Contains(joined, "/base/lists/list-google.txt") {
		t.Errorf("placeholder not expanded in custom args: %s", joined)
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

func TestTokenizeUnbalancedQuote(t *testing.T) {
	if _, err := tokenize(`--a="oops`); err == nil {
		t.Error("expected error on unbalanced quote")
	}
}

func TestPresetsNonEmpty(t *testing.T) {
	if len(Presets()) == 0 {
		t.Fatal("no presets defined")
	}
	for _, p := range Presets() {
		if p.ID == "" || p.Name == "" || len(p.profiles) == 0 {
			t.Errorf("incomplete preset: %+v", p)
		}
	}
}
