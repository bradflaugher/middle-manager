//go:build !windows

package loop

import (
	"errors"
	"os"
	"syscall"
)

// pidAlive reports whether a process exists (signal-0 probe). EPERM counts as
// alive: it means the process exists but belongs to another user — exactly
// the case where clobbering its repo lock would be worst.
func pidAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
