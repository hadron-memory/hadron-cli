//go:build !windows

package hostpoller

import (
	"fmt"
	"os"
	"os/exec"
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
	for _, name := range namedFiles(c, next, changedWorkers) {
		target := name
		if !strings.ContainsRune(name, '/') {
			p, err := exec.LookPath(name)
			if err != nil {
				continue
			}
			target = p
		}
		if err := trustedPath(target, "a program or directory the changed entries name"); err != nil {
			return err
		}
	}
	return nil
}

func trustedPath(path, what string) error {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	return trustedInfo(info, path, what)
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
