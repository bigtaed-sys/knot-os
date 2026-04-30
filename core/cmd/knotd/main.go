// Package main is the entry point for knotd, the KnotOS daemon.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
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

	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("knotd: ")

	if *dev {
		log.Printf("starting in dev mode (mock network backend), config=%s, listen=%s", *configPath, *listenAddr)
	} else {
		log.Printf("starting (linux backend), config=%s, listen=%s", *configPath, *listenAddr)
	}

	log.Println("not implemented yet — this is the M1 skeleton")
	os.Exit(0)
}
