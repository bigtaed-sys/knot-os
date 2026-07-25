//go:build linux

package linux

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestParseUSSDResponse(t *testing.T) {
	// "response:" label
	if got := parseUSSDResponse("initiated new USSD session:\n    response: 'Balance 123.45 RUB'\n"); got != "Balance 123.45 RUB" {
		t.Errorf("response label: got %q", got)
	}
	// "new reply from network:" label (real mmcli output)
	if got := parseUSSDResponse("USSD session initiated; new reply from network: 'Уважаемый Клиент'"); got != "Уважаемый Клиент" {
		t.Errorf("reply label: got %q", got)
	}
	// No quotes → whole trimmed output.
	if got := parseUSSDResponse("  plain text  "); got != "plain text" {
		t.Errorf("fallback got %q", got)
	}
}

func TestUnescapeGLib(t *testing.T) {
	// \320\240 = 0xD0 0xA0 = "Р", \320\276 = 0xD0 0xBE = "о".
	if got := unescapeGLib(`\320\240\320\276`); got != "Ро" {
		t.Errorf("cyrillic: got %q", got)
	}
	if got := unescapeGLib(`hi\n\tthere`); got != "hi\n\tthere" {
		t.Errorf("named escapes: got %q", got)
	}
	if got := unescapeGLib("plain ascii"); got != "plain ascii" {
		t.Errorf("ascii: got %q", got)
	}
}

func TestSendUSSD(t *testing.T) {
	f := &fakeRunner{respond: func(name string, args []string) (string, error) {
		if strings.Contains(strings.Join(args, " "), "--3gpp-ussd-initiate") {
			return "response: 'You have 50 GB'", nil
		}
		if strings.Join(args, " ") == "-L -K" {
			return modemListOut, nil
		}
		return "", nil
	}}
	resp, err := newTestBackend(f).SendUSSD(context.Background(), "*100#")
	if err != nil || resp != "You have 50 GB" {
		t.Fatalf("got (%q, %v)", resp, err)
	}
	if !f.called("--3gpp-ussd-cancel") {
		t.Error("USSD session should be cancelled after initiate")
	}
}

func TestListSMS(t *testing.T) {
	f := &fakeRunner{respond: mapResponder(map[string]string{
		"mmcli -L -K": modemListOut,
		"mmcli -m 0 --messaging-list-sms -K": `modem.messaging.sms.value[1] : /org/freedesktop/ModemManager1/SMS/2
modem.messaging.sms.value[2] : /org/freedesktop/ModemManager1/SMS/5
`,
		// Received, Cyrillic text octal-escaped by mmcli -K ("Ро").
		"mmcli -s 2 -K": "sms.content.number : +70001112233\nsms.content.text : \\320\\240\\320\\276\nsms.pdu-type : deliver\nsms.properties.timestamp : 2026-07-25T10:00:00+03:00\n",
		// Sent, direction reported only via properties.state.
		"mmcli -s 5 -K": "sms.content.number : +70009998877\nsms.content.text : sent one\nsms.properties.state : sent\n",
	})}
	msgs, err := newTestBackend(f).ListSMS(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages", len(msgs))
	}
	// Newest first → id 5 before id 2.
	if msgs[0].ID != "5" || !msgs[0].Sent {
		t.Errorf("first = %+v, want id 5 sent", msgs[0])
	}
	if msgs[1].Sent {
		t.Errorf("id 2 should be received, got sent")
	}
	if msgs[1].Text != "Ро" {
		t.Errorf("cyrillic not decoded: got %q", msgs[1].Text)
	}
}

func TestSendSMS_SanitizesAndSends(t *testing.T) {
	var createArgs string
	f := &fakeRunner{respond: func(name string, args []string) (string, error) {
		joined := strings.Join(args, " ")
		switch {
		case joined == "-L -K":
			return modemListOut, nil
		case strings.Contains(joined, "--messaging-create-sms"):
			createArgs = joined
			return "Successfully created new SMS: /org/freedesktop/ModemManager1/SMS/9", nil
		}
		return "", nil
	}}
	err := newTestBackend(f).SendSMS(context.Background(), "+7000'111", "he'llo")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(createArgs, "'he'llo'") || strings.Contains(createArgs, "+7000'111") {
		t.Errorf("quotes not sanitized: %q", createArgs)
	}
	if !f.called("mmcli -s /org/freedesktop/ModemManager1/SMS/9 --send") {
		t.Errorf("created SMS not sent; calls: %v", f.calls)
	}
}

func TestDeleteSMS_RejectsNonNumericID(t *testing.T) {
	f := &fakeRunner{respond: mapResponder(map[string]string{"mmcli -L -K": modemListOut})}
	if err := newTestBackend(f).DeleteSMS(context.Background(), "../evil"); err == nil {
		t.Error("non-numeric SMS id should be rejected")
	}
}

func TestModemNetwork(t *testing.T) {
	f := &fakeRunner{respond: mapResponder(map[string]string{
		"mmcli -L -K": modemListOut,
		"mmcli -m 0 -K": `modem.generic.supported-modes.value[1] : allowed: 2g; preferred: none
modem.generic.supported-modes.value[2] : allowed: 3g, 4g; preferred: 4g
modem.generic.current-modes : allowed: 4g; preferred: none
modem.generic.supported-bands.value[1] : eutran-1
modem.generic.supported-bands.value[2] : eutran-3
modem.generic.current-bands.value[1] : eutran-1
`,
	})}
	net, err := newTestBackend(f).ModemNetwork(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(net.SupportedModes, []string{"2g", "3g", "4g"}) {
		t.Errorf("supported modes = %v", net.SupportedModes)
	}
	if !reflect.DeepEqual(net.CurrentModes, []string{"4g"}) {
		t.Errorf("current modes = %v", net.CurrentModes)
	}
	if !reflect.DeepEqual(net.SupportedBands, []string{"eutran-1", "eutran-3"}) {
		t.Errorf("supported bands = %v", net.SupportedBands)
	}
}

func TestSetNetworkModes(t *testing.T) {
	f := &fakeRunner{respond: mapResponder(map[string]string{"mmcli -L -K": modemListOut})}
	if err := newTestBackend(f).SetNetworkModes(context.Background(), []string{"4g"}); err != nil {
		t.Fatal(err)
	}
	if !f.called("--set-current-modes=4g") {
		t.Errorf("calls: %v", f.calls)
	}
}

func TestSetBands(t *testing.T) {
	f := &fakeRunner{respond: mapResponder(map[string]string{"mmcli -L -K": modemListOut})}
	b := newTestBackend(f)
	if err := b.SetBands(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if !f.called("--set-current-bands=any") {
		t.Errorf("empty bands should unlock to 'any'; calls: %v", f.calls)
	}
	if err := b.SetBands(context.Background(), []string{"eutran-1", "eutran-3"}); err != nil {
		t.Fatal(err)
	}
	if !f.called("--set-current-bands=eutran-1|eutran-3") {
		t.Errorf("calls: %v", f.calls)
	}
}
