//go:build unix

package team

import (
	"os"
	"path/filepath"
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

// unsafeAncestors judges every directory above path after resolving links: one another user owns (root and you are
// trusted) or that group or others can write (a sticky one, like /tmp, may be) lets someone swap the file. "" is safe.
func unsafeAncestors(path string) string {
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "has a directory that cannot be resolved"
	}
	for {
		fi, err := os.Lstat(dir)
		if err != nil {
			return "has a directory that cannot be read"
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); !ok || (st.Uid != 0 && int(st.Uid) != os.Geteuid()) {
			return "is in a directory another user owns"
		}
		if fi.Mode().Perm()&0o011 == 0 {
			// Group and others cannot enter this directory, so nothing above it matters: they cannot reach the file.
			return ""
		}
		if fi.Mode().Perm()&0o022 != 0 && fi.Mode()&os.ModeSticky == 0 {
			return "is in a directory group or others can write"
		}
		up := filepath.Dir(dir)
		if up == dir {
			return ""
		}
		dir = up
	}
}
