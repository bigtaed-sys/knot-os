package zapret

import (
	"fmt"
	"strings"
)

// Preset is a named, ready-to-use nfqws strategy. Each preset is a
// sequence of nfqws "profiles" (the engine applies the first whose
// --filter matches a packet). Profiles are joined with `--new` at
// render time. {LISTS} and {BIN} placeholders expand to the on-disk
// asset directories.
//
// These are ported from the Flowseal/zapret-discord-youtube strategies
// (general / ALT / ALT2), scoped to the YouTube + Discord hostlists.
// They are STARTING POINTS — DPI behaviour is ISP-specific, so the UI
// lets the user switch presets or paste a custom strategy. Because the
// strategy is data (config + on-disk lists), changing it never needs a
// new knotd binary.
type Preset struct {
	ID       string
	Name     string
	Desc     string
	profiles [][]string
}

// quicYT is the shared QUIC profile (YouTube/general over udp/443).
var quicYT = []string{
	"--filter-udp=443",
	"--hostlist={LISTS}/list-general.txt", "--hostlist={LISTS}/list-google.txt",
	"--dpi-desync=fake", "--dpi-desync-repeats=6",
	"--dpi-desync-fake-quic={BIN}/quic_initial_www_google_com.bin",
}

// discordVoice is the shared Discord voice/STUN profile (udp ranges).
var discordVoice = []string{
	"--filter-udp=19294-19344,50000-50100", "--filter-l7=discord,stun",
	"--dpi-desync=fake",
	"--dpi-desync-fake-discord={BIN}/quic_initial_dbankcloud_ru.bin",
	"--dpi-desync-fake-stun={BIN}/quic_initial_dbankcloud_ru.bin",
	"--dpi-desync-repeats=6",
}

var presets = []Preset{
	{
		ID:   "general",
		Name: "General",
		Desc: "Multisplit + seqovl. Хорошая отправная точка для большинства провайдеров.",
		profiles: [][]string{
			quicYT,
			discordVoice,
			{
				"--filter-tcp=443",
				"--hostlist={LISTS}/list-general.txt", "--hostlist={LISTS}/list-google.txt",
				"--dpi-desync=multisplit", "--dpi-desync-split-seqovl=681", "--dpi-desync-split-pos=1",
				"--dpi-desync-split-seqovl-pattern={BIN}/tls_clienthello_www_google_com.bin",
			},
			{
				"--filter-tcp=80",
				"--hostlist={LISTS}/list-general.txt", "--hostlist={LISTS}/list-google.txt",
				"--dpi-desync=multisplit", "--dpi-desync-split-pos=1",
			},
		},
	},
	{
		ID:   "alt",
		Name: "General (ALT)",
		Desc: "Fake + fakedsplit с ts-fooling. Если General не пробил.",
		profiles: [][]string{
			quicYT,
			discordVoice,
			{
				"--filter-tcp=443",
				"--hostlist={LISTS}/list-general.txt", "--hostlist={LISTS}/list-google.txt",
				"--dpi-desync=fake,fakedsplit", "--dpi-desync-repeats=6",
				"--dpi-desync-fooling=ts", "--dpi-desync-fakedsplit-pattern=0x00",
				"--dpi-desync-fake-tls={BIN}/tls_clienthello_www_google_com.bin",
			},
			{
				"--filter-tcp=80",
				"--hostlist={LISTS}/list-general.txt", "--hostlist={LISTS}/list-google.txt",
				"--dpi-desync=fake,fakedsplit", "--dpi-desync-repeats=6", "--dpi-desync-fooling=ts",
			},
		},
	},
	{
		ID:   "alt2",
		Name: "General (ALT2)",
		Desc: "Multisplit seqovl=652, pos=2. Ещё один вариант на перебор.",
		profiles: [][]string{
			quicYT,
			discordVoice,
			{
				"--filter-tcp=443",
				"--hostlist={LISTS}/list-general.txt", "--hostlist={LISTS}/list-google.txt",
				"--dpi-desync=multisplit", "--dpi-desync-split-seqovl=652", "--dpi-desync-split-pos=2",
				"--dpi-desync-split-seqovl-pattern={BIN}/tls_clienthello_www_google_com.bin",
			},
			{
				"--filter-tcp=80",
				"--hostlist={LISTS}/list-general.txt", "--hostlist={LISTS}/list-google.txt",
				"--dpi-desync=multisplit", "--dpi-desync-split-pos=2",
			},
		},
	},
	{
		ID:   "discord",
		Name: "Только Discord",
		Desc: "Голос/текст Discord без YouTube — легче и стабильнее, если нужен только дискорд.",
		profiles: [][]string{
			discordVoice,
			{
				"--filter-tcp=443",
				"--hostlist={LISTS}/list-general.txt",
				"--dpi-desync=fake,fakedsplit", "--dpi-desync-repeats=6",
				"--dpi-desync-fooling=ts", "--dpi-desync-fakedsplit-pattern=0x00",
				"--dpi-desync-fake-tls={BIN}/tls_clienthello_www_google_com.bin",
			},
		},
	},
}

// Presets returns the built-in strategy catalogue (UI dropdown).
func Presets() []Preset { return presets }

// presetByID looks a preset up; ok=false when unknown.
func presetByID(id string) (Preset, bool) {
	for _, p := range presets {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}

// render flattens the preset's profiles into one nfqws argument list,
// joining profiles with `--new` and expanding asset placeholders.
func (p Preset) render(base string) []string {
	var out []string
	for i, prof := range p.profiles {
		if i > 0 {
			out = append(out, "--new")
		}
		for _, a := range prof {
			out = append(out, expandAssets(a, base))
		}
	}
	return out
}

// expandAssets substitutes {LISTS} and {BIN} with the on-disk dirs.
func expandAssets(s, base string) string {
	s = strings.ReplaceAll(s, "{LISTS}", ListsDir(base))
	s = strings.ReplaceAll(s, "{BIN}", FakeDir(base))
	return s
}

// RenderArgs produces the full nfqws argv (excluding the binary path)
// for the given settings: the queue number plus either the custom
// strategy or the selected preset (defaulting to "general").
func RenderArgs(s Settings, base string) ([]string, error) {
	args := []string{fmt.Sprintf("--qnum=%d", QueueNum)}
	if strings.TrimSpace(s.CustomArgs) != "" {
		toks, err := tokenize(s.CustomArgs)
		if err != nil {
			return nil, fmt.Errorf("custom strategy: %w", err)
		}
		for _, t := range toks {
			args = append(args, expandAssets(t, base))
		}
		return args, nil
	}
	p, ok := presetByID(s.Strategy)
	if !ok {
		p = presets[0] // default: general
	}
	return append(args, p.render(base)...), nil
}

// tokenize splits a strategy string into argv, honouring double quotes
// so a quoted path with spaces stays one token. Newlines and the line-
// continuation backslash are treated as whitespace, so a multi-line
// Flowseal-style block pastes cleanly.
func tokenize(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inQuote := false
	hasTok := false
	flush := func() {
		if hasTok {
			out = append(out, cur.String())
			cur.Reset()
			hasTok = false
		}
	}
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
			hasTok = true
		case (r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\\' || r == '^') && !inQuote:
			flush()
		default:
			cur.WriteRune(r)
			hasTok = true
		}
	}
	if inQuote {
		return nil, fmt.Errorf("unbalanced quote")
	}
	flush()
	return out, nil
}
