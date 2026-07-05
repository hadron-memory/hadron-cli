package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

// WriteFileAtomic writes data to path atomically and with exactly perm,
// replacing any existing file. It refuses to write through a symlink, and
// installs a fresh file via a same-dir temp + rename — so a crash mid-write
// never truncates the target (the reader sees either the old or the new
// complete file, never an empty/partial one), and a pre-existing loose-mode
// file is replaced by one at perm rather than left group/world-readable
// (#117, #118).
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to write %s through a symlink", path)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Best-effort cleanup; a no-op once the rename below has consumed the temp.
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// WithLock runs fn while holding an exclusive advisory lock on the config
// directory, serializing the whole read-modify-write across concurrent
// `hadron` processes (AI agents shell out in parallel). The OS releases the
// lock when the process exits, so a crash never leaves a stale lock. The lock
// file is separate from the data files it guards.
func WithLock(fn func() error) error {
	dir, err := EnsureDir()
	if err != nil {
		return err
	}
	lock := flock.New(filepath.Join(dir, ".lock"))
	if err := lock.Lock(); err != nil {
		return err
	}
	defer func() { _ = lock.Unlock() }()
	return fn()
}
