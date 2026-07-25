//go:build linux

package linux

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/knot-os/knot-os/core/internal/config"
	"github.com/knot-os/knot-os/core/internal/network"
)

// Cellular (USB modem) WAN support, driven via ModemManager's `mmcli`.
//
// This is the "modem" WAN mode: instead of a fixed Ethernet interface,
// knotd asks ModemManager to connect the modem (unlock the SIM, attach
// with the carrier APN), then uses the resulting data netdev (wwan0…)
// as the WAN — NAT, DHCP-serving and the AP are identical to the
// Ethernet path from there on.
//
// EXPERIMENTAL: developed without modem hardware on hand. mmcli output
// is parsed in key-value mode (`-K`) for stability, but real modems
// vary; expect to tune the connect/IP-config path against actual gear.

// mmcliKV runs `mmcli <args> -K` and parses the `key : value` output
// into a map. ModemManager's -K output is the machine-readable form,
// far more stable than the pretty tree.
func (b *LinuxBackend) mmcliKV(ctx context.Context, args ...string) (map[string]string, error) {
	out, err := b.r.run(ctx, "mmcli", append(args, "-K")...)
	if err != nil {
		return nil, err
	}
	kv := make(map[string]string)
	for _, line := range strings.Split(out, "\n") {
		i := strings.Index(line, ":")
		if i < 0 {
			continue
		}
		key := strings.TrimSpace(line[:i])
		val := strings.TrimSpace(line[i+1:])
		if key != "" && val != "--" {
			kv[key] = val
		}
	}
	return kv, nil
}

// firstModemIndex returns the index of the first modem ManagerManager
// knows about, or ok=false when there is none.
func (b *LinuxBackend) firstModemIndex(ctx context.Context) (string, bool) {
	kv, err := b.mmcliKV(ctx, "-L")
	if err != nil {
		return "", false
	}
	// modem-list.value[1] : /org/freedesktop/ModemManager1/Modem/0
	for k, v := range kv {
		if strings.HasPrefix(k, "modem-list.value") {
			if idx := v[strings.LastIndex(v, "/")+1:]; idx != "" {
				return idx, true
			}
		}
	}
	return "", false
}

// ModemStatus reports the live cellular state for the API/UI. Safe to
// call any time; returns Present=false when no modem or no mmcli.
func (b *LinuxBackend) ModemStatus(ctx context.Context) (network.ModemStatus, error) {
	idx, ok := b.firstModemIndex(ctx)
	if !ok {
		return network.ModemStatus{Present: false}, nil
	}
	kv, err := b.mmcliKV(ctx, "-m", idx)
	if err != nil {
		return network.ModemStatus{Present: false}, nil
	}
	st := network.ModemStatus{
		Present:      true,
		State:        kv["modem.generic.state"],
		Operator:     kv["modem.3gpp.operator-name"],
		Manufacturer: kv["modem.generic.manufacturer"],
		Model:        kv["modem.generic.model"],
		LockRequired: kv["modem.generic.unlock-required"],
	}
	if v := kv["modem.generic.signal-quality.value"]; v != "" {
		st.SignalPercent, _ = strconv.Atoi(v)
	}
	// access-technologies.value can be a list ("lte" or "lte, ...").
	if v := kv["modem.generic.access-technologies.value"]; v != "" {
		st.Tech = strings.SplitN(v, ",", 2)[0]
	}
	// SIM slots: only modems that expose more than one are switchable.
	st.SIMSlots, st.PrimarySlot = b.simSlotInfo(ctx, idx)
	// Resolve the data interface from the active bearer, if connected.
	if bidx, ok := bearerIndex(kv); ok {
		if bkv, err := b.mmcliKV(ctx, "-b", bidx); err == nil {
			st.Interface = bkv["bearer.status.interface"]
		}
	}
	// Surface the reason the last connect attempt failed so the UI can
	// show it instead of a bare "failed". Once the modem is actually
	// connected, a stale error is irrelevant — suppress it.
	if st.State != "connected" {
		st.LastError = b.lastModemErr()
	}
	return st, nil
}

// simSlotInfo returns how many SIM slots the modem at idx exposes and
// which is currently primary (1-based). Counts every sim-slots entry,
// including empty ones ("--"), so it can't undercount a modem whose
// second slot has no SIM. Returns (0, 0) for single-slot modems, which
// don't report the sim-slots keys at all.
func (b *LinuxBackend) simSlotInfo(ctx context.Context, idx string) (count, primary int) {
	// Read raw (not via mmcliKV, which filters "--" values) so empty
	// slots still count toward the total.
	out, err := b.r.run(ctx, "mmcli", "-m", idx, "-K")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(out, "\n") {
		i := strings.Index(line, ":")
		if i < 0 {
			continue
		}
		key := strings.TrimSpace(line[:i])
		val := strings.TrimSpace(line[i+1:])
		switch {
		case strings.HasPrefix(key, "modem.generic.sim-slots.value"):
			count++
		case key == "modem.generic.primary-sim-slot" && val != "--":
			primary, _ = strconv.Atoi(val)
		}
	}
	return count, primary
}

// selectSIMSlot switches the modem to the requested 1-based SIM slot when
// the modem exposes more than one. Switching resets the modem, which
// re-enumerates (often under a new ModemManager index), so it returns the
// index to keep using. On single-slot modems the request is inert (not an
// error) — the setting simply does nothing.
func (b *LinuxBackend) selectSIMSlot(ctx context.Context, idx string, slot int) (string, error) {
	count, primary := b.simSlotInfo(ctx, idx)
	if count <= 1 {
		return idx, nil // single-slot modem — nothing to switch
	}
	if slot > count {
		return "", fmt.Errorf("SIM slot %d requested but modem exposes %d slot(s)", slot, count)
	}
	if slot == primary {
		return idx, nil // already active
	}
	b.logger.Printf("modem: switching to SIM slot %d (was %d)", slot, primary)
	if err := b.r.runOK(ctx, "mmcli", "-m", idx, fmt.Sprintf("--set-primary-sim-slot=%d", slot)); err != nil {
		return "", fmt.Errorf("switch SIM slot to %d: %w", slot, err)
	}
	// The switch resets the modem; wait for it to re-appear.
	newIdx, err := b.waitForModem(ctx, 30*time.Second)
	if err != nil {
		return "", fmt.Errorf("modem did not return after SIM slot switch: %w", err)
	}
	return newIdx, nil
}

// waitForModem polls until ModemManager sees a modem again, or d elapses.
func (b *LinuxBackend) waitForModem(ctx context.Context, d time.Duration) (string, error) {
	deadline := time.Now().Add(d)
	for {
		if idx, ok := b.firstModemIndex(ctx); ok {
			return idx, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timeout after %s", d)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// modemFailedReason reads ModemManager's state-failed-reason for the
// modem at idx, or "" when there's nothing useful. Used to enrich a
// bare connect error with the modem's own diagnosis.
func (b *LinuxBackend) modemFailedReason(ctx context.Context, idx string) string {
	kv, err := b.mmcliKV(ctx, "-m", idx)
	if err != nil {
		return ""
	}
	r := kv["modem.generic.state-failed-reason"]
	if r == "none" {
		return ""
	}
	return r
}

// orUnknown returns s, or "unknown" when empty — so error strings never
// read "modem in failed state ()".
func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// failedStateHint maps ModemManager's state-failed-reason to an
// actionable hint. Most "failed" states on a USB modem are a SIM the
// module can't read — common when the modem sits behind a mini-PCIe/M.2
// -> USB adapter whose SIM slot is finicky.
func failedStateHint(reason string) string {
	switch reason {
	case "sim-missing":
		return "the SIM is not detected — check it's inserted the right way and seated in the slot"
	case "sim-error":
		return "the SIM was found but couldn't be read — reseat it; if it persists the SIM or slot may be faulty"
	default:
		return "reseat the SIM and power-cycle the modem (or run: mmcli -m 0 --reset)"
	}
}

// bearerIndex extracts the first bearer index from a modem's KV map.
func bearerIndex(modemKV map[string]string) (string, bool) {
	for k, v := range modemKV {
		if strings.HasPrefix(k, "modem.generic.bearers.value") {
			if idx := v[strings.LastIndex(v, "/")+1:]; idx != "" {
				return idx, true
			}
		}
	}
	return "", false
}

// connectModem unlocks + connects the modem via ModemManager and returns
// the data interface plus whether the caller should run a DHCP client on
// it (false means connectModem already applied a static address from the
// bearer). Idempotent-ish: connecting an already-connected modem is fine.
func (b *LinuxBackend) connectModem(ctx context.Context, m *config.Modem) (iface string, needsDHCP bool, err error) {
	if m == nil {
		m = &config.Modem{}
	}
	idx, ok := b.firstModemIndex(ctx)
	if !ok {
		return "", false, fmt.Errorf("no cellular modem found (is it plugged in and in modem mode?)")
	}

	// Select the requested SIM slot on multi-slot modems. Done first: the
	// switch resets the modem, so everything below must run against the
	// re-enumerated index it returns. Inert on single-slot hardware.
	if m.SIMSlot > 0 {
		newIdx, err := b.selectSIMSlot(ctx, idx, m.SIMSlot)
		if err != nil {
			return "", false, err
		}
		idx = newIdx
	}

	// A modem in the "failed" state can't be enabled or connected — every
	// downstream call returns a cryptic error ("modem has no Simple
	// capabilities"). Catch it here and report the modem's own reason,
	// which almost always names the true cause (SIM not detected). A reset
	// (`mmcli -m N --reset`) or reseating the SIM is the fix, but that's a
	// slow, physical action we don't do implicitly mid-apply.
	if kv, err := b.mmcliKV(ctx, "-m", idx); err == nil {
		if kv["modem.generic.state"] == "failed" {
			reason := kv["modem.generic.state-failed-reason"]
			return "", false, fmt.Errorf("modem in failed state (%s): %s",
				orUnknown(reason), failedStateHint(reason))
		}
	}

	// Enable the modem (no-op if already enabled).
	b.r.runIgnoreError(ctx, "mmcli", "-m", idx, "--enable")

	// Unlock the SIM if a PIN is required and one is configured.
	if kv, err := b.mmcliKV(ctx, "-m", idx); err == nil {
		if kv["modem.generic.unlock-required"] == "sim-pin" && m.PIN != "" {
			b.r.runIgnoreError(ctx, "mmcli", "-i", "any", "--pin="+m.PIN)
		}
	}

	// Build the simple-connect string. An empty APN lets MM try the
	// carrier default.
	parts := []string{"ip-type=ipv4"}
	if m.APN != "" {
		parts = append([]string{"apn=" + m.APN}, parts...)
	}
	if m.Username != "" {
		parts = append(parts, "user="+m.Username)
	}
	if m.Password != "" {
		parts = append(parts, "password="+m.Password)
	}
	if err := b.r.runOK(ctx, "mmcli", "-m", idx, "--simple-connect="+strings.Join(parts, ",")); err != nil {
		// mmcli's simple-connect stderr is often terse ("couldn't connect
		// the modem"). The modem's own state-failed-reason usually names
		// the real cause (e.g. "no-service", "sim-missing", "unknown"),
		// so fold it in when available.
		reason := b.modemFailedReason(ctx, idx)
		if reason != "" {
			return "", false, fmt.Errorf("modem connect (%s): %w", reason, err)
		}
		return "", false, fmt.Errorf("modem connect: %w", err)
	}

	// Give ModemManager a moment to bring up the bearer, then read it.
	var bkv map[string]string
	for attempt := 0; attempt < 10; attempt++ {
		mkv, err := b.mmcliKV(ctx, "-m", idx)
		if err == nil {
			if bidx, ok := bearerIndex(mkv); ok {
				if got, err := b.mmcliKV(ctx, "-b", bidx); err == nil && got["bearer.status.interface"] != "" {
					bkv = got
					break
				}
			}
		}
		time.Sleep(time.Second)
	}
	if bkv == nil {
		return "", false, fmt.Errorf("modem connected but no data bearer appeared")
	}

	iface = bkv["bearer.status.interface"]
	if iface == "" {
		return "", false, fmt.Errorf("modem bearer has no interface")
	}

	// Keep the USB modem awake. Linux USB autosuspend puts an idle
	// modem into low power; on many LTE sticks that drops the control
	// port and ModemManager transitions the modem to "disabled" —
	// internet works for a while, then dies until the next apply. Pin
	// the whole USB device chain to "on" so it never suspends.
	b.keepModemAwake(iface)

	// QMI modems usually run the netdev in raw-ip mode; ModemManager
	// sets that up. The bearer tells us whether to DHCP or apply a
	// static address it already negotiated (typical for QMI).
	method := bkv["bearer.ipv4-config.method"]
	if method == "static" {
		if err := b.applyBearerStatic(ctx, iface, bkv); err != nil {
			return "", false, err
		}
		return iface, false, nil
	}
	// "dhcp" (or unknown) → let the caller run dhclient.
	return iface, true, nil
}

// keepModemAwake disables USB autosuspend for the modem behind iface by
// writing "on" to power/control at every USB level from the netdev up to
// the sysfs root. Best-effort: failures are logged, never fatal (the
// modem still works — it may just autosuspend). The netdev's `device`
// symlink resolves into /sys/devices/.../usbN/.../<usb-iface>; each
// ancestor with a power/control file is a suspendable USB node.
func (b *LinuxBackend) keepModemAwake(iface string) {
	start, err := filepath.EvalSymlinks(filepath.Join("/sys/class/net", iface, "device"))
	if err != nil {
		b.logger.Printf("modem: keep-awake: resolve %s: %v", iface, err)
		return
	}
	n := 0
	for dir := start; strings.HasPrefix(dir, "/sys/devices"); dir = filepath.Dir(dir) {
		ctl := filepath.Join(dir, "power", "control")
		if _, err := os.Stat(ctl); err != nil {
			continue
		}
		if err := os.WriteFile(ctl, []byte("on"), 0o644); err == nil {
			n++
		}
	}
	b.logger.Printf("modem: USB autosuspend disabled on %d node(s) for %s", n, iface)
}

// applyBearerStatic applies the ModemManager-negotiated IPv4 config to
// the data interface and installs a default route via its gateway.
func (b *LinuxBackend) applyBearerStatic(ctx context.Context, iface string, bkv map[string]string) error {
	addr := bkv["bearer.ipv4-config.address"]
	prefix := bkv["bearer.ipv4-config.prefix"]
	gw := bkv["bearer.ipv4-config.gateway"]
	if addr == "" || prefix == "" {
		return fmt.Errorf("modem bearer static config incomplete (addr=%q prefix=%q)", addr, prefix)
	}
	b.addrFlush(ctx, iface)
	if err := b.r.runOK(ctx, "ip", "addr", "add", addr+"/"+prefix, "dev", iface); err != nil {
		return fmt.Errorf("modem addr: %w", err)
	}
	if err := b.linkUp(ctx, iface); err != nil {
		return err
	}
	if gw != "" {
		// Replace any existing default so the modem becomes the WAN.
		b.r.runIgnoreError(ctx, "ip", "route", "replace", "default", "via", gw, "dev", iface)
	}
	return nil
}
