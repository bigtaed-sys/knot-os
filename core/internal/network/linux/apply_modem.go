//go:build linux

package linux

import (
	"context"
	"fmt"
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
	// Resolve the data interface from the active bearer, if connected.
	if bidx, ok := bearerIndex(kv); ok {
		if bkv, err := b.mmcliKV(ctx, "-b", bidx); err == nil {
			st.Interface = bkv["bearer.status.interface"]
		}
	}
	return st, nil
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
