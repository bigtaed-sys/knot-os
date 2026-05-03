// Package main is the entry point for knotd, the KnotOS daemon.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"net"

	"github.com/knot-os/knot-os/core/internal/api"
	"github.com/knot-os/knot-os/core/internal/auth"
	"github.com/knot-os/knot-os/core/internal/config"
	"github.com/knot-os/knot-os/core/internal/deviceregistry"
	knotdns "github.com/knot-os/knot-os/core/internal/dns"
	"github.com/knot-os/knot-os/core/internal/httpserver"
	"github.com/knot-os/knot-os/core/internal/network"
	netlinux "github.com/knot-os/knot-os/core/internal/network/linux"
	"github.com/knot-os/knot-os/core/internal/plugin"
	"github.com/knot-os/knot-os/core/internal/profile"
	"github.com/knot-os/knot-os/core/internal/scheduler"
	"github.com/knot-os/knot-os/core/internal/secrets"
	knottls "github.com/knot-os/knot-os/core/internal/tls"
	"github.com/knot-os/knot-os/core/internal/update"
)

// schedulerDevices adapts deviceregistry.Registry to the scheduler's
// minimal DeviceProvider interface.
type schedulerDevices struct{ r *deviceregistry.Registry }

func (s schedulerDevices) List() []scheduler.Device {
	all := s.r.List()
	out := make([]scheduler.Device, 0, len(all))
	for _, d := range all {
		out = append(out, scheduler.Device{MAC: d.MAC, ProfileID: d.ProfileID})
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
	if !*dev {
		leasePath = "/var/lib/misc/dnsmasq.leases"
	}
	devices := deviceregistry.NewRegistry(deviceregistry.Options{
		StoreFile: "/etc/knot/devices.yaml",
		LeaseFile: leasePath,
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
	logger.Printf("device registry: %d known", len(devices.List()))

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
		Devices:  schedulerDevices{r: devices},
		Profiles: schedulerProfiles{r: profiles},
		Updater:  updater,
		Logger:   logger,
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
	dnsServer := knotdns.New(knotdns.Options{
		Listen:     dnsListenForRole(cfg, *dev),
		Blocklists: dnsBlocklists,
		Devices:    dnsDeviceLookup{devices: devices, profiles: profiles},
		Log:        dnsLog,
		Cache:      dnsCache,
		Logger:     logger,
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

	apiSrv := api.New(api.Options{
		ConfigPath: *configPath,
		Initial:    cfg,
		Version:    Version,
		Backend:    backend,
		Sessions:   auth.NewSessions(),
		Plugins:    plugins,
	})
	apiSrv.SetDeviceRegistry(devices)
	apiSrv.SetProfileRegistry(profiles)
	apiSrv.SetDNSServices(dnsLog, dnsBlocklists, dnsDownloader)
	apiSrv.SetSealer(sealer)
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
		updater, err := update.New(update.Options{
			CurrentVersion: Version,
			Logger:         logger,
		})
		if err != nil {
			logger.Printf("update: %v — auto-update endpoints disabled", err)
		} else {
			apiSrv.SetUpdateManager(updater)
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

	// Single config-applied callback wires every effect a role
	// transition has to ripple through:
	//   - DNS resolver listen address (port 53 ownership)
	//   - blocklist refresh on entering extender mode
	//   - TLS leaf re-issue if the LAN gateway moved
	//   - HTTPS redirect on/off (setup → wifi-* flips it on)
	apiSrv.SetOnConfigApplied(func(applied config.Config) {
		dnsServer.SetListen(dnsListenForRole(applied, *dev))
		if applied.Role == config.RoleWiFiExtender || applied.Role == config.RoleWiFiRouter {
			dnsDownloader.RefreshNow()
		}
		if tlsMaterials != nil {
			if err := tlsMaterials.Regenerate(leafSubjectFor(applied)); err != nil {
				logger.Printf("tls: regenerate after apply: %v", err)
			}
		}
		srv.SetRedirectHTTPS(shouldRedirectHTTPS(applied, *dev, tlsActive))
	})

	if err := srv.Start(ctx); err != nil {
		logger.Fatalf("server error: %v", err)
	}
	logger.Println("shutdown complete")
}
