//go:build !windows

package hostpoller

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// checkReloadTrust refuses a reload whose inputs anyone but the poller's own
// user could have written. It stops other users and loose modes; a process
// running as the poller's own user is inside the poller's trust boundary, as
// it is for a restart, and no check here could tell it apart: the configuration file, and
// each program or directory the changed entries name, must be owned by the
// poller's user or by root and not writable by group or others (cli#2623).
// A name that does not resolve is not an error here: starting that worker
// fails on it as before.
func checkReloadTrust(c, next *Config, path string, info os.FileInfo, changedWorkers []string) error {
	if err := trustedInfo(info, path, "the configuration file"); err != nil {
		return err
	}
	if err := trustedAncestors(path, "the configuration file"); err != nil {
		return err
	}
	for _, name := range namedFiles(c, next, changedWorkers) {
		target := name
		if !strings.ContainsRune(name, '/') {
			p, err := exec.LookPath(name)
			if err != nil {
				continue
			}
			target = p
		}
		if err := trustedChain(target, "a program or directory the changed entries name"); err != nil {
			return err
		}
	}
	return nil
}

// trustedChain judges the path itself and every directory above it.
func trustedChain(path, what string) error {
	if err := trustedPath(path, what); err != nil {
		return err
	}
	return trustedAncestors(path, what)
}

func trustedPath(path, what string) error {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	return trustedInfo(info, path, what)
}

// trustedAncestors judges each directory from the path's parent up to the
// root: someone who can write a directory on the way can replace what is
// below it, so each must belong to the poller's user or root and not be
// writable by group or others, unless it is sticky (a sticky directory, like
// /tmp, lets others add entries but not replace ours). A path that does not
// exist yet is judged from the nearest directory that does.
func trustedAncestors(path, what string) error {
	// Two walks: the resolved path (where the file really is) and the path as
	// written (a directory that merely holds a symlink on the way can be
	// rewritten to point elsewhere, so it is judged even though the resolved
	// path never passes through it).
	lexical, err := filepath.Abs(path)
	if err != nil {
		lexical = filepath.Clean(path)
	}
	for _, start := range []string{resolveExisting(path), lexical} {
		for d := filepath.Dir(start); ; d = filepath.Dir(d) {
			info, err := os.Stat(d)
			if err == nil {
				if err := trustedDirInfo(info, d, path, what); err != nil {
					return err
				}
			}
			if d == filepath.Dir(d) {
				break
			}
		}
	}
	return nil
}

// resolveExisting is path with symlinks resolved as far as it exists, so the
// ancestors judged are the real ones.
func resolveExisting(path string) string {
	path = filepath.Clean(path)
	rest := ""
	for p := path; ; p = filepath.Dir(p) {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(real, rest)
		}
		if p == filepath.Dir(p) {
			return path
		}
		rest = filepath.Join(filepath.Base(p), rest)
	}
}

func trustedDirInfo(info os.FileInfo, dir, of, what string) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if uid := int(st.Uid); uid != os.Geteuid() && uid != 0 {
		return fmt.Errorf("%s (%s) is on the path to %s and is owned by uid %d, not the poller's user or root; a reload runs what it names on the next poll, so restart the poller to apply it", dir, dir, of, uid)
	}
	if info.Mode().Perm()&0o022 != 0 && info.Mode()&os.ModeSticky == 0 {
		return fmt.Errorf("the directory %s on the path to %s (%s) is writable by group or others and not sticky (mode %o); a reload runs what is below it on the next poll, so tighten it or restart the poller to apply the change", dir, of, what, info.Mode().Perm())
	}
	return nil
}

func trustedInfo(info os.FileInfo, path, what string) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if uid := int(st.Uid); uid != os.Geteuid() && uid != 0 {
		return fmt.Errorf("%s (%s) is owned by uid %d, not the poller's user or root; a reload runs what it names on the next poll, so restart the poller to apply it", what, path, uid)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s (%s) is writable by group or others (mode %o); a reload runs what it names on the next poll, so tighten it (chmod go-w) or restart the poller to apply the change", what, path, info.Mode().Perm())
	}
	return nil
}
