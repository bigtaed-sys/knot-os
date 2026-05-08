package applycoord

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/knot-os/knot-os/core/internal/config"
	"github.com/knot-os/knot-os/core/internal/network"
)

// DefaultHealthChecker is what Coordinator uses when the caller
// doesn't supply a custom checker. It polls backend.Status() until
// the runtime state matches role-specific expectations, then returns.
//
// Per role:
//
//   * setup:         AP up (any SSID — the fact that it's broadcasting
//                    is enough; the connect-portal browser test isn't
//                    something the daemon can do for itself).
//   * wifi-extender: uplink connected (RSSI != 0 or Connected flag set).
//                    AP up too.
//   * wifi-router:   AP up. WAN status is REPORTED but not required —
//                    the apply path explicitly handles "no carrier"
//                    gracefully so that's not a failure mode.
//
// All checks tolerate transient flap: we accept the first matching
// state we see and don't require it to hold for N consecutive polls.
// The poll cadence is 500ms.
func DefaultHealthChecker(b network.Backend) HealthChecker {
	return HealthCheckerFunc(func(ctx context.Context, cfg config.Config) error {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()

		var lastErr error
		// First check immediately, then on every tick. Important:
		// some apply paths return after the daemons are running;
		// waiting another half-second for the first poll is wasteful.
		for {
			st, err := b.Status(ctx)
			if err == nil {
				if checkErr := evaluateStatus(cfg, st); checkErr == nil {
					return nil
				} else {
					lastErr = checkErr
				}
			} else {
				lastErr = err
			}
			select {
			case <-ctx.Done():
				if lastErr == nil {
					return ctx.Err()
				}
				return fmt.Errorf("after %s: %w", DefaultHealthTimeout, lastErr)
			case <-t.C:
			}
		}
	})
}

// evaluateStatus returns nil if the runtime state matches the
// applied config, an error otherwise. The error wording is shown
// to the user via the apply attempt's `error` field, so it should
// be friendly + actionable.
func evaluateStatus(cfg config.Config, st network.Status) error {
	switch cfg.Role {
	case config.RoleSetup:
		if st.AP == nil || !st.AP.Up {
			return errors.New("setup AP did not come up — Wi-Fi chip might be locked or in a bad state")
		}
		return nil

	case config.RoleWiFiExtender:
		if st.AP == nil || !st.AP.Up {
			return errors.New("AP didn't come up after switching to extender mode")
		}
		if st.Uplink == nil || !st.Uplink.Connected {
			// Uplink failure is actually common in the wild (wrong
			// PSK, target SSID went away). Don't masquerade — name it.
			return errors.New("couldn't connect to upstream Wi-Fi — wrong password, or the network isn't in range")
		}
		return nil

	case config.RoleWiFiRouter:
		if st.AP == nil || !st.AP.Up {
			return errors.New("Wi-Fi AP didn't come up — check that the channel/country combination is supported")
		}
		// WAN may be down (cable not plugged) and that's OK; the
		// apply path explicitly tolerates a no-carrier WAN. We log
		// it for diagnostics but don't fail the health check on it.
		return nil
	}
	return fmt.Errorf("unknown role %q in applied config", cfg.Role)
}
