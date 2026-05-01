//go:build linux

package linux

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// UpdateBlockedMACs replaces the contents of the nftables block-set
// "blocked_macs" inside our `inet knot` table. The forward chain has
// a rule `ether saddr @blocked_macs drop`, so any MAC in the set
// loses internet (but keeps LAN-side connectivity).
//
// Idempotent: safe to call from the scheduler every 30s. The flush
// is intentional — re-applying the same set is much cheaper than
// computing the diff and incrementally updating.
//
// If the table doesn't exist (e.g. setup role, or knotd booted but
// applyExtender hasn't run yet), errors are ignored.
func (b *LinuxBackend) UpdateBlockedMACs(macs []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Flush even when macs is empty — that drops any stale entries
	// from a previous tick.
	b.r.runIgnoreError(ctx, "nft", "flush", "set", "inet", "knot", "blocked_macs")
	if len(macs) == 0 {
		return nil
	}
	// Build a single `add element` command. nft accepts a
	// comma-separated list inside braces.
	expr := "{ " + strings.Join(macs, ", ") + " }"
	if err := b.r.runOK(ctx, "nft", "add", "element", "inet", "knot", "blocked_macs", expr); err != nil {
		// "table does not exist" is normal during setup mode, where
		// the extender ruleset hasn't been loaded. Surface other
		// errors though.
		if strings.Contains(err.Error(), "No such file or directory") ||
			strings.Contains(err.Error(), "does not exist") {
			return nil
		}
		return fmt.Errorf("nft add element: %w", err)
	}
	return nil
}
