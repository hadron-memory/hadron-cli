//go:build windows

package teamrouter

import (
	"errors"

	"golang.org/x/sys/windows"
)

// processGone is true only on proof no process has pid: OpenProcess refuses
// an unknown pid with ERROR_INVALID_PARAMETER, or the process has exited.
// Access denied is a live process under another user, never displaced.
func processGone(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_INVALID_PARAMETER)
	}
	defer windows.CloseHandle(h) //nolint:errcheck // nothing to do with a close failure
	var code uint32
	if windows.GetExitCodeProcess(h, &code) != nil {
		return false
	}
	return code != 259 // STILL_ACTIVE
}

// processStartedAtMS is pid's creation time.
func processStartedAtMS(pid int) (int64, bool) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return 0, false
	}
	defer windows.CloseHandle(h) //nolint:errcheck // nothing to do with a close failure
	var created, exited, kernel, user windows.Filetime
	if windows.GetProcessTimes(h, &created, &exited, &kernel, &user) != nil {
		return 0, false
	}
	return created.Nanoseconds() / 1e6, true
}

// bootEpochMS is unknown on Windows: owner records omit it, and the
// reboot proof is skipped (the pid proofs still apply).
func bootEpochMS() (int64, bool) { return 0, false }
