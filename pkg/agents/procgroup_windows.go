//go:build windows

package agents

import "os/exec"

// setProcGroup is a no-op on Windows (no POSIX process groups). middle-manager
// targets Unix-like systems; this keeps the package buildable on Windows.
func setProcGroup(cmd *exec.Cmd) {}

// killProcGroup kills the agent process on Windows. There is no process-group
// signalling, so graceful (SIGTERM) is skipped and only the forced kill acts.
func killProcGroup(cmd *exec.Cmd, force bool) {
	if cmd.Process == nil {
		return
	}
	if force {
		_ = cmd.Process.Kill()
	}
}
