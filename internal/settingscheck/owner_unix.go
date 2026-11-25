//go:build unix

package settingscheck

import (
	"errors"
	"os"
	"syscall"
)

// pathOwnedByUser reports whether path is absent or owned by the current user.
func pathOwnedByUser(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return errors.Is(err, os.ErrNotExist)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Geteuid()
}
