//go:build linux

package plugin

import (
	"os/exec"
	"syscall"
)

// applySandbox confines a plugin child process. v1 is privilege
// separation + lifetime binding, which buys most of the protection
// with no extra dependencies:
//
//   - Credential: run as an unprivileged uid/gid (a dedicated
//     "knot-plugin" user), so a compromised or buggy plugin can't
//     touch root-owned config/secrets or the rest of the system.
//   - NoSetGroups: don't inherit the daemon's supplementary groups.
//   - Setpgid: own process group, so Stop kills the whole tree.
//   - Pdeathsig SIGKILL: if knotd dies, the kernel reaps the plugin
//     too — no orphaned plugin processes outliving the daemon.
//
// uid<=0 means "no drop" (dev / not configured). Tighter confinement
// (seccomp, mount/network namespaces) is a future layer on top.
func applySandbox(cmd *exec.Cmd, uid, gid int) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
	if uid > 0 {
		cmd.SysProcAttr.Credential = &syscall.Credential{
			Uid:         uint32(uid),
			Gid:         uint32(gid),
			NoSetGroups: true,
		}
	}
}
