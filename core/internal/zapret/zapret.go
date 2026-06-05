// Package zapret integrates the nfqws DPI-circumvention engine
// (github.com/bol-van/zapret) so KnotOS can reach YouTube / Discord
// through ISP throttling without a full tunnel.
//
// How it fits together:
//
//   - nfqws is a userspace daemon that reads packets from an nftables
//     NFQUEUE and applies DPI-desync (TLS/QUIC fragmentation, fake
//     packets). It only touches connections whose SNI/QUIC-SNI is on a
//     hostlist, so the rest of the LAN's traffic is untouched.
//   - The nftables hook lives in its OWN table (`inet zapret`), applied
//     and torn down independently of the main knot ruleset, so a bad
//     queue rule can never break NAT/forwarding. The queue rule uses
//     `bypass`, so if nfqws is down packets pass through normally.
//
// Updatability (the important design constraint): the desync STRATEGY
// and the domain LISTS are data on disk under RuntimeDir, not baked
// into knotd. The binary ships only seed defaults (written on first run
// when absent) plus the engine. Strategies are editable in the UI
// (preset dropdown + free-text override) and lists refresh from the
// upstream Flowseal repo on demand — both without a new knotd build.
package zapret

import "path"

const (
	// Version is the pinned bol-van/zapret release the download
	// fallback fetches when no image-staged binary is present.
	Version = "72.12"
	// NfqwsSHA256 is the sha256 of binaries/linux-arm64/nfqws inside
	// the pinned release tarball — verified after extraction before
	// the downloaded binary is ever executed.
	NfqwsSHA256 = "79d3e81bbea96fe6794f9f365c536248ba86ccd4a675065fbc4badd677e24fe1"
	// NfqwsTarMember is the path of the arm64 binary inside the tarball.
	NfqwsTarMember = "zapret-v72.12/binaries/linux-arm64/nfqws"
	// DownloadURL is the release tarball carrying the static binary.
	DownloadURL = "https://github.com/bol-van/zapret/releases/download/v72.12/zapret-v72.12.tar.gz"

	// QueueNum is the NFQUEUE number shared by the nft rule and nfqws.
	QueueNum = 200
	// DesyncMark is nfqws's fwmark on reinjected packets; the nft rule
	// skips marked packets so fakes aren't re-queued in a loop.
	DesyncMark = 0x40000000

	// RuntimeDir is the writable root for on-disk, updatable assets.
	RuntimeDir = "/var/lib/knot/zapret"
	// ImageBinPath is where image/build.sh stages nfqws. Preferred
	// over a downloaded copy when present (trusted, signed image).
	ImageBinPath = "/usr/local/bin/nfqws"

	// ListsRefreshBase is the upstream raw root the "refresh lists"
	// action pulls updated domain lists / fake payloads from.
	ListsRefreshBase = "https://raw.githubusercontent.com/Flowseal/zapret-discord-youtube/main"
)

// ListsDir is where domain hostlists live under base.
func ListsDir(base string) string { return path.Join(base, "lists") }

// FakeDir is where fake-payload .bin files live under base.
func FakeDir(base string) string { return path.Join(base, "bin") }

// DownloadedBinPath is where a fetched nfqws is cached under base.
func DownloadedBinPath(base string) string { return path.Join(base, "nfqws") }

// Settings is the live, platform-agnostic engine configuration the
// Manager acts on. main.go translates config.Zapret into this.
type Settings struct {
	// Enabled turns the engine + its nft hook on.
	Enabled bool
	// Strategy is the preset ID; ignored when CustomArgs is set.
	Strategy string
	// CustomArgs, when non-empty, is the raw nfqws argument string
	// (overrides the preset). {LISTS} and {BIN} placeholders are
	// expanded to the on-disk asset directories.
	CustomArgs string
	// WANInterface is the egress interface the nft queue rule matches
	// (oifname). Empty disables the hook (no WAN, e.g. extender role).
	WANInterface string
}
