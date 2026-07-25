//go:build linux

package linux

import (
	"context"
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/knot-os/knot-os/core/internal/config"
)

// fakeRunner is a commandRunner that returns scripted output instead of
// shelling out, and records every invocation for assertions.
type fakeRunner struct {
	// respond returns (stdout, err) for a command. nil → ("", nil).
	respond func(name string, args []string) (string, error)
	calls   []string
}

func (f *fakeRunner) run(_ context.Context, name string, args ...string) (string, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if f.respond == nil {
		return "", nil
	}
	return f.respond(name, args)
}
func (f *fakeRunner) runOK(ctx context.Context, name string, args ...string) error {
	_, err := f.run(ctx, name, args...)
	return err
}
func (f *fakeRunner) runIgnoreError(ctx context.Context, name string, args ...string) {
	_, _ = f.run(ctx, name, args...)
}

// called reports whether any recorded invocation contains sub.
func (f *fakeRunner) called(sub string) bool {
	for _, c := range f.calls {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}

func newTestBackend(f *fakeRunner) *LinuxBackend {
	return &LinuxBackend{logger: log.New(io.Discard, "", 0), r: f}
}

// mapResponder answers -K reads from a "name arg...":output map and
// returns ("", nil) for anything else (so runOK/runIgnoreError succeed).
func mapResponder(m map[string]string) func(string, []string) (string, error) {
	return func(name string, args []string) (string, error) {
		if out, ok := m[name+" "+strings.Join(args, " ")]; ok {
			return out, nil
		}
		return "", nil
	}
}

const (
	modemListOut = "modem-list.value[1] : /org/freedesktop/ModemManager1/Modem/0\n"

	modemConnectedOut = `modem.generic.state                     : connected
modem.3gpp.operator-name                : Tele2
modem.generic.manufacturer              : QUALCOMM INCORPORATED
modem.generic.model                     : QUECTEL EC25
modem.generic.signal-quality.value      : 71
modem.generic.access-technologies.value : lte
modem.generic.unlock-required           : --
modem.generic.bearers.value[1]          : /org/freedesktop/ModemManager1/Bearer/0
`
	bearerDHCPOut = `bearer.status.interface   : wwan0
bearer.ipv4-config.method : dhcp
`
	bearerStaticOut = `bearer.status.interface    : wwan0
bearer.ipv4-config.method  : static
bearer.ipv4-config.address : 10.64.64.64
bearer.ipv4-config.prefix  : 30
bearer.ipv4-config.gateway : 10.64.64.65
`
)

func TestMmcliKV_ParsesAndFiltersDashes(t *testing.T) {
	f := &fakeRunner{respond: mapResponder(map[string]string{
		"mmcli -m 0 -K": "modem.generic.state : connected\nmodem.generic.unlock-required : --\n",
	})}
	b := newTestBackend(f)
	kv, err := b.mmcliKV(context.Background(), "-m", "0")
	if err != nil {
		t.Fatal(err)
	}
	if kv["modem.generic.state"] != "connected" {
		t.Errorf("state = %q", kv["modem.generic.state"])
	}
	if _, ok := kv["modem.generic.unlock-required"]; ok {
		t.Error(`"--" value should be filtered out`)
	}
}

func TestFirstModemIndex(t *testing.T) {
	present := &fakeRunner{respond: mapResponder(map[string]string{"mmcli -L -K": modemListOut})}
	if idx, ok := newTestBackend(present).firstModemIndex(context.Background()); !ok || idx != "0" {
		t.Errorf("present: got (%q,%v), want (0,true)", idx, ok)
	}
	empty := &fakeRunner{respond: mapResponder(map[string]string{"mmcli -L -K": "\n"})}
	if _, ok := newTestBackend(empty).firstModemIndex(context.Background()); ok {
		t.Error("empty list should report no modem")
	}
}

func TestModemStatus_Connected(t *testing.T) {
	f := &fakeRunner{respond: mapResponder(map[string]string{
		"mmcli -L -K":   modemListOut,
		"mmcli -m 0 -K": modemConnectedOut,
		"mmcli -b 0 -K": bearerDHCPOut,
	})}
	b := newTestBackend(f)
	b.setModemErr("stale error from a past failure")

	st, err := b.ModemStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !st.Present || st.State != "connected" {
		t.Fatalf("present=%v state=%q", st.Present, st.State)
	}
	if st.Operator != "Tele2" || st.Model != "QUECTEL EC25" || st.Tech != "lte" {
		t.Errorf("operator/model/tech = %q/%q/%q", st.Operator, st.Model, st.Tech)
	}
	if st.SignalPercent != 71 {
		t.Errorf("signal = %d", st.SignalPercent)
	}
	if st.Interface != "wwan0" {
		t.Errorf("interface = %q", st.Interface)
	}
	if st.LastError != "" {
		t.Errorf("a connected modem must not surface a stale error, got %q", st.LastError)
	}
}

func TestModemStatus_NotConnectedSurfacesError(t *testing.T) {
	f := &fakeRunner{respond: mapResponder(map[string]string{
		"mmcli -L -K":   modemListOut,
		"mmcli -m 0 -K": "modem.generic.state : failed\n",
	})}
	b := newTestBackend(f)
	b.setModemErr("modem in failed state (sim-missing)")

	st, _ := b.ModemStatus(context.Background())
	if st.LastError != "modem in failed state (sim-missing)" {
		t.Errorf("LastError = %q", st.LastError)
	}
}

func TestModemStatus_NoModem(t *testing.T) {
	f := &fakeRunner{respond: mapResponder(map[string]string{"mmcli -L -K": "\n"})}
	st, err := newTestBackend(f).ModemStatus(context.Background())
	if err != nil || st.Present {
		t.Errorf("no modem: present=%v err=%v", st.Present, err)
	}
}

func TestSimSlotInfo(t *testing.T) {
	single := &fakeRunner{respond: mapResponder(map[string]string{
		"mmcli -m 0 -K": "modem.generic.state : connected\n",
	})}
	if c, p := newTestBackend(single).simSlotInfo(context.Background(), "0"); c != 0 || p != 0 {
		t.Errorf("single-slot: got (%d,%d), want (0,0)", c, p)
	}

	// Two slots (second empty "--"), primary 1 — the empty slot must still
	// be counted so a switch to slot 2 isn't rejected.
	multi := &fakeRunner{respond: mapResponder(map[string]string{
		"mmcli -m 0 -K": `modem.generic.sim-slots.value[1] : /org/freedesktop/ModemManager1/SIM/0
modem.generic.sim-slots.value[2] : --
modem.generic.primary-sim-slot   : 1
`,
	})}
	if c, p := newTestBackend(multi).simSlotInfo(context.Background(), "0"); c != 2 || p != 1 {
		t.Errorf("multi-slot: got (%d,%d), want (2,1)", c, p)
	}
}

func TestSelectSIMSlot(t *testing.T) {
	multiKV := `modem.generic.sim-slots.value[1] : /org/freedesktop/ModemManager1/SIM/0
modem.generic.sim-slots.value[2] : /org/freedesktop/ModemManager1/SIM/1
modem.generic.primary-sim-slot   : 1
`
	t.Run("single-slot is inert", func(t *testing.T) {
		f := &fakeRunner{respond: mapResponder(map[string]string{
			"mmcli -m 0 -K": "modem.generic.state : connected\n",
		})}
		idx, err := newTestBackend(f).selectSIMSlot(context.Background(), "0", 2)
		if err != nil || idx != "0" {
			t.Fatalf("got (%q,%v)", idx, err)
		}
		if f.called("--set-primary-sim-slot") {
			t.Error("must not switch on a single-slot modem")
		}
	})

	t.Run("already active", func(t *testing.T) {
		f := &fakeRunner{respond: mapResponder(map[string]string{"mmcli -m 0 -K": multiKV})}
		idx, err := newTestBackend(f).selectSIMSlot(context.Background(), "0", 1)
		if err != nil || idx != "0" {
			t.Fatalf("got (%q,%v)", idx, err)
		}
		if f.called("--set-primary-sim-slot") {
			t.Error("no switch needed when already on the requested slot")
		}
	})

	t.Run("switches and waits for re-enumeration", func(t *testing.T) {
		f := &fakeRunner{respond: mapResponder(map[string]string{
			"mmcli -m 0 -K": multiKV,
			"mmcli -L -K":   modemListOut, // reappears after the switch
		})}
		idx, err := newTestBackend(f).selectSIMSlot(context.Background(), "0", 2)
		if err != nil || idx != "0" {
			t.Fatalf("got (%q,%v)", idx, err)
		}
		if !f.called("mmcli -m 0 --set-primary-sim-slot=2") {
			t.Errorf("expected a slot switch; calls: %v", f.calls)
		}
	})

	t.Run("rejects out-of-range slot", func(t *testing.T) {
		f := &fakeRunner{respond: mapResponder(map[string]string{"mmcli -m 0 -K": multiKV})}
		if _, err := newTestBackend(f).selectSIMSlot(context.Background(), "0", 3); err == nil {
			t.Error("slot 3 on a 2-slot modem should error")
		}
	})
}

func TestConnectModem_FailedStateReportsReason(t *testing.T) {
	f := &fakeRunner{respond: mapResponder(map[string]string{
		"mmcli -L -K":   modemListOut,
		"mmcli -m 0 -K": "modem.generic.state : failed\nmodem.generic.state-failed-reason : sim-missing\n",
	})}
	_, _, err := newTestBackend(f).connectModem(context.Background(), &config.Modem{})
	if err == nil || !strings.Contains(err.Error(), "sim-missing") {
		t.Fatalf("err = %v, want one naming sim-missing", err)
	}
	if f.called("--simple-connect") {
		t.Error("must not attempt to connect a failed modem")
	}
}

func TestConnectModem_SuccessDHCP(t *testing.T) {
	f := &fakeRunner{respond: mapResponder(map[string]string{
		"mmcli -L -K":   modemListOut,
		"mmcli -m 0 -K": modemConnectedOut,
		"mmcli -b 0 -K": bearerDHCPOut,
	})}
	iface, dhcp, err := newTestBackend(f).connectModem(context.Background(), &config.Modem{APN: "internet"})
	if err != nil {
		t.Fatal(err)
	}
	if iface != "wwan0" || !dhcp {
		t.Errorf("got (%q, dhcp=%v), want (wwan0, true)", iface, dhcp)
	}
	if !f.called("--simple-connect=apn=internet,ip-type=ipv4") {
		t.Errorf("APN not passed to simple-connect; calls: %v", f.calls)
	}
}

func TestConnectModem_SuccessStaticAppliesAddr(t *testing.T) {
	f := &fakeRunner{respond: mapResponder(map[string]string{
		"mmcli -L -K":   modemListOut,
		"mmcli -m 0 -K": modemConnectedOut,
		"mmcli -b 0 -K": bearerStaticOut,
	})}
	iface, dhcp, err := newTestBackend(f).connectModem(context.Background(), &config.Modem{})
	if err != nil {
		t.Fatal(err)
	}
	if iface != "wwan0" || dhcp {
		t.Errorf("got (%q, dhcp=%v), want (wwan0, false)", iface, dhcp)
	}
	if !f.called("ip addr add 10.64.64.64/30 dev wwan0") {
		t.Errorf("static bearer addr not applied; calls: %v", f.calls)
	}
}

func TestConnectModem_UnlocksPIN(t *testing.T) {
	lockedThenConnected := `modem.generic.state            : registered
modem.generic.unlock-required  : sim-pin
modem.generic.bearers.value[1] : /org/freedesktop/ModemManager1/Bearer/0
`
	f := &fakeRunner{respond: mapResponder(map[string]string{
		"mmcli -L -K":   modemListOut,
		"mmcli -m 0 -K": lockedThenConnected,
		"mmcli -b 0 -K": bearerDHCPOut,
	})}
	_, _, err := newTestBackend(f).connectModem(context.Background(), &config.Modem{PIN: "1234"})
	if err != nil {
		t.Fatal(err)
	}
	if !f.called("mmcli -i any --pin=1234") {
		t.Errorf("PIN not sent; calls: %v", f.calls)
	}
}

func TestConnectModem_SimpleConnectFailureFoldsInReason(t *testing.T) {
	f := &fakeRunner{respond: func(name string, args []string) (string, error) {
		joined := name + " " + strings.Join(args, " ")
		switch {
		case joined == "mmcli -L -K":
			return modemListOut, nil
		case strings.Contains(joined, "--simple-connect"):
			return "", context.DeadlineExceeded // any failure
		case joined == "mmcli -m 0 -K":
			// Not failed at the pre-check, but state-failed-reason is set
			// once the connect attempt fails.
			return "modem.generic.state : registered\nmodem.generic.state-failed-reason : no-service\n", nil
		}
		return "", nil
	}}
	_, _, err := newTestBackend(f).connectModem(context.Background(), &config.Modem{})
	if err == nil || !strings.Contains(err.Error(), "no-service") {
		t.Fatalf("err = %v, want one naming no-service", err)
	}
}

func TestModemActionFor(t *testing.T) {
	cases := map[string]modemAction{
		"connected":     modemNoAction,
		"failed":        modemReset,
		"disabled":      modemReconnect,
		"registered":    modemReconnect,
		"searching":     modemReconnect,
		"disconnecting": modemReconnect,
		"":              modemReconnect,
	}
	for state, want := range cases {
		if got := modemActionFor(state); got != want {
			t.Errorf("modemActionFor(%q) = %d, want %d", state, got, want)
		}
	}
}

func TestModemWatchOnce_ConnectedClearsError(t *testing.T) {
	f := &fakeRunner{respond: mapResponder(map[string]string{
		"mmcli -L -K":   modemListOut,
		"mmcli -m 0 -K": modemConnectedOut,
		"mmcli -b 0 -K": bearerDHCPOut,
	})}
	b := newTestBackend(f)
	b.setModemErr("stale")
	var lastReset time.Time
	b.modemWatchOnce(context.Background(), &lastReset)
	if b.lastModemErr() != "" {
		t.Errorf("connected tick should clear the error, got %q", b.lastModemErr())
	}
}

func TestModemWatchOnce_FailedResetsRateLimited(t *testing.T) {
	failed := "modem.generic.state : failed\nmodem.generic.state-failed-reason : sim-missing\n"
	newFake := func() *fakeRunner {
		return &fakeRunner{respond: mapResponder(map[string]string{
			"mmcli -L -K":   modemListOut,
			"mmcli -m 0 -K": failed,
		})}
	}

	t.Run("resets when cooldown elapsed", func(t *testing.T) {
		f := newFake()
		b := newTestBackend(f)
		var lastReset time.Time // zero → cooldown long past
		b.modemWatchOnce(context.Background(), &lastReset)
		if !f.called("mmcli -m 0 --reset") {
			t.Errorf("expected a reset; calls: %v", f.calls)
		}
		if lastReset.IsZero() {
			t.Error("lastReset should be stamped after a reset")
		}
		if !strings.Contains(b.lastModemErr(), "sim-missing") {
			t.Errorf("failed reason not surfaced: %q", b.lastModemErr())
		}
	})

	t.Run("skips reset within cooldown", func(t *testing.T) {
		f := newFake()
		b := newTestBackend(f)
		lastReset := time.Now() // just reset → still cooling down
		b.modemWatchOnce(context.Background(), &lastReset)
		if f.called("--reset") {
			t.Error("must not reset again within the cooldown window")
		}
	})
}

func TestFailedStateHint(t *testing.T) {
	if !strings.Contains(failedStateHint("sim-missing"), "not detected") {
		t.Error("sim-missing hint")
	}
	if failedStateHint("whatever") == "" {
		t.Error("default hint should be non-empty")
	}
	if orUnknown("") != "unknown" || orUnknown("x") != "x" {
		t.Error("orUnknown")
	}
}
