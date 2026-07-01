//go:build windows

package loop

import "golang.org/x/sys/windows"

// pidAlive reports whether a process exists. Windows has no signal-0 probe,
// so open a query-only handle and check the exit code: STILL_ACTIVE means the
// repo lock's holder is genuinely running. An access-denied open (another
// user's process) also counts as alive — same reasoning as EPERM on unix.
func pidAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return err == windows.ERROR_ACCESS_DENIED
	}
	defer windows.CloseHandle(h)
	// STILL_ACTIVE (0x103): x/sys/windows doesn't export it by name.
	const stillActive = 259
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return true // handle opened — assume alive rather than clobber the lock
	}
	return code == stillActive
}
