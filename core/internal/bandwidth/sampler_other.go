//go:build !linux

package bandwidth

import (
	"context"
	"fmt"
)

// LinuxSampler stub — non-Linux dev hosts have no /proc/net/nf_conntrack.
// Run blocks on ctx so a goroutine started in main.go has the right
// shape; sampling produces nothing.
type LinuxSampler struct{}

// IPToMACResolver — kept here so the interface compiles on dev hosts.
type IPToMACResolver interface {
	MACForIP(ip string) (string, bool)
}

// NewLinuxSampler returns a no-op sampler on non-Linux.
func NewLinuxSampler(_ *Tracker, _ IPToMACResolver, _ string) *LinuxSampler {
	return &LinuxSampler{}
}

// Run blocks until ctx done.
func (s *LinuxSampler) Run(ctx context.Context) { <-ctx.Done() }

// FormatRate is platform-agnostic; redeclared so callers can use it
// from any OS without build-tag gymnastics.
func FormatRate(kbps float64) string {
	if kbps < 0.5 {
		return "—"
	}
	if kbps < 1000 {
		return fmt.Sprintf("%.0f Kbps", kbps)
	}
	return fmt.Sprintf("%.1f Mbps", kbps/1000)
}
