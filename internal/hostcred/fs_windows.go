//go:build windows

package hostcred

import (
	"errors"
	"os"
)

// tooOpen is never true on Windows: a Go mode there only reflects the
// read-only attribute. The file's owner-only access list is set when it is
// written (internal/secretfile), as for the host key file.
func tooOpen(os.FileInfo) bool { return false }

var errSymlink = errors.New("symlink")

// openNoFollow refuses a reparse point (a symlink or junction) at path, then
// opens it.
func openNoFollow(path string) (*os.File, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&os.ModeSymlink != 0 || fi.Mode()&os.ModeIrregular != 0 {
		return nil, errSymlink
	}
	return os.Open(path)
}

func isSymlinkErr(err error) bool { return errors.Is(err, errSymlink) }
