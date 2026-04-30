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
	"github.com/knot-os/knot-os/core/internal/config"
	"github.com/knot-os/knot-os/core/internal/httpserver"
)

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

	apiSrv := api.New(api.Options{
		ConfigPath: *configPath,
		Initial:    cfg,
		Version:    Version,
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
