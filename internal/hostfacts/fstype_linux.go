//go:build linux

package hostfacts

import (
	"os"
	"path/filepath"
	"syscall"
)

// defaultFSType reads the filesystem kind under path, walking up to the nearest directory that exists (the
// state directory may not have been created yet).
func defaultFSType(path string) (string, error) {
	p := path
	for {
		if _, err := os.Stat(p); err == nil {
			break
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", nil
		}
		p = parent
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(p, &st); err != nil {
		return "", err
	}
	switch uint32(st.Type) {
	case 0x01021994:
		return "tmpfs", nil
	case 0x858458f6:
		return "ramfs", nil
	}
	return "other", nil
}
