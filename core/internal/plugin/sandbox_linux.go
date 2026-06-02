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
// On top of the privilege drop, M5 adds Linux namespace isolation:
//
//   - NEWPID / NEWIPC / NEWUTS: the plugin can't see or signal other
//     processes, share SysV IPC, or read/change the host's hostname.
//     Always on — cheap and safe for a single-process plugin.
//   - NEWNET (default-deny network): unless the plugin declared the
//     "network" permission, it runs in an empty network namespace —
//     no internet, no LAN, not even loopback up. Its Unix sockets (the
//     host API + its own listener) still work because those are
//     filesystem objects, not network ones. A plugin that genuinely
//     needs to reach the internet declares `network` and shares the
//     host net instead.
//
// Namespaces are created at clone time while knotd is still root; the
// uid drop (Credential) happens afterwards. uid<=0 means "no drop"
// (dev / not configured). Tighter layers (seccomp, mount confinement,
// cgroup limits) can stack on top later.
func applySandbox(cmd *exec.Cmd, uid, gid int, allowNetwork bool) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
	cmd.SysProcAttr.Cloneflags |= syscall.CLONE_NEWPID | syscall.CLONE_NEWIPC | syscall.CLONE_NEWUTS
	if !allowNetwork {
		cmd.SysProcAttr.Cloneflags |= syscall.CLONE_NEWNET
	}
	if uid > 0 {
		cmd.SysProcAttr.Credential = &syscall.Credential{
			Uid:         uint32(uid),
			Gid:         uint32(gid),
			NoSetGroups: true,
		}
	}
}
