//go:build linux

package linux

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/knot-os/knot-os/core/internal/network"
)

// Cellular control-plane extras driven via mmcli: USSD (prepaid balance),
// SMS (receive codes / send), and access-tech / band selection (prefer
// 4G, lock a band for a fixed MIMO antenna). All best-effort against real
// hardware — mmcli output varies by modem, so parsers are lenient.

var errNoModem = fmt.Errorf("no cellular modem found")

// SendUSSD runs a USSD session (e.g. "*100#" for balance) and returns the
// network's textual response. The session is cancelled afterwards so a
// menu-style code doesn't leave the modem in an open session.
func (b *LinuxBackend) SendUSSD(ctx context.Context, code string) (string, error) {
	idx, ok := b.firstModemIndex(ctx)
	if !ok {
		return "", errNoModem
	}
	out, err := b.r.run(ctx, "mmcli", "-m", idx, "--3gpp-ussd-initiate="+code)
	// Best-effort cleanup: end the session regardless of the outcome.
	b.r.runIgnoreError(ctx, "mmcli", "-m", idx, "--3gpp-ussd-cancel")
	if err != nil {
		return "", fmt.Errorf("ussd %q: %w", code, err)
	}
	resp := parseUSSDResponse(out)
	if resp == "" {
		return "", fmt.Errorf("ussd %q: empty response", code)
	}
	return resp, nil
}

// parseUSSDResponse pulls the quoted reply out of mmcli's USSD output.
// The reply is always single-quoted, but the label varies by mmcli
// version — "response: 'Balance 123'" or "new reply from network:
// 'Balance 123'" — so we extract the quoted span rather than matching a
// label. Falls back to the whole output when there are no quotes.
func parseUSSDResponse(out string) string {
	if i := strings.IndexByte(out, '\''); i >= 0 {
		if j := strings.LastIndexByte(out, '\''); j > i {
			return strings.TrimSpace(out[i+1 : j])
		}
	}
	return strings.TrimSpace(out)
}

// ListSMS returns stored short messages, newest first.
func (b *LinuxBackend) ListSMS(ctx context.Context) ([]network.SMS, error) {
	idx, ok := b.firstModemIndex(ctx)
	if !ok {
		return nil, errNoModem
	}
	kv, err := b.mmcliKV(ctx, "-m", idx, "--messaging-list-sms")
	if err != nil {
		return nil, err
	}
	var ids []string
	for k, v := range kv {
		if strings.HasPrefix(k, "modem.messaging.sms.value") {
			if id := v[strings.LastIndex(v, "/")+1:]; id != "" {
				ids = append(ids, id)
			}
		}
	}
	out := make([]network.SMS, 0, len(ids))
	for _, id := range ids {
		skv, err := b.mmcliKV(ctx, "-s", id)
		if err != nil {
			continue
		}
		out = append(out, network.SMS{
			ID:        id,
			Number:    skv["sms.content.number"],
			Text:      unescapeGLib(skv["sms.content.text"]),
			Timestamp: skv["sms.properties.timestamp"],
			// mmcli's -K reports direction via pdu-type ("submit" = we
			// sent it, "deliver" = received); older versions only set
			// properties.state ("sent"/"received"). Check both.
			Sent: skv["sms.pdu-type"] == "submit" || skv["sms.properties.state"] == "sent",
		})
	}
	// Newest first by numeric index (higher = more recent).
	sort.Slice(out, func(i, j int) bool {
		return atoiOr(out[i].ID, 0) > atoiOr(out[j].ID, 0)
	})
	return out, nil
}

// SendSMS creates and sends a text message. Quotes are stripped from the
// inputs so the mmcli create string can't be broken.
func (b *LinuxBackend) SendSMS(ctx context.Context, number, text string) error {
	idx, ok := b.firstModemIndex(ctx)
	if !ok {
		return errNoModem
	}
	number = sanitizeSMSField(number)
	text = sanitizeSMSField(text)
	if number == "" || text == "" {
		return fmt.Errorf("sms: number and text are required")
	}
	out, err := b.r.run(ctx, "mmcli", "-m", idx,
		fmt.Sprintf("--messaging-create-sms=text='%s',number='%s'", text, number))
	if err != nil {
		return fmt.Errorf("sms create: %w", err)
	}
	path := parseCreatedSMSPath(out)
	if path == "" {
		return fmt.Errorf("sms: could not determine created message path")
	}
	if err := b.r.runOK(ctx, "mmcli", "-s", path, "--send"); err != nil {
		return fmt.Errorf("sms send: %w", err)
	}
	return nil
}

// DeleteSMS removes a stored message by its index.
func (b *LinuxBackend) DeleteSMS(ctx context.Context, id string) error {
	idx, ok := b.firstModemIndex(ctx)
	if !ok {
		return errNoModem
	}
	if _, err := strconv.Atoi(id); err != nil {
		return fmt.Errorf("sms: bad id %q", id)
	}
	return b.r.runOK(ctx, "mmcli", "-m", idx, "--messaging-delete-sms="+id)
}

// parseCreatedSMSPath extracts the created SMS object path from mmcli's
// "Successfully created new SMS: /org/.../SMS/7" output.
func parseCreatedSMSPath(out string) string {
	for _, f := range strings.Fields(out) {
		if strings.Contains(f, "/SMS/") {
			return f
		}
	}
	return ""
}

// sanitizeSMSField strips characters that would break the single-quoted
// mmcli create string.
func sanitizeSMSField(s string) string {
	return strings.TrimSpace(strings.NewReplacer("'", "", "\n", " ", "\r", " ").Replace(s))
}

// unescapeGLib reverses the escaping mmcli's -K output applies to
// non-ASCII text (GLib g_strescape): non-printable/high bytes become
// octal "\NNN", plus the usual "\n\t\r\\\"" escapes. Cyrillic SMS arrive
// as UTF-8 bytes shown that way (e.g. "\320\240"); decoding back to raw
// bytes reconstructs the UTF-8 string.
func unescapeGLib(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}
	var b []byte
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b = append(b, s[i])
			continue
		}
		c := s[i+1]
		switch {
		case c >= '0' && c <= '7':
			// Up to 3 octal digits.
			j, val := i+1, 0
			for j < len(s) && j < i+4 && s[j] >= '0' && s[j] <= '7' {
				val = val*8 + int(s[j]-'0')
				j++
			}
			b = append(b, byte(val))
			i = j - 1
		case c == 'n':
			b = append(b, '\n')
			i++
		case c == 't':
			b = append(b, '\t')
			i++
		case c == 'r':
			b = append(b, '\r')
			i++
		case c == '\\' || c == '"' || c == '\'':
			b = append(b, c)
			i++
		default:
			b = append(b, '\\')
		}
	}
	return string(b)
}

// ModemNetwork reports the modem's access-tech and band capabilities plus
// the current selection.
func (b *LinuxBackend) ModemNetwork(ctx context.Context) (network.ModemNetwork, error) {
	idx, ok := b.firstModemIndex(ctx)
	if !ok {
		return network.ModemNetwork{}, errNoModem
	}
	kv, err := b.mmcliKV(ctx, "-m", idx)
	if err != nil {
		return network.ModemNetwork{}, err
	}
	// supported-modes.value[N] : "allowed: 2g, 3g; preferred: 3g"
	techSet := map[string]bool{}
	for k, v := range kv {
		if strings.HasPrefix(k, "modem.generic.supported-modes.value") {
			for _, t := range allowedTechs(v) {
				techSet[t] = true
			}
		}
	}
	return network.ModemNetwork{
		SupportedModes: sortedTechs(techSet),
		CurrentModes:   allowedTechs(kv["modem.generic.current-modes"]),
		SupportedBands: collectValues(kv, "modem.generic.supported-bands.value"),
		CurrentBands:   collectValues(kv, "modem.generic.current-bands.value"),
	}, nil
}

// SetNetworkModes sets the allowed access techs. Empty selects everything
// the modem supports (i.e. "auto"). Changing modes may drop the current
// connection; the watchdog reconnects.
func (b *LinuxBackend) SetNetworkModes(ctx context.Context, modes []string) error {
	idx, ok := b.firstModemIndex(ctx)
	if !ok {
		return errNoModem
	}
	if len(modes) == 0 {
		// "auto" — allow every supported tech.
		net, err := b.ModemNetwork(ctx)
		if err != nil {
			return err
		}
		modes = net.SupportedModes
	}
	if len(modes) == 0 {
		return fmt.Errorf("no access modes to set")
	}
	return b.r.runOK(ctx, "mmcli", "-m", idx, "--set-current-modes="+strings.Join(modes, "|"))
}

// SetBands locks the modem to the given bands. Empty ("any") unlocks.
func (b *LinuxBackend) SetBands(ctx context.Context, bands []string) error {
	idx, ok := b.firstModemIndex(ctx)
	if !ok {
		return errNoModem
	}
	val := "any"
	if len(bands) > 0 {
		val = strings.Join(bands, "|")
	}
	return b.r.runOK(ctx, "mmcli", "-m", idx, "--set-current-bands="+val)
}

// allowedTechs pulls the tech tokens from the "allowed:" part of a
// ModemManager modes string, e.g. "allowed: 2g, 3g; preferred: 3g".
func allowedTechs(s string) []string {
	if s == "" {
		return nil
	}
	// Drop the "preferred: ..." clause.
	if i := strings.Index(s, ";"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "allowed:"))
	var out []string
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" && t != "any" {
			out = append(out, t)
		}
	}
	return out
}

// sortedTechs returns the tech set ordered 2g<3g<4g<5g, unknowns last.
func sortedTechs(set map[string]bool) []string {
	order := map[string]int{"2g": 0, "3g": 1, "4g": 2, "5g": 3}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		oi, oj := order[out[i]], order[out[j]]
		if oi != oj {
			return oi < oj
		}
		return out[i] < out[j]
	})
	return out
}

// collectValues gathers all "<prefix>[N] : value" entries from a KV map.
func collectValues(kv map[string]string, prefix string) []string {
	var out []string
	for k, v := range kv {
		if strings.HasPrefix(k, prefix) && v != "" && v != "any" {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}
