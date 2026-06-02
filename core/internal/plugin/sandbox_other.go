//go:build !linux

package plugin

import "os/exec"

// applySandbox is a no-op on non-Linux dev hosts: the uid-drop and
// Pdeathsig confinement are Linux-specific. Plugins still run, just
// without OS-level isolation (which only matters on the device).
func applySandbox(_ *exec.Cmd, _, _ int) {}
