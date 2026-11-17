//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package accountusage

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// openRollout anchors the ConfigDir parent with existing host-prefix alias
// semantics, then rejects links on ConfigDir and every component below it.
// Already-opened directories remain anchored if renamed. The caller still
// verifies the final regular-file identity. Ancestors above ConfigDir are
// outside the attacker-inside-the-home boundary (cli#1666).
func openRollout(home, path string) (*os.File, error) {
	rel, err := filepath.Rel(home, path)
	if err != nil {
		return nil, err
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return nil, os.ErrInvalid
	}
	parts := append([]string{filepath.Base(home)}, strings.Split(rel, string(filepath.Separator))...)
	fd, err := unix.Open(filepath.Dir(home), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}

	for i, part := range parts {
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
		if i < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		next, openErr := unix.Openat(fd, part, flags, 0)
		_ = unix.Close(fd)
		if openErr != nil {
			return nil, openErr
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), path), nil
}
