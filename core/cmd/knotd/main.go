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

	"github.com/knot-os/knot-os/core/internal/api"
	"github.com/knot-os/knot-os/core/internal/auth"
	"github.com/knot-os/knot-os/core/internal/config"
	"github.com/knot-os/knot-os/core/internal/httpserver"
	"github.com/knot-os/knot-os/core/internal/network"
	netlinux "github.com/knot-os/knot-os/core/internal/network/linux"
)

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

	apiSrv := api.New(api.Options{
		ConfigPath: *configPath,
		Initial:    cfg,
		Version:    Version,
		Backend:    backend,
		Sessions:   auth.NewSessions(),
	})

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
