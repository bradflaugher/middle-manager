//go:build !windows

package agents

import (
	"os/exec"
	"syscall"
)

// setProcGroup puts the agent in its own process group so we can later signal
// the whole tree (the agent plus any shells / test runners it spawns).
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcGroup signals the agent's process group. With Setpgid the child is
// its own group leader (pgid == pid), so -pid targets the whole group. force
// escalates from SIGTERM (graceful) to SIGKILL.
func killProcGroup(cmd *exec.Cmd, force bool) {
	if cmd.Process == nil {
		return
	}
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	_ = syscall.Kill(-cmd.Process.Pid, sig)
}
