// Package main is the entry point for knotd, the KnotOS daemon.
package main

import (
	"context"
	"crypto/ed25519"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"os/user"
	"strconv"
	"strings"
	"syscall"
	"time"

	"net"
	"net/http"

	"github.com/knot-os/knot-os/core/internal/api"
	"github.com/knot-os/knot-os/core/internal/applycoord"
	"github.com/knot-os/knot-os/core/internal/auth"
	"github.com/knot-os/knot-os/core/internal/bandwidth"
	"github.com/knot-os/knot-os/core/internal/config"
	"github.com/knot-os/knot-os/core/internal/deviceregistry"
	knotdns "github.com/knot-os/knot-os/core/internal/dns"
	"github.com/knot-os/knot-os/core/internal/events"
	"github.com/knot-os/knot-os/core/internal/guest"
	"github.com/knot-os/knot-os/core/internal/httpserver"
	"github.com/knot-os/knot-os/core/internal/network"
	netlinux "github.com/knot-os/knot-os/core/internal/network/linux"
	"github.com/knot-os/knot-os/core/internal/notify"
	"github.com/knot-os/knot-os/core/internal/plugin"
	"github.com/knot-os/knot-os/core/internal/profile"
	"github.com/knot-os/knot-os/core/internal/routing"
	"github.com/knot-os/knot-os/core/internal/scheduler"
	"github.com/knot-os/knot-os/core/internal/secrets"
	"github.com/knot-os/knot-os/core/internal/singbox"
	"github.com/knot-os/knot-os/core/internal/subscription"
	knottls "github.com/knot-os/knot-os/core/internal/tls"
	"github.com/knot-os/knot-os/core/internal/update"
	"github.com/knot-os/knot-os/core/internal/vpn"
	"github.com/knot-os/knot-os/core/internal/wol"
	"github.com/knot-os/knot-os/core/internal/xray"
	"github.com/knot-os/knot-os/core/internal/zapret"
)

// schedulerDevices adapts deviceregistry.Registry to the scheduler's
// minimal DeviceProvider interface.
type schedulerDevices struct{ r *deviceregistry.Registry }

func (s schedulerDevices) List() []scheduler.Device {
	all := s.r.List()
	out := make([]scheduler.Device, 0, len(all))
	for _, d := range all {
		out = append(out, scheduler.Device{
			MAC:        d.MAC,
			ProfileID:  d.ProfileID,
			PauseUntil: d.PauseUntil,
			Approved:   d.Approved,
		})
	}
	return out
}

// schedulerProfiles adapts profile.Registry to ProfileLookup.
type schedulerProfiles struct{ r *profile.Registry }

func (s schedulerProfiles) IsBlockingAt(id string, t time.Time) bool {
	p, ok := s.r.Get(id)
	if !ok {
		return false
	}
	return p.IsBlockingAt(t)
}

// nopMACSetUpdater is the dev-mode / mock fallback for the scheduler's
// nftables side-effect.
type nopMACSetUpdater struct{}

func (nopMACSetUpdater) UpdateBlockedMACs(_ []string) error { return nil }

// guestProviderAdapter wraps guest.Registry to satisfy
// netlinux.GuestSessionProvider. Single-method indirection keeps
// the linux backend independent of the guest package's import.
// sealerAdapter promotes a config.Sealer (which is what main.go
// wires up at boot) into a notify.Sealer. Same two-method
// contract; the indirection keeps the notify package free of
// the config import.
type sealerAdapter struct{ s config.Sealer }

func (a sealerAdapter) Wrap(p string) (string, error)   { return a.s.Wrap(p) }
func (a sealerAdapter) Unwrap(p string) (string, error) { return a.s.Unwrap(p) }

type guestProviderAdapter struct{ r *guest.Registry }

func (a guestProviderAdapter) CurrentGuestSession() netlinux.ActiveGuestSession {
	if a.r == nil {
		return netlinux.ActiveGuestSession{}
	}
	cur := a.r.Current()
	if !cur.Active(time.Now()) {
		return netlinux.ActiveGuestSession{}
	}
	return netlinux.ActiveGuestSession{SSID: cur.SSID, PSK: cur.PSK}
}

// dnsDeviceLookup bridges deviceregistry + profile.Registry into
// the dns.DeviceLookup interface: given a source IP, return the
// device's MAC and the blocklist names from its assigned profile.
type dnsDeviceLookup struct {
	devices  *deviceregistry.Registry
	profiles *profile.Registry
}

// leafSubjectFor builds the TLS leaf-cert subject for the daemon
// from the current config: device hostname (so `<name>.local`
// resolves to a trusted name) plus the LAN gateway IP (the address
// users actually type into the address bar). Loopback and
// localhost are added unconditionally inside BuildLeafSubject.
func leafSubjectFor(cfg config.Config) knottls.LeafSubject {
	var ips []net.IP
	if cfg.Network.LAN != nil {
		if gw := firstUsableIPv4(cfg.Network.LAN.CIDR); gw != "" {
			if ip := net.ParseIP(gw); ip != nil {
				ips = append(ips, ip)
			}
		}
	}
	return knottls.BuildLeafSubject(cfg.Device.Name, nil, ips)
}

// shouldRedirectHTTPS is true when the daemon should 301 plain
// HTTP → HTTPS. Stays off in setup role (the wizard wants to be
// reachable on plain HTTP, before the user has installed the root
// CA) and in dev mode.
func shouldRedirectHTTPS(cfg config.Config, devMode bool, tlsEnabled bool) bool {
	if devMode || !tlsEnabled {
		return false
	}
	return cfg.Role != config.RoleSetup
}

// dnsListenForRole derives the address knotd's DNS resolver should
// bind to given the current config. Returns "" when no listener
// should be active.
//
//	role=setup            → "" (dnsmasq holds 53 for the captive portal)
//	role=wifi-extender    → "<gateway>:53"
//	role=wifi-router      → "<gateway>:53"
//	dev mode              → "" (no port 53 binding on a developer's box)
//
// dnsUpstreamFromConfig translates the cfg.Network.DNS block (or
// nil) into the (mode, upstreams) pair the resolver expects. nil
// or empty mode means UDP plain — back-compat with v0.3 configs.
func dnsUpstreamFromConfig(cfg config.Config) (knotdns.UpstreamMode, []string) {
	d := cfg.Network.DNS
	if d == nil {
		return knotdns.UpstreamModeUDP, nil
	}
	mode := knotdns.UpstreamMode(d.Mode)
	if mode == "" {
		mode = knotdns.UpstreamModeUDP
	}
	return mode, append([]string(nil), d.Upstreams...)
}

func dnsListenForRole(cfg config.Config, devMode bool) string {
	if devMode {
		return ""
	}
	if cfg.Role != config.RoleWiFiExtender && cfg.Role != config.RoleWiFiRouter {
		return ""
	}
	lan := cfg.Network.LAN
	if lan == nil {
		return ""
	}
	gw := firstUsableIPv4(lan.CIDR)
	if gw == "" {
		return ""
	}
	return gw + ":53"
}

// zapretEgressIface returns the interface internet-bound traffic leaves
// on, which the zapret nft hook matches as oifname. Router → the WAN
// dongle; extender → the wlan0 STA uplink. Empty (setup role, or router
// with no WAN yet) disables the hook.
// liveWANIfaceProvider is satisfied by the Linux backend: in modem WAN
// mode the config's WAN.Interface is empty (the wwanN device is
// discovered at connect time), so subsystems that key off the egress
// interface ask the backend for the live one.
type liveWANIfaceProvider interface {
	LiveModemIface() string
}

func zapretEgressIface(cfg config.Config, backend network.Backend) string {
	switch cfg.Role {
	case config.RoleWiFiRouter:
		if cfg.Network.WAN == nil {
			return ""
		}
		if cfg.Network.WAN.Mode == "modem" {
			// Discovered at connect time — resolve the live wwanN.
			if p, ok := backend.(liveWANIfaceProvider); ok {
				return p.LiveModemIface()
			}
			return ""
		}
		return cfg.Network.WAN.Interface
	case config.RoleWiFiExtender:
		return "wlan0"
	}
	return ""
}

// firstUsableIPv4 mirrors the helper in network/linux/apply_setup.go.
// Kept inlined to avoid a dependency cycle (core/internal/network/linux
// is build-tagged for linux-only and main.go must compile on Windows
// for dev too).
func firstUsableIPv4(cidr string) string {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil || ipnet == nil {
		return ""
	}
	ip := ipnet.IP.To4()
	if ip == nil {
		return ""
	}
	gw := make(net.IP, 4)
	copy(gw, ip)
	gw[3]++
	return gw.String()
}

func (d dnsDeviceLookup) BlocklistsForIP(ip net.IP) (string, []string, bool) {
	if ip == nil {
		return "", nil, false
	}
	target := ip.String()
	for _, dev := range d.devices.List() {
		if dev.IP == target {
			if dev.ProfileID == "" {
				return dev.MAC, nil, true
			}
			p, ok := d.profiles.Get(dev.ProfileID)
			if !ok {
				return dev.MAC, nil, true
			}
			return dev.MAC, p.DNSBlocklists, true
		}
	}
	return "", nil, false
}

// SafeSearchForIP reports whether the device leasing ip is on a
// profile that enforces SafeSearch.
func (d dnsDeviceLookup) SafeSearchForIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	target := ip.String()
	for _, dev := range d.devices.List() {
		if dev.IP == target {
			if dev.ProfileID == "" {
				return false
			}
			p, ok := d.profiles.Get(dev.ProfileID)
			return ok && p.SafeSearch
		}
	}
	return false
}

// listenPort returns the numeric port from a listen address like ":80"
// or "127.0.0.1:8080". Used by LinuxBackend to set up captive-portal
// DNAT rules pointing at knotd.
func listenPort(addr string) int {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			n := 0
			for _, c := range addr[i+1:] {
				if c < '0' || c > '9' {
					return 80
				}
				n = n*10 + int(c-'0')
			}
			if n > 0 {
				return n
			}
		}
	}
	return 80
}

// captiveFunc adapts a plain function to dns.CaptiveLookup.
type captiveFunc func(net.IP) (net.IP, bool)

func (f captiveFunc) CaptiveIP(ip net.IP) (net.IP, bool) { return f(ip) }

// landingHTML is the self-contained page a blocked device sees. reason
// is "pending" (awaiting quarantine approval), "paused", or "blocked"
// (schedule). Bilingual RU/EN since it's shown on the client's own
// browser with no locale hint.
func landingHTML(reason string) []byte {
	icon, ruTitle, ruBody, enTitle, enBody := "bi", "", "", "", ""
	switch reason {
	case "pending":
		icon = "⏳"
		ruTitle, ruBody = "Ожидает одобрения", "Это устройство новое в сети. Администратор должен одобрить его, прежде чем появится интернет."
		enTitle, enBody = "Awaiting approval", "This device is new to the network. An administrator must approve it before it gets internet."
	case "paused":
		icon = "⏸️"
		ruTitle, ruBody = "Интернет на паузе", "Доступ в интернет для этого устройства приостановлен. Он возобновится автоматически или когда администратор снимет паузу."
		enTitle, enBody = "Internet paused", "Internet access for this device is paused. It will resume automatically, or when the administrator lifts the pause."
	default:
		icon = "🚫"
		ruTitle, ruBody = "Интернет заблокирован", "Доступ в интернет для этого устройства сейчас заблокирован по расписанию."
		enTitle, enBody = "Internet blocked", "Internet access for this device is currently blocked by a schedule."
	}
	return []byte(fmt.Sprintf(`<!doctype html><html><head><meta charset=utf-8>
<meta name=viewport content="width=device-width,initial-scale=1"><title>%s — KnotOS</title>
<style>
 html,body{height:100%%;margin:0}
 body{font-family:system-ui,sans-serif;background:#0f172a;color:#e2e8f0;display:grid;place-items:center;padding:1.5rem}
 .card{max-width:30rem;text-align:center;background:#1e293b;border:1px solid #334155;border-radius:18px;padding:2.5rem 2rem}
 .ico{font-size:3rem;line-height:1}
 h1{font-size:1.5rem;margin:1rem 0 .25rem}
 .en h1{font-size:1.05rem;color:#94a3b8;font-weight:600;margin-top:.25rem}
 p{color:#cbd5e1;line-height:1.5;margin:.5rem 0}
 .en p{color:#94a3b8;font-size:.9rem}
 .brand{margin-top:1.5rem;color:#64748b;font-size:.8rem;letter-spacing:.03em}
 hr{border:0;border-top:1px solid #334155;margin:1.25rem 0}
</style></head><body><div class=card>
 <div class=ico>%s</div>
 <h1>%s</h1><p>%s</p>
 <hr><div class=en><h1>%s</h1><p>%s</p></div>
 <div class=brand>KnotOS</div>
</div></body></html>`, ruTitle, icon, ruTitle, ruBody, enTitle, enBody))
}

// Version is overridden at build time via -ldflags "-X main.Version=...".
var Version = "0.0.0-dev"

func main() {
	var (
		showVersion   = flag.Bool("version", false, "print version and exit")
		dev           = flag.Bool("dev", false, "run in dev mode with mock network backend")
		configPath    = flag.String("config", "/etc/knot/config.yaml", "path to configuration file")
		listenAddr    = flag.String("listen", ":80", "HTTP listen address (empty disables)")
		tlsListenAddr = flag.String("tls-listen", ":443", "HTTPS listen address (empty disables)")
		tlsDir        = flag.String("tls-dir", "/etc/knot/tls", "directory holding the device root CA + leaf cert")
		noTLS         = flag.Bool("no-tls", false, "disable TLS entirely (dev / debug)")
		seedPath      = flag.String("secrets-seed", secrets.DefaultSeedPath, "path to the random seed used to derive the at-rest encryption key")
		machineIDPath = flag.String("secrets-machine-id", secrets.DefaultMachineIDPath, "path to /etc/machine-id (mixed into the encryption key); empty to skip")
		pluginsDir    = flag.String("plugins-dir", "/usr/lib/knot/plugins", "directory containing installed plugins")
		updateRepo    = flag.String("update-repo", "bigtaed-sys/knot-os", "GitHub <owner>/<name> to query for self-update releases")
		pluginsIndex  = flag.String("plugins-index", "https://raw.githubusercontent.com/bigtaed-sys/knot-os-plugins/main/store.json", "URL of the plugin store catalog (JSON index)")
		pluginUser    = flag.String("plugin-user", "knot-plugin", "unprivileged user to run plugin processes as (Linux); empty or unknown = run unconfined")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("knotd", Version)
		return
	}

	logger := log.New(os.Stderr, "knotd: ", log.LstdFlags|log.Lmsgprefix)

	mode := "linux backend"
	if *dev {
		mode = "dev mode (mock network backend)"
	}
	logger.Printf("starting %s — version=%s config=%s listen=%s", mode, Version, *configPath, *listenAddr)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Bring up the at-rest secrets sealer before the config load:
	// the YAML may contain `enc:v1:` PSKs that need to be unwrapped.
	// In -dev mode we skip the sealer entirely; the dev YAML stays
	// cleartext for easy editing. On a real device the seed file is
	// generated once and lives on the FAT boot partition.
	var sealer config.Sealer
	if !*dev {
		key, err := secrets.LoadOrCreateKey(secrets.SeedOptions{
			SeedPath:      *seedPath,
			MachineIDPath: *machineIDPath,
		})
		if err != nil {
			logger.Fatalf("secrets key: %v", err)
		}
		s, err := secrets.New(key)
		if err != nil {
			logger.Fatalf("secrets sealer: %v", err)
		}
		sealer = s
	}

	cfg, needsMigration, err := config.LoadWithMigration(*configPath, sealer)
	if err != nil {
		logger.Fatalf("load config: %v", err)
	}
	logger.Printf("config loaded: device=%q role=%q", cfg.Device.Name, cfg.Role)

	// If the loaded config contained any cleartext PSKs (legacy
	// v0.1/v0.2 format), persist it back encrypted now. One-shot:
	// after this completes the file's secrets are wrapped and
	// subsequent boots see needsMigration=false.
	if sealer != nil && needsMigration {
		if err := config.SaveWith(*configPath, cfg, sealer); err != nil {
			logger.Printf("secrets migration: %v", err)
		} else {
			logger.Printf("secrets migration: encrypted PSKs at rest")
		}
	}

	// Pick a backend. -dev uses the mock everywhere; otherwise we
	// build the LinuxBackend (which only compiles fully on Linux —
	// on other OSes it's a stub that errors out on every method).
	var backend network.Backend
	if *dev {
		backend = network.NewMock()
	} else {
		lb := netlinux.New(netlinux.Options{
			Logger:   logger,
			HTTPPort: listenPort(*listenAddr),
		})
		if err := lb.Init(ctx); err != nil {
			logger.Fatalf("linux backend init: %v", err)
		}
		defer lb.Close()
		backend = lb
	}

	// Apply the loaded config to the backend immediately so /api/status
	// reports a state consistent with what's on disk.
	if err := backend.Apply(ctx, cfg); err != nil {
		logger.Fatalf("initial apply: %v", err)
	}

	// TLS materials: device-local PKI for the HTTPS listener.
	// Generated on first boot, persisted, re-issued automatically
	// when the LAN gateway IP changes (e.g. role transition). Skipped
	// entirely in -dev or with -no-tls so a developer running on a
	// laptop doesn't need root for :443.
	var tlsMaterials *knottls.Materials
	tlsActive := !*dev && !*noTLS && *tlsListenAddr != ""
	if tlsActive {
		m, err := knottls.Open(knottls.Options{
			Dir:     *tlsDir,
			Subject: leafSubjectFor(cfg),
		})
		if err != nil {
			logger.Printf("tls: %v — falling back to HTTP only", err)
			tlsActive = false
		} else {
			tlsMaterials = m
			snap := m.Snapshot()
			logger.Printf("tls: leaf %s expires %s, root %s expires %s",
				snap.LeafFingerprint[:23], snap.LeafNotAfter.Format("2006-01-02"),
				snap.RootFingerprint[:23], snap.RootNotAfter.Format("2006-01-02"))
		}
	}

	// Device registry: tracks LAN devices via dnsmasq leases + a YAML
	// store of user-set names/profiles. Started here so the API has it
	// from the first request.
	leasePath := ""
	arpPath := ""
	if !*dev {
		leasePath = "/var/lib/misc/dnsmasq.leases"
		arpPath = deviceregistry.DefaultARPFile
	}
	devices := deviceregistry.NewRegistry(deviceregistry.Options{
		StoreFile: "/etc/knot/devices.yaml",
		LeaseFile: leasePath,
		ARPFile:   arpPath,
		Logger:    logger,
	})
	if err := devices.Load(); err != nil {
		logger.Printf("deviceregistry load: %v", err)
	}
	if err := devices.RefreshFromLeases(); err != nil {
		logger.Printf("deviceregistry initial refresh: %v", err)
	}
	if err := devices.StartLeaseWatcher(ctx); err != nil {
		logger.Printf("deviceregistry watcher: %v", err)
	}
	// ARP watcher polls /proc/net/arp every 30s and updates each
	// device's LastARPSeen. With that signal the per-device "online"
	// pill in the UI flips off within minutes of a phone leaving
	// the LAN, instead of staying green for the full 12-hour DHCP
	// lease as it did in v0.2.
	devices.StartARPWatcher(ctx)
	logger.Printf("device registry: %d known", len(devices.List()))

	// Hourly sweep of anonymous rotation ghosts: phones with rotating
	// private MACs leave a trail of offline single-use entries. Carry-
	// forward (in RefreshFromLeases) collapses ones with a matching
	// hostname; this prunes the rest (offline, randomized, uncustomized,
	// older than 6h) so the device list doesn't accumulate clutter.
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				if n := devices.PruneStaleRandomized(now, 6*time.Hour); n > 0 {
					_ = devices.FlushIfDirty()
					logger.Printf("device registry: pruned %d stale randomized-MAC ghost(s)", n)
				}
			}
		}
	}()

	// Profile registry: built-in + user profiles, persisted to YAML.
	profiles := profile.NewRegistry("/etc/knot/profiles.yaml")
	if err := profiles.Load(); err != nil {
		logger.Printf("profiles load: %v", err)
	}
	logger.Printf("profiles: %d loaded", len(profiles.List()))

	// Scheduler: every 30s recomputes which devices are currently in
	// a block window and pushes their MACs to the kernel block-set
	// via nftables (or the no-op updater in dev mode).
	var updater scheduler.MACSetUpdater = nopMACSetUpdater{}
	if !*dev {
		// The Linux backend implements UpdateBlockedMACs.
		if lb, ok := backend.(*netlinux.LinuxBackend); ok {
			updater = lb
		}
	}
	sched := scheduler.New(scheduler.Options{
		Devices:    schedulerDevices{r: devices},
		Profiles:   schedulerProfiles{r: profiles},
		Updater:    updater,
		Quarantine: devices.Quarantine,
		Logger:     logger,
	})
	go sched.Run(ctx)

	// Blocklist registry + downloader. The downloader fetches its
	// configured sources at boot (after a short delay so it doesn't
	// race the rest of startup), once daily, and on demand via
	// /api/dns/refresh (M11d). On disk caches survive reboots so a
	// device offline at boot still has filtering on the second boot.
	dnsBlocklists := knotdns.NewRegistry()
	dnsDownloader := knotdns.NewDownloader(knotdns.DownloaderOptions{
		Registry: dnsBlocklists,
		Logger:   logger,
	})
	go dnsDownloader.Run(ctx)

	// In-memory query log + response cache. The log feeds the
	// /api/dns/{stats,queries} endpoints (M11d) and the Protection
	// UI in M12; the cache deduplicates upstream traffic and shrinks
	// the per-query latency on a busy LAN.
	dnsLog := knotdns.NewRingLog(0)
	dnsCache := knotdns.NewCache(knotdns.CacheOptions{})

	// DNS resolver: started always, but the listen address is
	// derived from the current role (empty in setup mode where
	// dnsmasq's own DNS catch-all owns port 53; gateway:53 in
	// wifi-extender mode where dnsmasq is configured port=0).
	// LAN gateway used as the captive landing target. Read once at
	// startup (matches the bandwidth sampler's convention); a LAN-CIDR
	// change is picked up on next boot.
	captiveGW := ""
	if cfg.Network.LAN != nil {
		captiveGW = firstUsableIPv4(cfg.Network.LAN.CIDR)
	}
	// deviceBlockReason reports why a source IP is currently denied the
	// internet (and whether it is). Shared by the DNS captive redirect
	// and the HTTP landing page so both agree.
	deviceBlockReason := func(ip string) (string, bool) {
		if ip == "" {
			return "", false
		}
		var dev deviceregistry.Device
		found := false
		for _, d := range devices.List() {
			if d.IP == ip {
				dev, found = d, true
				break
			}
		}
		if !found {
			return "", false
		}
		now := time.Now()
		if devices.Quarantine() && !dev.Approved {
			return "pending", true
		}
		if dev.Paused(now) {
			return "paused", true
		}
		if dev.ProfileID != "" {
			if p, ok := profiles.Get(dev.ProfileID); ok && p.IsBlockingAt(now) {
				return "blocked", true
			}
		}
		return "", false
	}

	dnsMode, dnsUpstreams := dnsUpstreamFromConfig(cfg)
	dnsServer := knotdns.New(knotdns.Options{
		Listen:     dnsListenForRole(cfg, *dev),
		Blocklists: dnsBlocklists,
		Devices:    dnsDeviceLookup{devices: devices, profiles: profiles},
		Captive: captiveFunc(func(srcIP net.IP) (net.IP, bool) {
			if !devices.BlockLanding() || captiveGW == "" {
				return nil, false
			}
			if _, blocked := deviceBlockReason(srcIP.String()); blocked {
				return net.ParseIP(captiveGW), true
			}
			return nil, false
		}),
		Log:          dnsLog,
		Cache:        dnsCache,
		UpstreamMode: dnsMode,
		Upstreams:    dnsUpstreams,
		Logger:       logger,
	})
	go func() {
		if err := dnsServer.Run(ctx); err != nil {
			logger.Printf("dns server: %v", err)
		}
	}()

	plugins := plugin.NewRegistry(*pluginsDir)
	if err := plugins.Discover(); err != nil {
		// Discovery errors are non-fatal: bad plugins are skipped,
		// good ones still load. We log so operators know.
		logger.Printf("plugin discovery: %v", err)
	}
	// Apply enabled-state from config so a previously-enabled plugin
	// stays enabled across reboots.
	enabled := make(map[string]bool, len(cfg.Plugins))
	for id, pc := range cfg.Plugins {
		enabled[id] = pc.Enabled
	}
	plugins.ApplyEnabledMap(enabled)
	logger.Printf("plugins: %d discovered in %s", len(plugins.List()), *pluginsDir)

	// Persisted sessions: in dev mode we keep them in memory (the dev
	// run is short-lived, no persistence dir to mess with), in
	// production we back them with /var/lib/knot/sessions.json so a
	// reboot or auto-update doesn't kick the user out.
	var sessions *auth.Sessions
	if *dev {
		sessions = auth.NewSessions()
	} else {
		s, err := auth.NewSessionsAt("/var/lib/knot/sessions.json")
		if err != nil {
			logger.Printf("sessions: load: %v (starting empty)", err)
			s = auth.NewSessions()
		}
		sessions = s
	}

	apiSrv := api.New(api.Options{
		ConfigPath: *configPath,
		Initial:    cfg,
		Version:    Version,
		Backend:    backend,
		Sessions:   sessions,
		Plugins:    plugins,
	})
	apiSrv.SetDeviceRegistry(devices)
	apiSrv.SetProfileRegistry(profiles)
	apiSrv.SetDNSServices(dnsLog, dnsBlocklists, dnsDownloader)
	apiSrv.SetSealer(sealer)

	// Transactional Apply coordinator — wraps backend.Apply with a
	// snapshot + health-check + rollback. PUT /api/config delegates
	// here. The CommitFn closure persists the new config to disk,
	// updates the in-memory snapshot, and fires the onConfigApplied
	// chain (DNS / TLS / WG / sing-box). If commit fails OR the
	// post-Apply health check times out, the coordinator re-runs
	// Apply with the snapshot — system returns to last-known-good.
	applyCoord, err := applycoord.NewCoordinator(applycoord.Options{
		Backend: backend,
		Logger:  logger,
		SnapshotFn: func() config.Config {
			return apiSrv.Snapshot()
		},
		CommitFn: func(c config.Config) error {
			if err := config.SaveWith(*configPath, c, sealer); err != nil {
				return err
			}
			apiSrv.SetConfig(c)
			apiSrv.FireConfigApplied()
			return nil
		},
	})
	if err != nil {
		logger.Fatalf("apply coordinator: %v", err)
	}
	apiSrv.SetApplyCoordinator(applyCoord)

	// Per-device bandwidth metering. The sampler reads
	// /proc/net/nf_conntrack every 2s, aggregates by source IP,
	// resolves IP→MAC via the device registry, and pushes samples
	// into the tracker's ring buffers. The non-Linux build is a
	// no-op stub, so this is safe to wire unconditionally.
	bwTracker := bandwidth.NewTracker()
	apiSrv.SetBandwidthTracker(bwTracker)
	go func() {
		// LAN CIDR is read at startup; if the user changes it later,
		// next boot picks up the new value. Worth the simplicity.
		lan := ""
		if cfg.Network.LAN != nil {
			lan = cfg.Network.LAN.CIDR
		}
		bandwidth.NewLinuxSampler(bwTracker, devices, lan).Run(ctx)
	}()
	if tlsMaterials != nil {
		apiSrv.SetTLSMaterials(tlsMaterials, func() knottls.LeafSubject {
			return leafSubjectFor(apiSrv.Snapshot())
		})
	}
	// Auto-update path: query GitHub Releases for a newer knotd
	// binary, verify the detached Ed25519 signature against the
	// release public key baked in at build time, atomically replace
	// /usr/local/bin/knotd, restart the service. Skipped in dev
	// because the install path needs root + systemctl.
	if !*dev {
		// Per-device rescue keypair — generated on first boot,
		// stored at /etc/knot/rescue.json with mode 0600. The
		// public half authorises self-built binaries; the private
		// half is delivered to the user once via the System UI
		// then erased from disk. Loss of the rescue private key
		// is a non-event for normal users (auto-update still works
		// against the official release key); it matters only when
		// the user wants to install their own builds OR the
		// release key has been compromised.
		var rescuePub ed25519.PublicKey
		rescue, err := update.LoadOrCreateRescue(update.DefaultRescuePath)
		if err != nil {
			logger.Printf("update: rescue keypair: %v (continuing without rescue key)", err)
		} else {
			rescuePub = rescue.PublicKey()
		}

		// Self-update source. Defaults to the project's release repo;
		// override with -update-repo for a fork. A malformed value
		// falls back to the update package's own default rather than
		// disabling updates outright.
		repoOwner, repoName := "", ""
		if o, n, ok := strings.Cut(*updateRepo, "/"); ok {
			repoOwner, repoName = o, n
		} else {
			logger.Printf("update: -update-repo %q is not <owner>/<name>; using package default", *updateRepo)
		}
		updater, err := update.New(update.Options{
			CurrentVersion: Version,
			RepoOwner:      repoOwner,
			RepoName:       repoName,
			Logger:         logger,
		})
		if err != nil {
			logger.Printf("update: %v — auto-update endpoints disabled", err)
		} else {
			if rescuePub != nil {
				updater.SetRescueKey(rescuePub)
			}
			apiSrv.SetUpdateManager(updater)
			if rescue != nil {
				apiSrv.SetRescue(rescue)
			}
		}
	}
	// SchedulerKick lets the API trigger an immediate scheduler tick
	// after a device's profile or a profile's schedule changes.
	apiSrv.SetSchedulerKick(func() {
		go sched.RunOnce()
	})
	// On every config-apply (PUT /api/config or setup wizard
	// completion), update the DNS resolver's listen address to
	// match the new role. Goes through SetListen which is
	// idempotent for unchanged addresses.
	// onConfigApplied is wired in at the bottom (after we have the
	// http server) because it needs to flip the HTTPS redirect on
	// role transitions in the same callback as the DNS+TLS updates.
	// Production mode unlocks the system endpoints (reboot/shutdown/
	// update) that would be destructive in dev. Tied to the absence
	// of -dev because that's the same condition that picks the real
	// LinuxBackend.
	apiSrv.SetProductionMode(!*dev)

	// Event bus: in-process pub/sub for "WAN went down", "new device
	// on the LAN", "profile changed", etc. Created here (before the
	// plugin runtime) so the host API's event stream can dispatch from
	// it the moment a plugin connects. Publishers are scattered across
	// the daemon; subscribers are the notify bot and plugins.
	eventBus := events.NewBus()
	apiSrv.SetEventBus(eventBus)

	// Plugin runtime: supervise enabled plugins as subprocesses and
	// expose the host API on a root-owned loopback Unix socket they
	// call back through. Production-only — the plugin dir and /run
	// sockets are a device concern; dev mode discovers and lists
	// plugins but runs none.
	if !*dev {
		const (
			hostSock  = "/run/knot/host.sock"
			pluginRun = "/run/knot/plugins"
		)
		// Sandbox: resolve the unprivileged user plugins run as. If it
		// doesn't exist (e.g. an older image), plugins run unconfined —
		// log it rather than refusing to run them.
		sandUID, sandGID := 0, 0
		if *pluginUser != "" {
			if u, err := user.Lookup(*pluginUser); err == nil {
				sandUID, _ = strconv.Atoi(u.Uid)
				sandGID, _ = strconv.Atoi(u.Gid)
			} else {
				logger.Printf("plugin host: user %q not found (%v) — plugins will run unconfined", *pluginUser, err)
			}
		}
		sup := plugin.NewSupervisor(plugin.SupervisorOptions{
			PluginsDir: *pluginsDir,
			RuntimeDir: pluginRun,
			DataDir:    "/var/lib/knot/plugins",
			HostSocket: hostSock,
			RunAsUID:   sandUID,
			RunAsGID:   sandGID,
			Logger:     logger,
		})
		apiSrv.SetPluginSupervisor(sup)
		apiSrv.SetPluginSyncFn(func() { sup.Sync(plugins.List()) })

		// Plugin store: install packages from the GitHub-hosted
		// catalog. A package signed by the release key (the same trust
		// anchor as auto-update) installs as "official"; anything else
		// requires the operator's explicit confirmation.
		installer := &plugin.Installer{Dir: *pluginsDir}
		if k := update.ReleasePublicKey(); k != nil {
			installer.TrustedKeys = []ed25519.PublicKey{k}
		}
		apiSrv.SetPluginStore(installer, *pluginsIndex)

		_ = os.MkdirAll("/run/knot", 0o755)
		_ = os.MkdirAll(pluginRun, 0o755)
		// The plugin runs as sandUID and must be able to bind its socket
		// in pluginRun, so hand that dir to it.
		if sandUID > 0 {
			_ = os.Chown(pluginRun, sandUID, sandGID)
		}
		_ = os.Remove(hostSock)
		if ln, err := net.Listen("unix", hostSock); err != nil {
			logger.Printf("plugin host: listen %s: %v (host API disabled)", hostSock, err)
		} else {
			_ = os.Chmod(hostSock, 0o600)
			// Let the (non-root) plugin user reach the host socket; the
			// per-plugin token still scopes what each one can do.
			if sandUID > 0 {
				_ = os.Chown(hostSock, sandUID, sandGID)
			}
			hostSrv := &http.Server{Handler: apiSrv.HostAPIHandler()}
			go func() { _ = hostSrv.Serve(ln) }()
			go func() { <-ctx.Done(); _ = hostSrv.Close() }()
			logger.Printf("plugin host: API on %s", hostSock)
		}

		// Bring up whatever's already enabled, and tear everything
		// down cleanly on shutdown.
		sup.Sync(plugins.List())
		defer sup.StopAll()
	}

	// WireGuard road-warrior server. Persisted in
	// /etc/knot/wg.yaml; key generated on first boot. Apply hook
	// below picks up the registry on every config-apply, so adding
	// a peer in the UI takes effect within the same request.
	wgRegistry, err := vpn.Open("/etc/knot/wg.yaml")
	if err != nil {
		logger.Printf("vpn: open registry: %v (VPN endpoints disabled)", err)
	} else {
		apiSrv.SetVPNRegistry(wgRegistry)
		snap := wgRegistry.Server()
		logger.Printf("vpn: server pub=%s peers=%d enabled=%v",
			wgRegistry.PublicServerKey().String()[:11]+"…", len(wgRegistry.Peers()), snap.Enabled)
	}

	// VPN-subscription registry — Happ-style v0.5 feature. Stores
	// pasted vless://-style URIs and HTTPS subscription URLs that
	// resolve into bundles of servers. The fetcher runs HTTPS GETs
	// when the user clicks "Refresh"; the registry caches the
	// parsed snapshot to disk so we survive a reboot offline.
	subsRegistry := subscription.NewRegistry("/var/lib/knot/subscriptions.yaml")
	if err := subsRegistry.Load(); err != nil {
		logger.Printf("subscription: load: %v (starting empty)", err)
	} else {
		apiSrv.SetSubscriptions(subsRegistry, subscription.NewFetcher())
		logger.Printf("subscription: loaded %d subscriptions", len(subsRegistry.List()))
	}

	// sing-box supervisor. The Linux build registers a real process
	// runner that signals SIGHUP on config changes; the dev-host
	// build is a no-op stub. The Manager itself is platform-agnostic.
	singboxRunner := netlinux.NewSingBoxRunner()
	singboxMgr := singbox.NewManager(singboxRunner, logger)

	// Xray supervisor. Runs alongside sing-box as a protocol upstream:
	// servers sing-box can't speak (xhttp etc.) are hosted by Xray on
	// loopback SOCKS ports that sing-box dials. Idle (process stopped)
	// until the routing layer produces at least one Xray upstream.
	xrayRunner := netlinux.NewXrayRunner()
	xrayMgr := xray.NewManager(xrayRunner, logger)

	// Zapret (nfqws) supervisor — DPI-bypass for YouTube/Discord. The
	// runner owns both the nfqws process and its isolated nft hook;
	// idle until a config with zapret enabled is applied.
	zapretRunner := netlinux.NewZapretRunner()
	zapretMgr := zapret.NewManager(zapretRunner, logger)
	apiSrv.SetZapretManager(zapretMgr)

	// Routing diagnostics endpoint — UI pulls per-device decisions
	// and the kill-switch list from this. Closes over the live
	// registries; reads happen on every GET /api/routing.
	apiSrv.SetRoutingProvider(func() (routing.Result, error) {
		cfg := apiSrv.Snapshot()
		lan := ""
		if cfg.Network.LAN != nil {
			lan = cfg.Network.LAN.CIDR
		}
		if lan == "" {
			return routing.Result{}, fmt.Errorf("LAN not configured yet")
		}
		return routing.FromRegistries(subsRegistry, devices, profiles, lan)
	})

	// Guest network registry. Single active session at a time,
	// auto-expires via a 30s sweeper. Apply callback below maps
	// the active session into the hostapd multi-BSS config.
	guestRegistry, err := guest.Open("/etc/knot/guest.yaml")
	if err != nil {
		logger.Printf("guest: open registry: %v (guest endpoints disabled)", err)
	} else {
		apiSrv.SetGuestRegistry(guestRegistry)
		// Hand the registry to the Linux backend so applyRouter can
		// pull the live session on every Apply.
		if lb, ok := backend.(*netlinux.LinuxBackend); ok {
			lb.SetGuestProvider(guestProviderAdapter{r: guestRegistry})
		}
		// Watcher: every 30s clear expired sessions, then re-apply
		// so the BSS gets torn down without waiting for a manual
		// trigger. Idempotent — if nothing expired, fireConfigApplied
		// just walks through unchanged inputs.
		go func() {
			t := time.NewTicker(30 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case now := <-t.C:
					if guestRegistry.SweepExpired(now) {
						logger.Printf("guest: session expired, tearing down BSS")
						apiSrv.FireConfigApplied()
					}
				}
			}
		}()
	}

	// (eventBus created earlier, before the plugin runtime block.)

	// Notification subsystem: Telegram bot + persistent state.
	// In dev mode the store still loads but we don't pass a sealer
	// (matches the rest of the dev-mode plaintext pattern).
	var notifyStoreSealer notify.Sealer
	if sealer != nil {
		notifyStoreSealer = sealerAdapter{s: sealer}
	}
	notifyStore, err := notify.Open("/etc/knot/notify.yaml", notifyStoreSealer)
	if err != nil {
		logger.Printf("notify: open store: %v (bot disabled)", err)
	} else {
		bot := notify.NewBot(notifyStore, eventBus, logger)
		// Read-only callbacks the bot pulls live data through.
		// Each closure captures the relevant registry; nil-safety
		// is one nil-check per callback so they fail soft if a
		// subsystem isn't wired (dev mode etc.).
		bot.StatusFn = func() notify.StatusSnapshot {
			snap := apiSrv.Snapshot()
			netStatus, _ := backend.Status(ctx)
			online := 0
			for _, d := range devices.List() {
				if d.Online(time.Now()) {
					online++
				}
			}
			out := notify.StatusSnapshot{
				Role:          string(snap.Role),
				DeviceName:    snap.Device.Name,
				Version:       Version,
				OnlineDevices: online,
			}
			if netStatus.WAN != nil {
				out.WANUp = netStatus.WAN.Up
				out.WANIface = netStatus.WAN.Interface
				out.WANIP = netStatus.WAN.IP
			}
			if netStatus.AP != nil {
				out.APSSID = netStatus.AP.SSID
				out.APUp = netStatus.AP.Up
			}
			return out
		}
		bot.DevicesFn = func() []notify.DeviceSnapshot {
			now := time.Now()
			all := devices.List()
			out := make([]notify.DeviceSnapshot, 0, len(all))
			for _, d := range all {
				out = append(out, notify.DeviceSnapshot{
					MAC: d.MAC, Label: d.Label(), IP: d.IP,
					Online:    d.Online(now),
					Stale:     d.Stale(now),
					ProfileID: d.ProfileID,
				})
			}
			return out
		}
		bot.ProfilesFn = func() []notify.ProfileSnapshot {
			ps := profiles.List()
			out := make([]notify.ProfileSnapshot, 0, len(ps))
			for _, p := range ps {
				out = append(out, notify.ProfileSnapshot{ID: p.ID, Name: p.Name})
			}
			return out
		}
		bot.WakeFn = func(mac string) error {
			d, ok := devices.Get(mac)
			if !ok {
				return fmt.Errorf("device %s not in registry", mac)
			}
			cfg := apiSrv.Snapshot()
			if cfg.Network.LAN == nil {
				return fmt.Errorf("LAN not configured")
			}
			bcast, err := wol.BroadcastForCIDR(cfg.Network.LAN.CIDR)
			if err != nil {
				return err
			}
			return wol.Wake(d.MAC, bcast, 0)
		}
		bot.SetProfileFn = func(mac, profileID string) error {
			_, err := devices.Update(mac, func(d *deviceregistry.Device) {
				d.ProfileID = profileID
			})
			if err == nil {
				_ = devices.FlushIfDirty()
				go sched.RunOnce()
			}
			return err
		}
		bot.ProtectionFn = func() notify.ProtectionSnapshot {
			st := dnsLog.Stats(0)
			cfg := apiSrv.Snapshot()
			mode := "udp"
			if cfg.Network.DNS != nil && cfg.Network.DNS.Mode != "" {
				mode = cfg.Network.DNS.Mode
			}
			ratio := 0.0
			if st.TotalQueries > 0 {
				ratio = float64(st.TotalBlocked) / float64(st.TotalQueries)
			}
			return notify.ProtectionSnapshot{
				Queries:      st.TotalQueries,
				Blocked:      st.TotalBlocked,
				BlockedRatio: ratio,
				UpstreamMode: mode,
			}
		}
		bot.SetDNSModeFn = func(mode string) error {
			cfg := apiSrv.Snapshot()
			if cfg.Network.DNS == nil {
				cfg.Network.DNS = &config.DNSUpstream{}
			} else {
				cp := *cfg.Network.DNS
				cfg.Network.DNS = &cp
			}
			cfg.Network.DNS.Mode = mode
			cfg.Network.DNS.Upstreams = nil
			if err := config.SaveWith(*configPath, cfg, sealer); err != nil {
				return err
			}
			apiSrv.SetConfig(cfg)
			apiSrv.FireConfigApplied()
			return nil
		}
		if guestRegistry != nil {
			bot.GuestFn = func() *notify.GuestSnapshot {
				cur := guestRegistry.Current()
				if !cur.Active(time.Now()) {
					return nil
				}
				rem := int64(-1)
				if !cur.ExpiresAt.IsZero() {
					rem = int64(time.Until(cur.ExpiresAt).Seconds())
					if rem < 0 {
						rem = 0
					}
				}
				return &notify.GuestSnapshot{
					SSID: cur.SSID, PSK: cur.PSK, RemainingSec: rem,
				}
			}
			bot.RevokeGuestFn = func() error {
				err := guestRegistry.Revoke()
				if err == nil {
					apiSrv.FireConfigApplied()
				}
				return err
			}
		}

		// VPN-routing summary for /routing in Telegram. Mirrors the
		// /api/routing endpoint but bucketed for chat display.
		bot.RoutingFn = func() *notify.RoutingSnapshot {
			cfg := apiSrv.Snapshot()
			lan := ""
			if cfg.Network.LAN != nil {
				lan = cfg.Network.LAN.CIDR
			}
			if lan == "" {
				return nil
			}
			res, err := routing.FromRegistries(subsRegistry, devices, profiles, lan)
			if err != nil {
				return nil
			}
			snap := &notify.RoutingSnapshot{
				MissingOutbounds: res.MissingOutbounds,
			}
			subList := subsRegistry.List()
			for _, s := range subList {
				if len(s.Servers) > 0 {
					snap.Subscriptions++
					snap.Servers += len(s.Servers)
				}
			}
			for _, dr := range res.DeviceRoutes {
				switch dr.Status {
				case "tunnel":
					snap.DevicesTunnel++
				case "kill":
					snap.DevicesKill++
				default:
					snap.DevicesDirect++
				}
			}
			return snap
		}

		apiSrv.SetNotifyServices(notifyStore, bot)
		if err := bot.Start(ctx); err != nil {
			logger.Printf("notify: bot start: %v (bot disabled, fix the token in System → Notifications)", err)
		}

		// WAN watcher: poll backend.Status every 30s and publish a
		// transition event whenever the up/down flag flips. Cheap;
		// the actual link-state read is just /sys/class/net/<wan>/operstate.
		go func() {
			t := time.NewTicker(30 * time.Second)
			defer t.Stop()
			lastUp := false
			started := false
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					st, err := backend.Status(ctx)
					if err != nil || st.WAN == nil {
						continue
					}
					if !started {
						lastUp = st.WAN.Up
						started = true
						continue
					}
					if st.WAN.Up != lastUp {
						eventBus.Publish(ctx, events.Event{
							Kind: events.KindWANStatus,
							Payload: events.WANStatus{
								Up:        st.WAN.Up,
								Interface: st.WAN.Interface,
								IP:        st.WAN.IP,
							},
						})
						lastUp = st.WAN.Up
					}
				}
			}
		}()

		// Device-joined watcher: track which MACs the registry has
		// seen, fire on first appearance. We poll the registry every
		// 30s rather than wiring callbacks into deviceregistry directly
		// to keep that package independent of events/.
		go func() {
			t := time.NewTicker(30 * time.Second)
			defer t.Stop()
			seen := make(map[string]bool)
			// Bootstrap with whatever already exists so a daemon
			// restart doesn't spam "new device" for everything.
			for _, d := range devices.List() {
				seen[d.MAC] = true
			}
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					for _, d := range devices.List() {
						if seen[d.MAC] {
							continue
						}
						seen[d.MAC] = true
						eventBus.Publish(ctx, events.Event{
							Kind: events.KindDeviceJoined,
							Payload: events.DeviceJoined{
								MAC: d.MAC, Hostname: d.Hostname, IP: d.IP,
							},
						})
					}
				}
			}
		}()
	}

	srvOpts := httpserver.Options{
		Addr:   *listenAddr,
		Logger: logger,
	}
	if tlsActive {
		srvOpts.TLSAddr = *tlsListenAddr
		srvOpts.TLS = tlsMaterials
	}
	srv := httpserver.New(srvOpts)
	srv.Mount("/api", apiSrv.Handler())
	srv.SetRedirectHTTPS(shouldRedirectHTTPS(cfg, *dev, tlsActive))
	// Blocked-device landing: serve the explanatory page to any client
	// that's currently denied the internet (and has the landing on).
	srv.SetBlockedPageFn(func(ip string) ([]byte, bool) {
		if !devices.BlockLanding() {
			return nil, false
		}
		reason, blocked := deviceBlockReason(ip)
		if !blocked {
			return nil, false
		}
		return landingHTML(reason), true
	})

	// Single config-applied callback wires every effect a role
	// transition has to ripple through:
	//   - DNS resolver listen address (port 53 ownership)
	//   - blocklist refresh on entering extender mode
	//   - TLS leaf re-issue if the LAN gateway moved
	//   - HTTPS redirect on/off (setup → wifi-* flips it on)
	apiSrv.SetOnConfigApplied(func(applied config.Config) {
		dnsServer.SetListen(dnsListenForRole(applied, *dev))
		// Pick up any DNS upstream changes (mode + URL list) the
		// user just made. Idempotent when nothing changed.
		mode, upstreams := dnsUpstreamFromConfig(applied)
		dnsServer.SetUpstreams(mode, upstreams)
		if applied.Role == config.RoleWiFiExtender || applied.Role == config.RoleWiFiRouter {
			dnsDownloader.RefreshNow()
		}
		if tlsMaterials != nil {
			if err := tlsMaterials.Regenerate(leafSubjectFor(applied)); err != nil {
				logger.Printf("tls: regenerate after apply: %v", err)
			}
		}
		srv.SetRedirectHTTPS(shouldRedirectHTTPS(applied, *dev, tlsActive))

		// WireGuard apply: lives in the linux backend (build-tagged).
		// In dev mode the type assertion fails and we silently skip
		// — vpn endpoints still work, we just don't try to wg-quick.
		if wgRegistry != nil {
			if lb, ok := backend.(interface {
				ApplyWireGuard(ctx context.Context, srv vpn.ServerConfig, peers []vpn.Peer) error
			}); ok {
				if err := lb.ApplyWireGuard(ctx, wgRegistry.Server(), wgRegistry.Peers()); err != nil {
					logger.Printf("vpn: apply: %v", err)
				}
			}
		}

		// sing-box subscription routing. Pure render → write →
		// SIGHUP. Manager skips starting the process when no profile
		// asks for a tunnel, so a fresh device with no subs has zero
		// runtime cost.
		lanCIDR := ""
		if applied.Network.LAN != nil {
			lanCIDR = applied.Network.LAN.CIDR
		}
		if lanCIDR != "" {
			res, err := routing.FromRegistries(subsRegistry, devices, profiles, lanCIDR)
			if err != nil {
				logger.Printf("routing: build: %v", err)
			} else {
				if len(res.MissingOutbounds) > 0 {
					logger.Printf("routing: %d missing outbounds, kill-switching affected devices: %v",
						len(res.MissingOutbounds), res.MissingOutbounds)
				}
				// Bring Xray up first so its loopback SOCKS upstreams
				// are listening before sing-box starts dialing them.
				if err := xrayMgr.Apply(ctx, res.XrayConfig); err != nil {
					logger.Printf("xray: apply: %v", err)
				}
				if err := singboxMgr.Apply(ctx, res.Config); err != nil {
					logger.Printf("singbox: apply: %v", err)
				}
			}
		}

		// Zapret (nfqws DPI-bypass). Reconciled on every config-applied
		// so a toggle/strategy change takes effect immediately. Egress
		// interface is the WAN in router mode, the uplink in extender.
		zwan := zapretEgressIface(applied, backend)
		zs := zapret.Settings{WANInterface: zwan}
		if applied.Network.Zapret != nil {
			zs.Enabled = applied.Network.Zapret.Enabled
			zs.Strategy = applied.Network.Zapret.Strategy
			zs.CustomArgs = applied.Network.Zapret.CustomArgs
		}
		if err := zapretMgr.Apply(ctx, zs); err != nil {
			logger.Printf("zapret: apply: %v", err)
		}
	})

	// Fire the apply chain once at boot so persisted state that lives
	// outside backend.Apply — the WireGuard server and, crucially, the
	// sing-box per-device routing — is reflected on the running system
	// without waiting for the user to poke a setting. Without this a
	// reboot leaves every tunneled device going direct until the next
	// profile/subscription/config change re-triggers the callback.
	apiSrv.FireConfigApplied()

	if err := srv.Start(ctx); err != nil {
		logger.Fatalf("server error: %v", err)
	}
	logger.Println("shutdown complete")
}
