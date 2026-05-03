package update

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
// When the constant is empty (a developer's local `go build`),
// signature verification is skipped with a loud warning. That keeps
// development frictionless without quietly disabling security in
// shipped images — official builds always set the flag.
var releaseKeyHex = ""

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
