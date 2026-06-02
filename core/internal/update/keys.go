package update

import "crypto/ed25519"

// ReleasePublicKey returns the embedded release public key (the same
// key auto-update verifies against), or nil if none is baked in. The
// plugin store uses it as the trust anchor for "official" packages —
// a plugin signed by this key installs without the third-party
// confirmation prompt.
func ReleasePublicKey() ed25519.PublicKey {
	k, err := decodeKey(releaseKeyHex)
	if err != nil {
		return nil
	}
	return k
}

// releaseKeyHex is the hex-encoded Ed25519 public half of the
// release-signing key. Empty in source: production builds inject
// the value at link time via
//
//	go build -ldflags '-X github.com/knot-os/knot-os/core/internal/update.releaseKeyHex=<32-byte hex>'
//
// CI signs each released `knotd-linux-arm64` with the corresponding
// private half (kept out of git, lives only in the GitHub Actions
// secret store) and uploads `knotd-linux-arm64.sig` next to the
// binary. Devices running an official build verify against this
// value at update time.
//
// This is the PUBLIC half of the release key — safe to commit. Baking
// it in as the default (rather than leaving it empty and relying on
// the -ldflags injection only in the release CI) means every build —
// the flashed image included — trusts official releases. Without it,
// a device built by image/build.sh (which does not inject the key)
// would refuse every GitHub auto-update, since it would trust only the
// per-device rescue key. The release CI still passes the same value
// via -ldflags; that override is a no-op now but keeps the pipeline's
// "key is present" sanity check meaningful.
//
// When the constant is empty (e.g. a fork that stripped it), signature
// verification falls back to the rescue key alone, or is skipped with a
// loud warning if neither key is configured.
var releaseKeyHex = "f470c961fc95a9431c0497069cc08e70672756b394195de8da505084170e1f1c"

// rescueKeyHex is the hex-encoded Ed25519 public half of an
// optional second authorising key. Set per-device on first run
// (M17b — not implemented yet in v0.3.0): the daemon generates a
// keypair, prints the private half once for the user to save in a
// password manager, and bakes the public half here for a future
// boot. A user with the rescue private key can sign self-built
// knotds and pass `verify` even if the official release key has
// been compromised / rotated.
//
// Empty by default: rescue-key bootstrap lands in M17b and is opt-in.
var rescueKeyHex = ""
