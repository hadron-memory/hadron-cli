//go:build !windows

package team

import (
	"os"
	"syscall"
)

// unsafeFile judges an Lstat result: a symlink or anything but a regular file, a file another user owns, or one
// group or others can write is refused. "" means safe. The same no-follow posture as the host credential store.
func unsafeFile(fi os.FileInfo) string {
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		return "is a symlink"
	case !fi.Mode().IsRegular():
		return "is not a regular file"
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Geteuid() {
		return "is not owned by you"
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return "is writable by group or others"
	}
	return ""
}

// openNoFollow opens path for reading without following a symlink at its last component.
func openNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}
