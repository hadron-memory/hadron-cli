//go:build !windows

package hostfacts

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// cli#3285: C3 (cli#3237) executes the program an accounts entry names, so the file and the way to it must be
// the user's own, as the poller's host-file reload check judges its inputs (internal/hostpoller/reloadtrust_unix.go).
// The file must belong to the user (or root); each directory touched in resolving it must belong to the user or
// root and not be writable by group or others unless sticky; each symlink followed on the way must belong to the
// user or root. The file itself being a symlink is refused before this runs (LoadAccounts).

// accountsOwnerOf reads a path's owner; a variable so a test can report another user (a test cannot chown).
var accountsOwnerOf = func(_ string, fi os.FileInfo) (uint32, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Uid, true
}

// trustedOwner names a path's owner for a message and reports whether it is the user or root. An owner that
// cannot be read is not trusted (fail closed).
func trustedOwner(path string, fi os.FileInfo) (string, bool) {
	uid, ok := accountsOwnerOf(path, fi)
	if !ok {
		return "an owner that cannot be read", false
	}
	return fmt.Sprintf("uid %d", uid), int(uid) == os.Geteuid() || uid == 0
}

const accountsRunsWhy = "hadron runs the programs this file names"

// checkAccountsTrust judges the file (already Lstat'ed, a regular file) and everything above it.
func checkAccountsTrust(path string, fi os.FileInfo) error {
	if uid, ok := trustedOwner(path, fi); !ok {
		return &AccountsError{Path: path, Msg: fmt.Sprintf("the accounts file is owned by %s, not you or root; %s, so recreate it as yourself (cp it to a new file, then mv it over %s)", uid, accountsRunsWhy, path)}
	}
	dirs, links, err := accountsVisitedDirs(path)
	if err != nil {
		return &AccountsError{Path: path, Msg: "the directories above the accounts file cannot be judged: " + err.Error()}
	}
	for _, d := range dirs {
		di, err := os.Stat(d)
		if err != nil {
			return &AccountsError{Path: path, Msg: fmt.Sprintf("the directory %s above the accounts file cannot be judged: %v", d, err)}
		}
		if uid, ok := trustedOwner(d, di); !ok {
			return &AccountsError{Path: path, Msg: fmt.Sprintf("the directory %s above the accounts file is owned by %s, not you or root; %s, so keep the Hadron config directory in a tree you own", d, uid, accountsRunsWhy)}
		}
		if di.Mode().Perm()&0o022 != 0 && di.Mode()&os.ModeSticky == 0 {
			return &AccountsError{Path: path, Msg: fmt.Sprintf("the directory %s above the accounts file is writable by group or others and not sticky (mode %o); %s, so tighten it: chmod go-w %s", d, di.Mode().Perm(), accountsRunsWhy, d)}
		}
	}
	for _, l := range links {
		li, err := os.Lstat(l)
		if err != nil {
			return &AccountsError{Path: path, Msg: fmt.Sprintf("the symlink %s above the accounts file cannot be judged: %v", l, err)}
		}
		if uid, ok := trustedOwner(l, li); !ok {
			return &AccountsError{Path: path, Msg: fmt.Sprintf("the symlink %s on the path to the accounts file is owned by %s, not you or root; whoever owns it chose where it leads, so replace it with a link or directory you own", l, uid)}
		}
	}
	return nil
}

const accountsMaxSymlinks = 40

// accountsVisitedDirs lists, in order and without repeats, the directories visited resolving path component by
// component, and the symlinks followed on the way (the walk of hostpoller.visitedDirs). The final component is
// not included. ".." is applied after a symlink is followed, as the kernel does.
func accountsVisitedDirs(path string) (dirs, links []string, err error) {
	abs := path
	if !filepath.IsAbs(abs) {
		wd, err := os.Getwd()
		if err != nil {
			return nil, nil, err
		}
		abs = wd + "/" + abs
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
	seen := map[string]bool{}
	cur, parts, followed := "/", split(abs), 0
	for len(parts) > 0 {
		c := parts[0]
		parts = parts[1:]
		if c == ".." {
			cur = filepath.Dir(cur)
			continue
		}
		if !seen[cur] {
			seen[cur] = true
			dirs = append(dirs, cur)
		}
		next := filepath.Join(cur, c)
		fi, err := os.Lstat(next)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return dirs, links, nil // the rest is not there; LoadAccounts already found the file
			}
			return dirs, links, err
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			cur = next
			continue
		}
		if followed++; followed > accountsMaxSymlinks {
			return dirs, links, fmt.Errorf("it passes through more than %d symlinks", accountsMaxSymlinks)
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

// openAccountsNoFollow opens the file refusing a symlink at the last component, so a swap after the Lstat
// cannot redirect the read.
func openAccountsNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}
