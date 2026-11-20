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
	for _, nf := range namedFiles(c, next, changedWorkers) {
		if nf.program {
			// A relative program (a slash, not absolute) is run from the
			// worker's directory, which the check cannot know here — a worker
			// can have several worktrees and the directory is picked when the
			// operation runs — so judging it against the poller's directory
			// would judge one file and run another (cli#2668).
			if strings.ContainsRune(nf.path, '/') && !filepath.IsAbs(nf.path) {
				return fmt.Errorf("a program the changed entries name (%s) is a relative path; it is run from the worker's directory, so it cannot be judged here; use an absolute path or a bare name, or restart the poller to apply the change", nf.path)
			}
		}
		target := nf.path
		if !strings.ContainsRune(target, '/') {
			p, err := exec.LookPath(target)
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

// statPath reads a path's metadata following symlinks, as the kernel does; a
// variable so a test can show what happens when a path cannot be stat'ed.
var statPath = os.Stat

// cannotJudge refuses a path the walk could not read: a reload runs what the
// entries name on the next poll, so an unreadable path — unlike a missing
// one, judged as far as it exists — cannot be judged part-way (cli#2668).
func cannotJudge(what, path string, err error) error {
	return fmt.Errorf("%s (%s) cannot be judged: %v; a reload runs what it names on the next poll, so restart the poller to apply the change", what, path, err)
}

func trustedPath(path, what string) error {
	info, err := statPath(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return cannotJudge(what, path, err)
	}
	return trustedInfo(info, path, what)
}

// trustedAncestors judges every directory touched in resolving the path, one
// component at a time, as the kernel does: each directory a component is
// looked up in, and, when a component is a symlink, the directories its
// target is resolved through (a chain a/link -> b/link2 -> c/prog touches a's,
// b's and c's directories). Someone who can write any of them can repoint a
// link or replace what is below, so each must belong to the poller's user or
// root and not be writable by group or others, unless it is sticky (a sticky
// directory, like /tmp, lets others add entries but not replace ours). A path
// that does not exist yet is judged as far as it does.
func trustedAncestors(path, what string) error {
	dirs, links, err := visitedDirs(path)
	if err != nil {
		return cannotJudge(what, path, err)
	}
	for _, d := range dirs {
		info, err := statPath(d)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return cannotJudge(what, path, err)
		}
		if err := trustedDirInfo(info, d, path, what); err != nil {
			return err
		}
	}
	// A symlink sits in a directory the walk may have accepted (a sticky one,
	// where anyone may add an entry), so each link followed is judged on its
	// own: it must belong to the poller's user or root, or its target is
	// whatever its owner chose.
	for _, l := range links {
		info, err := lstatLink(l)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return cannotJudge(what, path, err)
		}
		if err := trustedLinkInfo(info, l, path, what); err != nil {
			return err
		}
	}
	return nil
}

// lstatLink reads a followed symlink's own metadata; a variable so a test can
// show a link another user owns (a test cannot create one).
var lstatLink = os.Lstat

// trustedLinkInfo judges the owner of a symlink followed on the way to a path.
// A symlink's own mode carries no meaning, so only its owner is checked.
func trustedLinkInfo(info os.FileInfo, link, of, what string) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if uid := int(st.Uid); uid != os.Geteuid() && uid != 0 {
		return fmt.Errorf("the symlink %s on the path to %s (%s) is owned by uid %d, not the poller's user or root; whoever owns it chose where it leads, so restart the poller to apply the change", link, of, what, uid)
	}
	return nil
}

// maxSymlinks bounds a resolution, as the kernel does (ELOOP): a path that
// needs more is refused, not judged part-way.
const maxSymlinks = 40

// visitedDirs lists, in order and without repeats, the directories visited
// resolving path component by component, and the symlinks followed on the
// way. The final component, when it is not a symlink, is not included (that
// file or directory is judged by the caller).
func visitedDirs(path string) (dirs, links []string, err error) {
	// The path as written, made absolute but NOT cleaned: ".." after a symlink
	// goes to the link target's parent, not the written path's, so cleaning
	// first would judge directories the kernel never visits.
	abs := path
	if !filepath.IsAbs(abs) {
		wd, err := os.Getwd()
		if err != nil {
			return nil, nil, err
		}
		abs = wd + "/" + abs
	}
	seen := map[string]bool{}
	add := func(d string) {
		if !seen[d] {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	split := func(p string) []string {
		var parts []string
		for _, c := range strings.Split(p, "/") {
			if c != "" && c != "." {
				parts = append(parts, c)
			}
		}
		return parts
	}
	cur, parts, followed := "/", split(abs), 0
	for len(parts) > 0 {
		c := parts[0]
		parts = parts[1:]
		if c == ".." {
			cur = filepath.Dir(cur)
			continue
		}
		add(cur)
		next := filepath.Join(cur, c)
		fi, err := os.Lstat(next)
		if err != nil {
			if !os.IsNotExist(err) {
				return dirs, links, err // cannot be judged: refuse, do not skip
			}
			cur = next
			continue
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			cur = next
			continue
		}
		if followed++; followed > maxSymlinks {
			return dirs, links, fmt.Errorf("it passes through more than %d symlinks", maxSymlinks)
		}
		links = append(links, next)
		target, err := os.Readlink(next)
		if err != nil {
			return dirs, links, err
		}
		if filepath.IsAbs(target) {
			cur = "/"
		}
		parts = append(split(target), parts...)
	}
	return dirs, links, nil
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
