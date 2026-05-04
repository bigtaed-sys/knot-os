// knot-keygen prints a fresh Ed25519 keypair as two hex strings.
// Used once per project lifetime to seed the GitHub Actions secrets
// RELEASE_PUBLIC_KEY and RELEASE_PRIVATE_KEY (and the per-device
// rescue key if a user wants to bake one into their own build).
//
// Output format:
//
//	public:  <64 hex chars>
//	private: <128 hex chars>
//
// Save the private half somewhere safe; if it leaks, anyone can
// sign a knotd that the running daemon will accept.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
)

func main() {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Fprintf(os.Stderr, "knot-keygen: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("public:  %s\n", hex.EncodeToString(pub))
	fmt.Printf("private: %s\n", hex.EncodeToString(priv))
}
