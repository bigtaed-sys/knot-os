// Package main is the entry point for knotctl, the KnotOS command-line client.
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
		socket      = flag.String("socket", "/run/knot/api.sock", "path to knotd Unix socket")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("knotctl", Version)
		return
	}

	log.SetFlags(0)
	log.SetPrefix("knotctl: ")

	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: knotctl <command> [args...]")
		fmt.Fprintln(os.Stderr, "commands: status, config, snapshot, plugin")
		os.Exit(2)
	}

	_ = socket
	log.Printf("not implemented yet — this is the M1 skeleton (would run %q)", args[0])
}
