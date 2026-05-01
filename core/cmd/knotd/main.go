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

// dnsListenForRole derives the address knotd's DNS resolver should
// bind to given the current config. Returns "" when no listener
// should be active.
//
//	role=setup           → "" (dnsmasq holds 53 for the captive portal)
//	role=wifi-extender   → "<gateway>:53"
//	dev mode             → "" (no port 53 binding on a developer's box)
func dnsListenForRole(cfg config.Config, devMode bool) string {
	if devMode {
		return ""
	}
	if cfg.Role != config.RoleWiFiExtender {
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
		showVersion = flag.Bool("version", false, "print version and exit")
		dev         = flag.Bool("dev", false, "run in dev mode with mock network backend")
		configPath  = flag.String("config", "/etc/knot/config.yaml", "path to configuration file")
		listenAddr  = flag.String("listen", ":80", "HTTP listen address")
		pluginsDir  = flag.String("plugins-dir", "/usr/lib/knot/plugins", "directory containing installed plugins")
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

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Fatalf("load config: %v", err)
	}
	logger.Printf("config loaded: device=%q role=%q", cfg.Device.Name, cfg.Role)

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

	// DNS resolver: started always, but the listen address is
	// derived from the current role (empty in setup mode where
	// dnsmasq's own DNS catch-all owns port 53; gateway:53 in
	// wifi-extender mode where dnsmasq is configured port=0).
	dnsServer := knotdns.New(knotdns.Options{
		Listen:     dnsListenForRole(cfg, *dev),
		Blocklists: dnsBlocklists,
		Devices:    dnsDeviceLookup{devices: devices, profiles: profiles},
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
	// SchedulerKick lets the API trigger an immediate scheduler tick
	// after a device's profile or a profile's schedule changes.
	apiSrv.SetSchedulerKick(func() {
		go sched.RunOnce()
	})
	// On every config-apply (PUT /api/config or setup wizard
	// completion), update the DNS resolver's listen address to
	// match the new role. Goes through SetListen which is
	// idempotent for unchanged addresses.
	apiSrv.SetOnConfigApplied(func(applied config.Config) {
		dnsServer.SetListen(dnsListenForRole(applied, *dev))
		// Trigger an immediate refresh of cached blocklists when
		// transitioning into wifi-extender so the device gets ad-block
		// from the first query.
		if applied.Role == config.RoleWiFiExtender {
			dnsDownloader.RefreshNow()
		}
	})
	// Production mode unlocks the system endpoints (reboot/shutdown/
	// update) that would be destructive in dev. Tied to the absence
	// of -dev because that's the same condition that picks the real
	// LinuxBackend.
	apiSrv.SetProductionMode(!*dev)

	srv := httpserver.New(httpserver.Options{
		Addr:   *listenAddr,
		Logger: logger,
	})
	srv.Mount("/api", apiSrv.Handler())

	if err := srv.Start(ctx); err != nil {
		logger.Fatalf("server error: %v", err)
	}
	logger.Println("shutdown complete")
}
