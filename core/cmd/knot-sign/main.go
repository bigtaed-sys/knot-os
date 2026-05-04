// knot-sign produces a detached Ed25519 signature over a file.
//
// Usage:
//
//	knot-sign -key <hex-private-key> -in <input> -out <signature>
//
// Used by the release CI to produce knotd-linux-arm64.sig from
// dist/arm64/knotd. The private key is read from the -key flag
// (which the workflow populates from the RELEASE_PRIVATE_KEY
// secret), never written to disk.
package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	keyHex := flag.String("key", "", "hex-encoded ed25519 private key (64 bytes -> 128 hex chars)")
	inPath := flag.String("in", "", "file to sign")
	outPath := flag.String("out", "", "where to write the detached signature")
	flag.Parse()
	if *keyHex == "" || *inPath == "" || *outPath == "" {
		fmt.Fprintln(os.Stderr, "knot-sign: -key, -in, -out are all required")
		os.Exit(2)
	}

	keyBytes, err := hex.DecodeString(*keyHex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "knot-sign: parse key: %v\n", err)
		os.Exit(1)
	}
	if len(keyBytes) != ed25519.PrivateKeySize {
		fmt.Fprintf(os.Stderr, "knot-sign: key must be %d bytes (got %d)\n",
			ed25519.PrivateKeySize, len(keyBytes))
		os.Exit(1)
	}
	priv := ed25519.PrivateKey(keyBytes)

	in, err := os.Open(*inPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "knot-sign: open %s: %v\n", *inPath, err)
		os.Exit(1)
	}
	defer func() { _ = in.Close() }()
	body, err := io.ReadAll(in)
	if err != nil {
		fmt.Fprintf(os.Stderr, "knot-sign: read %s: %v\n", *inPath, err)
		os.Exit(1)
	}

	sig := ed25519.Sign(priv, body)
	if err := os.WriteFile(*outPath, sig, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "knot-sign: write %s: %v\n", *outPath, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "knot-sign: wrote %d-byte signature to %s\n", len(sig), *outPath)
}
