package capability

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// makeFakeSysfs builds a minimal /sys/class/net tree under root.
// Each entry in interfaces is interface-name → device-uevent body.
// A "device" symlink is added for each iface; its target controls
// whether the adapter is considered USB.
//
// Returns the sys/class/net path to feed into Probe.
func makeFakeSysfs(t *testing.T, interfaces map[string]struct {
	uevent  string
	devPath string // relative path from sys/class/net/<ifname>/ — empty skips the symlink
}) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink-heavy test, skip on Windows")
	}
	root := t.TempDir()
	sysClassNet := filepath.Join(root, "sys", "class", "net")
	if err := os.MkdirAll(sysClassNet, 0o755); err != nil {
		t.Fatal(err)
	}
	for ifname, spec := range interfaces {
		ifaceDir := filepath.Join(sysClassNet, ifname)
		if err := os.MkdirAll(ifaceDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if spec.devPath == "" {
			continue
		}
		// Create the target dir so the symlink points somewhere real.
		target := filepath.Join(root, "sys", spec.devPath)
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		// Drop the uevent file under the target.
		if err := os.WriteFile(filepath.Join(target, "uevent"),
			[]byte(spec.uevent), 0o644); err != nil {
			t.Fatal(err)
		}
		// Symlink iface/device -> target.
		if err := os.Symlink(target, filepath.Join(ifaceDir, "device")); err != nil {
			t.Fatal(err)
		}
	}
	return sysClassNet
}

// TestProbeReadsCarrier guards the v2026.05.2 fix: EthAdapter.Link
// must mirror /sys/class/net/<iface>/carrier so the setup wizard
// can tell whether the Ethernet cable is plugged in.
//
// Failure mode this catches: backend forgets to set Link on the
// struct → wizard always shows «no cable» → user can't pick
// router mode even with a working cable.
func TestProbeReadsCarrier(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink-heavy test, skip on Windows")
	}
	// Build a sysfs with TWO interfaces — one carrier=1 (cable in),
	// one carrier=0 (no cable). Both must be detected as USB adapters
	// but only the first should show Link=true.
	sysClassNet := makeFakeSysfs(t, map[string]struct {
		uevent, devPath string
	}{
		"eth0": {
			uevent: `DRIVER=r8152
PRODUCT=bda/8153/2000
`,
			devPath: "devices/platform/soc/usb1/2-1/2-1:1.0",
		},
		"eth1": {
			uevent: `DRIVER=r8152
PRODUCT=bda/8153/2000
`,
			devPath: "devices/platform/soc/usb1/2-2/2-2:1.0",
		},
	})
	// Sprinkle carrier files manually — the helper doesn't know about them.
	if err := os.WriteFile(filepath.Join(sysClassNet, "eth0", "carrier"),
		[]byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sysClassNet, "eth1", "carrier"),
		[]byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := Probe{SysClassNet: sysClassNet, ModelFile: "/nonexistent"}.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rep.Eth) != 2 {
		t.Fatalf("eth len=%d, want 2", len(rep.Eth))
	}
	// Order isn't guaranteed; index by interface name.
	byIf := map[string]EthAdapter{}
	for _, e := range rep.Eth {
		byIf[e.Interface] = e
	}
	if !byIf["eth0"].Link {
		t.Errorf("eth0 should have Link=true (carrier=1), got %+v", byIf["eth0"])
	}
	if byIf["eth1"].Link {
		t.Errorf("eth1 should have Link=false (carrier=0), got %+v", byIf["eth1"])
	}
	// Both must be flagged USB.
	if !byIf["eth0"].USB || !byIf["eth1"].USB {
		t.Errorf("USB flag should be true for both: %+v", rep.Eth)
	}
}

// TestProbeJSONUsesNameTag pins the JSON field name the wizard UI
// reads. Renaming `name` -> `interface` back would silently break
// the wizard (UI types expect `name`). Caught at marshal time.
func TestProbeJSONUsesNameTag(t *testing.T) {
	a := EthAdapter{Interface: "eth0", Driver: "r8152", Link: true, USB: true}
	js, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(js), `"name":"eth0"`) {
		t.Errorf("JSON missing name=eth0: %s", js)
	}
	if !strings.Contains(string(js), `"link":true`) {
		t.Errorf("JSON missing link=true: %s", js)
	}
	if !strings.Contains(string(js), `"usb":true`) {
		t.Errorf("JSON missing usb=true: %s", js)
	}
}

func TestProbeIdentifiesRTL8152(t *testing.T) {
	sysClassNet := makeFakeSysfs(t, map[string]struct {
		uevent, devPath string
	}{
		"eth0": {
			uevent: `DRIVER=r8152
PRODUCT=bda/8152/2000
INTERFACE=2
`,
			devPath: "devices/platform/soc/usb1/2-1/2-1:1.0",
		},
		"wlan0": { /* skipped by name filter */ },
	})

	rep, err := Probe{SysClassNet: sysClassNet, ModelFile: "/nonexistent"}.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !rep.RouterCapable {
		t.Error("RouterCapable should be true with a USB-Eth present")
	}
	if len(rep.Eth) != 1 {
		t.Fatalf("eth len=%d", len(rep.Eth))
	}
	got := rep.Eth[0]
	if got.Interface != "eth0" || got.Driver != "r8152" ||
		got.USBVendor != "0bda" || got.USBProduct != "8152" ||
		got.Model != "RTL8152 (USB 2.0 Fast Ethernet)" {
		t.Errorf("adapter: %+v", got)
	}
}

func TestProbeUnknownAdapterGetsGenericLabel(t *testing.T) {
	sysClassNet := makeFakeSysfs(t, map[string]struct {
		uevent, devPath string
	}{
		"eth0": {
			uevent: `DRIVER=cdc_ether
PRODUCT=ffff/aaaa/100
`,
			devPath: "devices/usb/something",
		},
	})
	rep, err := Probe{SysClassNet: sysClassNet, ModelFile: "/nonexistent"}.Run()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Eth[0].Model != "USB Ethernet (cdc_ether)" {
		t.Errorf("model=%q", rep.Eth[0].Model)
	}
}

func TestProbePiZero2WOTGPath(t *testing.T) {
	// Realistic device-link target on Pi Zero 2W: USB host controller
	// is the SoC's OTG IP, the dongle hangs off usb1.
	sysClassNet := makeFakeSysfs(t, map[string]struct {
		uevent, devPath string
	}{
		"eth0": {
			uevent: `DRIVER=r8152
PRODUCT=bda/8152/2000
INTERFACE=2
`,
			devPath: "devices/platform/soc/3f980000.usb/usb1/1-1/1-1:1.0",
		},
	})
	rep, err := Probe{SysClassNet: sysClassNet, ModelFile: "/nonexistent"}.Run()
	if err != nil {
		t.Fatal(err)
	}
	if !rep.RouterCapable || len(rep.Eth) != 1 || rep.Eth[0].Interface != "eth0" {
		t.Errorf("Zero 2W OTG path: %+v", rep)
	}
}

func TestProbeAcceptsUSBIDOnly(t *testing.T) {
	// Some kernels expose the uevent PRODUCT= line via the netdev's
	// own "device" symlink target, but the symlink target may not
	// contain a "usb" substring (e.g. it points straight at the
	// usb_interface dir whose path doesn't include "usb"). Make sure
	// we still classify those as USB based on the uevent alone.
	sysClassNet := makeFakeSysfs(t, map[string]struct {
		uevent, devPath string
	}{
		"eth0": {
			uevent:  "DRIVER=r8152\nPRODUCT=bda/8152/2000\n",
			devPath: "devices/foo/bar", // path without "usb" anywhere
		},
	})
	rep, err := Probe{SysClassNet: sysClassNet, ModelFile: "/nonexistent"}.Run()
	if err != nil {
		t.Fatal(err)
	}
	if !rep.RouterCapable {
		t.Errorf("USB-ID-only path should still classify as USB: %+v", rep)
	}
}

// TestProbeIncludesOnboardEth guards the Pi 4/5 fix: the onboard
// bcmgenet NIC sits on the SoC platform bus (no "usb" in the path,
// no PRODUCT= in uevent) and MUST still be reported — as a non-USB
// "Onboard Ethernet" port — so the wizard can offer it as the WAN.
// Regression this catches: the old probe only accepted USB adapters,
// so Pi 4B booted with "no Ethernet detected" on a cabled port.
func TestProbeIncludesOnboardEth(t *testing.T) {
	sysClassNet := makeFakeSysfs(t, map[string]struct {
		uevent, devPath string
	}{
		"eth0": {
			uevent:  "DRIVER=bcmgenet\n",
			devPath: "devices/platform/soc/fd580000.genet/net/eth0",
		},
	})
	rep, err := Probe{SysClassNet: sysClassNet, ModelFile: "/nonexistent"}.Run()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Eth) != 1 || !rep.RouterCapable {
		t.Fatalf("onboard eth should be reported: %+v", rep.Eth)
	}
	got := rep.Eth[0]
	if got.Interface != "eth0" || got.USB {
		t.Errorf("onboard eth0 should have USB=false: %+v", got)
	}
	if got.Model != "Onboard Ethernet (bcmgenet)" {
		t.Errorf("onboard model=%q, want %q", got.Model, "Onboard Ethernet (bcmgenet)")
	}
}

// TestProbeExcludesWireless makes sure a Wi-Fi netdev is never
// mistaken for a wired port even if it isn't named "wlan*": the
// phy80211 symlink is the signal.
func TestProbeExcludesWireless(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink-heavy test, skip on Windows")
	}
	sysClassNet := makeFakeSysfs(t, map[string]struct {
		uevent, devPath string
	}{
		"eth0": { // real wired port
			uevent:  "DRIVER=bcmgenet\n",
			devPath: "devices/platform/soc/fd580000.genet/net/eth0",
		},
		"wlp1s0": { // wireless despite non-wlan name
			uevent:  "DRIVER=brcmfmac\n",
			devPath: "devices/platform/soc/fe300000.mmc/wifi",
		},
	})
	// Mark wlp1s0 as wireless with a phy80211 symlink.
	if err := os.Symlink(filepath.Join(sysClassNet, "..", "..", "ieee80211", "phy0"),
		filepath.Join(sysClassNet, "wlp1s0", "phy80211")); err != nil {
		t.Fatal(err)
	}
	rep, err := Probe{SysClassNet: sysClassNet, ModelFile: "/nonexistent"}.Run()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Eth) != 1 || rep.Eth[0].Interface != "eth0" {
		t.Errorf("wireless netdev must be excluded; got %+v", rep.Eth)
	}
}

// TestProbeExcludesWWAN pins that a cellular data netdev (wwan0) is
// filtered out by name — it's the modem's WAN, not an assignable port.
func TestProbeExcludesWWAN(t *testing.T) {
	sysClassNet := makeFakeSysfs(t, map[string]struct {
		uevent, devPath string
	}{
		"eth0":  {uevent: "DRIVER=bcmgenet\n", devPath: "devices/platform/soc/fd580000.genet/net/eth0"},
		"wwan0": {uevent: "DRIVER=cdc_mbim\nPRODUCT=2c7c/0125/318\n", devPath: "devices/platform/soc/usb1/1-1/1-1:1.4"},
	})
	rep, err := Probe{SysClassNet: sysClassNet, ModelFile: "/nonexistent"}.Run()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Eth) != 1 || rep.Eth[0].Interface != "eth0" {
		t.Errorf("wwan0 must be excluded; got %+v", rep.Eth)
	}
}

func TestProbeMissingSysfsIsEmpty(t *testing.T) {
	rep, err := Probe{SysClassNet: filepath.Join(t.TempDir(), "absent"), ModelFile: "/absent"}.Run()
	if err != nil {
		t.Fatalf("missing sysfs should be soft-fail, got %v", err)
	}
	if rep.RouterCapable || len(rep.Eth) != 0 {
		t.Errorf("expected empty: %+v", rep)
	}
}

func TestClassifyPi(t *testing.T) {
	cases := map[string]PiModel{
		"Raspberry Pi Zero 2 W Rev 1.0":  PiZero2W,
		"Raspberry Pi 3 Model B Rev 1.2": Pi3B,
		"Raspberry Pi 4 Model B Rev 1.4": Pi4,
		"Raspberry Pi 5 Model B Rev 1.0": Pi5,
		"Some Other Board":               PiUnknown,
	}
	for in, want := range cases {
		if got := classifyPi(in); got != want {
			t.Errorf("classifyPi(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestGuestAPCapableGate(t *testing.T) {
	dir := t.TempDir()
	modelFile := filepath.Join(dir, "model")
	emptySys := filepath.Join(dir, "sys")
	if err := os.MkdirAll(emptySys, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		model string
		want  bool
	}{
		{"Raspberry Pi 4 Model B Rev 1.4", true},
		{"Raspberry Pi 5 Model B Rev 1.0", true},
		{"Raspberry Pi Zero 2 W Rev 1.0", false},
		{"Raspberry Pi 3 Model B Rev 1.2", false},
	} {
		if err := os.WriteFile(modelFile, []byte(c.model+"\x00"), 0o644); err != nil {
			t.Fatal(err)
		}
		rep, err := Probe{SysClassNet: emptySys, ModelFile: modelFile}.Run()
		if err != nil {
			t.Fatal(err)
		}
		if rep.GuestAPCapable != c.want {
			t.Errorf("%s: GuestAPCapable=%v want %v", c.model, rep.GuestAPCapable, c.want)
		}
	}
}
