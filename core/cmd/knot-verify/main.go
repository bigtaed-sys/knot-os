// knot-verify checks a detached Ed25519 signature against a file.
// Used by the release CI as a sanity step after knot-sign and
// before publishing — same code knotd runs on a device, so a
// successful verify here guarantees the device-side check will
// pass too.
//
// Usage:
//
//	knot-verify -key <hex-public-key> -in <file> -sig <signature>
//
// Exits 0 on a valid signature, 1 with a message on anything else.
package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
)

func main() {
	keyHex := flag.String("key", "", "hex-encoded ed25519 public key (32 bytes -> 64 hex chars)")
	inPath := flag.String("in", "", "file whose signature to verify")
	sigPath := flag.String("sig", "", "detached signature file")
	flag.Parse()
	if *keyHex == "" || *inPath == "" || *sigPath == "" {
		fmt.Fprintln(os.Stderr, "knot-verify: -key, -in, -sig are all required")
		os.Exit(2)
	}

	keyBytes, err := hex.DecodeString(*keyHex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "knot-verify: parse key: %v\n", err)
		os.Exit(1)
	}
	if len(keyBytes) != ed25519.PublicKeySize {
		fmt.Fprintf(os.Stderr, "knot-verify: key must be %d bytes (got %d)\n",
			ed25519.PublicKeySize, len(keyBytes))
		os.Exit(1)
	}
	pub := ed25519.PublicKey(keyBytes)

	body, err := os.ReadFile(*inPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "knot-verify: read %s: %v\n", *inPath, err)
		os.Exit(1)
	}
	sig, err := os.ReadFile(*sigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "knot-verify: read %s: %v\n", *sigPath, err)
		os.Exit(1)
	}

	if !ed25519.Verify(pub, body, sig) {
		fmt.Fprintf(os.Stderr, "knot-verify: signature does not match\n")
		os.Exit(1)
	}
	fmt.Println("OK")
}
