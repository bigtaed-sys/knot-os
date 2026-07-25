package zapret

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Strategies are the real Flowseal/zapret-discord-youtube .bat files,
// converted from winws to nfqws at load time. They live on disk under
// <base>/strategies (refreshable from upstream) with an embedded copy
// as the first-run seed — so the strategy catalogue updates without a
// new knotd binary, exactly like the domain lists.
//
//go:embed assets/strategies
var seedStrategies embed.FS

// Default queue ports used for a custom strategy (which carries no
// --wf-tcp/--wf-udp of its own). Mirrors Flowseal's general filter.
const (
	DefaultTCPPorts = "80,443,2053,2083,2087,2096,8443"
	DefaultUDPPorts = "443,19294-19344,50000-50100"
)

// Strategy is one converted nfqws strategy.
type Strategy struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Desc string `json:"desc"`
	// Args is the nfqws argument list (with {LISTS}/{BIN} placeholders,
	// profiles separated by "--new"), minus the queue number.
	Args []string `json:"-"`
	// TCPPorts / UDPPorts are the nft NFQUEUE port sets this strategy
	// expects (parsed from the .bat's --wf-tcp / --wf-udp).
	TCPPorts string `json:"-"`
	UDPPorts string `json:"-"`
}

// stratMeta gives display names/descriptions for the seeded strategy
// IDs. Disk strategies with unknown IDs fall back to a derived name.
var stratMeta = map[string]struct{ name, desc string }{
	"general":       {"General", "Базовый набор Flowseal. Начни с него."},
	"alt":           {"General (ALT)", "Альтернатива №1, если General не пробил."},
	"alt2":          {"General (ALT2)", "Альтернатива №2."},
	"alt3":          {"General (ALT3)", "hostfakesplit + fake-tls-mod (нужен свежий nfqws)."},
	"alt4":          {"General (ALT4)", "Альтернатива №4."},
	"alt5":          {"General (ALT5)", "syndata/multidisorder. Не рекомендуется как первый выбор."},
	"alt6":          {"General (ALT6)", "Альтернатива №6."},
	"fake-tls-auto": {"General (FAKE TLS AUTO)", "Авто-фейк TLS, больше повторов."},
	"simple-fake":   {"General (SIMPLE FAKE)", "Простой fake — самый лёгкий вариант."},
}

// stratOrder fixes the dropdown order (general first).
var stratOrder = []string{
	"general", "alt", "alt2", "alt3", "alt4", "alt5", "alt6", "fake-tls-auto", "simple-fake",
}

// LoadStrategies reads the strategy catalogue. A disk copy under
// <base>/strategies is preferred over the embedded seed — but only when
// it actually converts: if a refresh pulled an upstream .bat whose format
// we can't parse, we fall back to the working seed for that ID instead of
// dropping the strategy entirely. Each .bat is converted from winws to
// nfqws; IDs unparseable in both seed and disk are skipped.
func LoadStrategies(base string) []Strategy {
	seed := map[string]string{}  // id -> embedded seed content
	disk := map[string]string{}  // id -> on-disk (refreshed) content

	if entries, err := fs.ReadDir(seedStrategies, "assets/strategies"); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".bat") {
				continue
			}
			if b, err := seedStrategies.ReadFile("assets/strategies/" + e.Name()); err == nil {
				seed[strings.TrimSuffix(e.Name(), ".bat")] = string(b)
			}
		}
	}
	diskDir := filepath.Join(base, "strategies")
	if entries, err := os.ReadDir(diskDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".bat") {
				continue
			}
			if b, err := os.ReadFile(filepath.Join(diskDir, e.Name())); err == nil {
				disk[strings.TrimSuffix(e.Name(), ".bat")] = string(b)
			}
		}
	}

	// Every ID we've seen from either source.
	ids := map[string]struct{}{}
	for id := range seed {
		ids[id] = struct{}{}
	}
	for id := range disk {
		ids[id] = struct{}{}
	}

	// convert returns the parsed strategy for content, ok=false when it
	// doesn't yield usable args.
	convert := func(content string) (Strategy, bool) {
		s, err := ConvertBat(content)
		if err != nil || len(s.Args) == 0 {
			return Strategy{}, false
		}
		return s, true
	}

	var out []Strategy
	for id := range ids {
		// Prefer the refreshed disk copy, but fall back to the seed when
		// the disk copy fails to convert (upstream format change).
		s, ok := Strategy{}, false
		if c, has := disk[id]; has {
			s, ok = convert(c)
		}
		if !ok {
			if c, has := seed[id]; has {
				s, ok = convert(c)
			}
		}
		if !ok {
			continue
		}
		s.ID = id
		if m, ok := stratMeta[id]; ok {
			s.Name, s.Desc = m.name, m.desc
		} else {
			s.Name = id
		}
		out = append(out, s)
	}
	// Stable order: known order first, then any extras alphabetically.
	rank := func(id string) int {
		for i, k := range stratOrder {
			if k == id {
				return i
			}
		}
		return len(stratOrder)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := rank(out[i].ID), rank(out[j].ID)
		if ri != rj {
			return ri < rj
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// ConvertBat converts a Flowseal winws .bat into an nfqws Strategy:
// it extracts the winws command, drops the windivert filter flags
// (--wf-*) while capturing their ports for the nft queue, rewrites
// %BIN%/%LISTS% to {BIN}/{LISTS} placeholders, and drops profiles that
// reference the (empty) game filter or any other unexpanded %var%.
func ConvertBat(content string) (Strategy, error) {
	cmd, ok := extractWinwsCommand(content)
	if !ok {
		return Strategy{}, fmt.Errorf("no winws command found")
	}
	toks, err := tokenize(cmd)
	if err != nil {
		return Strategy{}, err
	}

	var (
		profiles [][]string
		cur      []string
		tcp, udp string
	)
	for _, t := range toks {
		switch {
		case strings.HasPrefix(t, "--wf-tcp="):
			tcp = stripVarPorts(strings.TrimPrefix(t, "--wf-tcp="))
		case strings.HasPrefix(t, "--wf-udp="):
			udp = stripVarPorts(strings.TrimPrefix(t, "--wf-udp="))
		case strings.HasPrefix(t, "--wf-l3"):
			// windivert layer-3 selector — irrelevant for nfqws.
		case t == "--new":
			profiles = append(profiles, cur)
			cur = nil
		default:
			cur = append(cur, expandAssets(t, "{LISTS}", "{BIN}"))
		}
	}
	profiles = append(profiles, cur)

	var args []string
	for _, p := range profiles {
		if len(p) == 0 || profileHasVar(p) {
			continue // empty or game-filter / unexpanded-var profile
		}
		if len(args) > 0 {
			args = append(args, "--new")
		}
		args = append(args, p...)
	}
	if len(args) == 0 {
		return Strategy{}, fmt.Errorf("no usable profiles after conversion")
	}
	if tcp == "" {
		tcp = DefaultTCPPorts
	}
	if udp == "" {
		udp = DefaultUDPPorts
	}
	return Strategy{Args: args, TCPPorts: tcp, UDPPorts: udp}, nil
}

// extractWinwsCommand joins the winws invocation (handling ^ line
// continuations) and returns everything after the winws.exe token.
func extractWinwsCommand(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	var parts []string
	collecting := false
	for _, ln := range lines {
		if !collecting {
			if !strings.Contains(ln, "winws.exe") {
				continue
			}
			collecting = true
		}
		trimmed := strings.TrimRight(ln, " \t\r")
		cont := strings.HasSuffix(trimmed, "^")
		trimmed = strings.TrimSuffix(trimmed, "^")
		parts = append(parts, trimmed)
		if !cont {
			break
		}
	}
	if !collecting {
		return "", false
	}
	joined := strings.Join(parts, " ")
	i := strings.Index(joined, "winws.exe")
	if i < 0 {
		return "", false
	}
	rest := joined[i+len("winws.exe"):]
	// Drop the closing quote of "...winws.exe".
	rest = strings.TrimLeft(rest, "\" ")
	return rest, true
}

// stripVarPorts removes %var% entries (e.g. %GameFilterTCP%) from a
// comma-separated port list.
func stripVarPorts(s string) string {
	var keep []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" || strings.Contains(p, "%") {
			continue
		}
		keep = append(keep, p)
	}
	return strings.Join(keep, ",")
}

// profileHasVar reports whether any token still contains an unexpanded
// %...% (game filter or otherwise) — such a profile is dropped.
func profileHasVar(p []string) bool {
	for _, t := range p {
		if strings.Contains(t, "%") {
			return true
		}
	}
	return false
}

// expandAssets substitutes the winws %LISTS%/%BIN% prefixes with the
// given placeholders (load time uses {LISTS}/{BIN}; render time later
// swaps those for real paths via expandPaths).
func expandAssets(s, lists, bin string) string {
	s = strings.ReplaceAll(s, "%LISTS%", lists+"/")
	s = strings.ReplaceAll(s, "%BIN%", bin+"/")
	return s
}

// expandPaths swaps {LISTS}/{BIN} placeholders for the on-disk dirs.
func expandPaths(s, base string) string {
	s = strings.ReplaceAll(s, "{LISTS}", ListsDir(base))
	s = strings.ReplaceAll(s, "{BIN}", FakeDir(base))
	return s
}

// BuildInvocation resolves the chosen strategy (preset or custom) into
// the full nfqws argv plus the nft queue ports.
func BuildInvocation(s Settings, base string) (args []string, tcpPorts, udpPorts string, err error) {
	args = []string{fmt.Sprintf("--qnum=%d", QueueNum)}
	if strings.TrimSpace(s.CustomArgs) != "" {
		toks, terr := tokenize(s.CustomArgs)
		if terr != nil {
			return nil, "", "", fmt.Errorf("custom strategy: %w", terr)
		}
		for _, t := range toks {
			args = append(args, expandPaths(t, base))
		}
		return args, DefaultTCPPorts, DefaultUDPPorts, nil
	}

	strategies := LoadStrategies(base)
	if len(strategies) == 0 {
		return nil, "", "", fmt.Errorf("no strategies available")
	}
	chosen := strategies[0]
	for _, st := range strategies {
		if st.ID == s.Strategy {
			chosen = st
			break
		}
	}
	for _, a := range chosen.Args {
		args = append(args, expandPaths(a, base))
	}
	return args, chosen.TCPPorts, chosen.UDPPorts, nil
}

// tokenize splits a strategy string into argv, honouring double quotes
// so a quoted path with spaces stays one token. Newlines, the ^ line-
// continuation and \ are treated as whitespace, so a multi-line
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
