//go:build linux

package linux

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/knot-os/knot-os/core/internal/zapret"
)

// ZapretRunner supervises nfqws plus its isolated nftables hook. It
// implements zapret.Runner so the platform-agnostic zapret.Manager can
// drive the lifecycle without syscall/exec imports.
//
// The nft rule lives in its OWN table (`inet zapret`), applied and torn
// down independently of the main knot ruleset — a malformed queue rule
// can never break NAT/forwarding. The rule uses `queue ... bypass`, so
// if nfqws dies the matched packets pass through untouched rather than
// being dropped.
type ZapretRunner struct {
	mu   sync.Mutex
	proc *supervisedProc
}

// NewZapretRunner builds an empty supervisor.
func NewZapretRunner() *ZapretRunner { return &ZapretRunner{} }

var _ zapret.Runner = (*ZapretRunner)(nil)

const zapretNftPath = "/run/knot/zapret.nft"

// Start applies the nft queue rule for wanIface then (re)starts nfqws.
func (r *ZapretRunner) Start(ctx context.Context, binPath string, args []string, wanIface, tcpPorts, udpPorts string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := applyZapretNft(ctx, wanIface, tcpPorts, udpPorts); err != nil {
		// Isolated table — failure here doesn't touch the main ruleset.
		return fmt.Errorf("zapret: nft: %w", err)
	}

	// Restart nfqws so a strategy change takes effect.
	if r.proc != nil {
		r.proc.Stop()
		r.proc = nil
	}
	r.proc = newSupervisedProc("nfqws", binPath, args...)
	if err := r.proc.Start(ctx); err != nil {
		r.proc = nil
		return fmt.Errorf("zapret: start nfqws: %w", err)
	}
	return nil
}

// Stop stops nfqws and removes the nft table. Best-effort.
func (r *ZapretRunner) Stop(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.proc != nil {
		r.proc.Stop()
		r.proc = nil
	}
	deleteZapretNft(ctx)
	return nil
}

// Running reports the supervisor's view of nfqws.
func (r *ZapretRunner) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.proc != nil && r.proc.Running()
}

// applyZapretNft writes and loads the isolated zapret table. Outbound
// TLS/HTTP/QUIC and Discord-voice ports on the WAN are sent to nfqws's
// NFQUEUE; only the first few packets per flow (where the ClientHello /
// QUIC-initial lives) are queued, and nfqws's own reinjected (marked)
// packets are skipped so fakes don't loop.
func applyZapretNft(ctx context.Context, wanIface, tcpPorts, udpPorts string) error {
	if tcpPorts == "" {
		tcpPorts = zapret.DefaultTCPPorts
	}
	if udpPorts == "" {
		udpPorts = zapret.DefaultUDPPorts
	}
	ruleset := fmt.Sprintf(`table inet zapret {
    chain post {
        type filter hook postrouting priority -150; policy accept;
        meta mark and 0x%08x == 0x%08x accept
        oifname "%[3]s" tcp dport { %[5]s } ct original packets 1-6 queue num %[4]d bypass
        oifname "%[3]s" udp dport { %[6]s } ct original packets 1-12 queue num %[4]d bypass
    }
}
`, zapret.DesyncMark, zapret.DesyncMark, wanIface, zapret.QueueNum, nftPortSet(tcpPorts), nftPortSet(udpPorts))

	if err := os.MkdirAll(filepath.Dir(zapretNftPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(zapretNftPath, []byte(ruleset), 0o644); err != nil {
		return err
	}
	// Replace any prior table atomically: delete then load.
	deleteZapretNft(ctx)
	return runNft(ctx, "-f", zapretNftPath)
}

// nftPortSet turns "80,443,19294-19344" into "80, 443, 19294-19344"
// for an nft anonymous set body. Ranges (a-b) pass through unchanged.
func nftPortSet(csv string) string {
	parts := strings.Split(csv, ",")
	clean := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			clean = append(clean, p)
		}
	}
	return strings.Join(clean, ", ")
}

func deleteZapretNft(ctx context.Context) {
	// Ignore "No such file or directory" on first apply / when absent.
	_ = exec.CommandContext(ctx, "nft", "delete", "table", "inet", "zapret").Run()
}

func runNft(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "nft", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
