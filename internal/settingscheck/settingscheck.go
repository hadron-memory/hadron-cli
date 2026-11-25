// Package settingscheck answers, without writing or running anything, whether
// Claude Code's idleCompaction setting is off for a worker's worktree
// (hadron-cli#2545; ruled off everywhere in hadron-cli#2544).
//
// Nothing of a settings file but the one key's value leaves this package, and
// every doubt is drift: a file that cannot be read or parsed is never "off".
package settingscheck

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	Key = "idleCompaction"
	// Default is what Claude Code does when no file sets the key.
	Default = true
	// MinVersion is the first Claude Code version that reads the key.
	MinVersion = "2.1.290"
	// ManagedDir is where Claude Code reads managed settings on Linux.
	ManagedDir = "/etc/claude-code"

	maxFile = 8 << 20
)

// The state of the key in one file.
const (
	StateOff        = "OFF"
	StateOn         = "ON"
	StateUnset      = "UNSET"
	StateInvalid    = "INVALID"    // not a JSON object, or the key is not a Boolean
	StateUnreadable = "UNREADABLE" // exists, but could not be read
)

// ReadFile is the key's state in one settings file. A missing file is UNSET.
func ReadFile(path string) string {
	st, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return StateUnset
	}
	if err != nil || !st.Mode().IsRegular() || st.Size() > maxFile {
		return StateUnreadable
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return StateUnreadable
	}
	// A map, not a struct: encoding/json matches struct fields without regard
	// to case, and Claude Code does not.
	var doc map[string]json.RawMessage
	// Claude Code 2.1.328 and 2.1.332 load a file with a byte order mark.
	if json.Unmarshal(bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf")), &doc) != nil || doc == nil {
		return StateInvalid
	}
	v, ok := doc[Key]
	if !ok {
		return StateUnset
	}
	switch string(bytes.TrimSpace(v)) {
	case "true":
		return StateOn
	case "false":
		return StateOff
	}
	return StateInvalid
}

// Result is the effective state and the file it came from ("" when no file
// sets the key).
type Result struct {
	State, Source string
	Managed       bool
}

// Off reports a value known to be false.
func (r Result) Off() bool { return r.State == StateOff }

// readManaged is the managed level: managed-settings.json, then every
// managed-settings.d/*.json in name order, a later file replacing the value.
// One file that cannot be used makes the level unknown.
func readManaged(dir string) Result {
	res := Result{State: StateUnset, Managed: true}
	paths := []string{filepath.Join(dir, "managed-settings.json")}
	dropDir := filepath.Join(dir, "managed-settings.d")
	entries, err := os.ReadDir(dropDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Result{State: StateUnreadable, Source: dropDir, Managed: true}
	}
	for _, e := range entries { // os.ReadDir sorts by name
		if n := e.Name(); strings.HasSuffix(n, ".json") && !strings.HasPrefix(n, ".") {
			paths = append(paths, filepath.Join(dropDir, n))
		}
	}
	for _, p := range paths {
		switch s := ReadFile(p); s {
		case StateUnset:
		case StateInvalid, StateUnreadable:
			return Result{State: s, Source: p, Managed: true}
		default:
			res.State, res.Source = s, p
		}
	}
	return res
}

// Effective is the key's value for a session started in worktree under one
// account directory. The order, highest first, was measured on Claude Code
// 2.1.328 and 2.1.332 (docs/plans/2545-settings-check.md): managed, the
// repository root's settings.local.json (for a linked worktree the MAIN
// checkout's), the worktree's own settings.local.json, its settings.json,
// the account's settings.json.
func Effective(managedDir, worktree, account, home string) Result {
	if m := readManaged(managedDir); m.State != StateUnset {
		return m
	}
	own := filepath.Join(worktree, ".claude")
	paths := []string{}
	root, err := localRoot(worktree, home)
	if err != nil {
		return Result{State: StateUnreadable, Source: filepath.Join(worktree, ".git")}
	}
	if root != "" {
		paths = append(paths, filepath.Join(root, ".claude", "settings.local.json"))
	}
	paths = append(paths, filepath.Join(own, "settings.local.json"), filepath.Join(own, "settings.json"), filepath.Join(account, "settings.json"))
	for _, p := range paths {
		if s := ReadFile(p); s != StateUnset {
			return Result{State: s, Source: p}
		}
	}
	return Result{State: StateUnset}
}

// ownedByUser is a variable so a test can be another user.
var ownedByUser = pathOwnedByUser

// localRoot is the directory, other than worktree itself, whose
// .claude/settings.local.json Claude Code reads for a session in worktree; ""
// when there is none. Claude Code skips it when it is the home directory or
// when it, its .git or its .claude belongs to another user.
func localRoot(worktree, home string) (string, error) {
	dir, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		return "", err
	}
	start := dir
	for {
		st, err := os.Stat(filepath.Join(dir, ".git"))
		if err == nil {
			if !st.IsDir() {
				if dir, err = mainCheckout(dir); err != nil {
					return "", err
				}
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil // not in a repository
		}
		dir = parent
	}
	if h, err := filepath.EvalSymlinks(home); err == nil {
		home = h
	}
	if dir == "" || dir == start || dir == home {
		return "", nil
	}
	for _, p := range []string{dir, filepath.Join(dir, ".git"), filepath.Join(dir, ".claude")} {
		if !ownedByUser(p) {
			return "", nil
		}
	}
	return dir, nil
}

// mainCheckout resolves a .git FILE: the main checkout for a linked worktree,
// the directory itself for a submodule, "" for a worktree of a bare repository.
func mainCheckout(dir string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, ".git"))
	if err != nil {
		return "", err
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir:")
	if !ok {
		return "", errors.New("not a gitdir file")
	}
	gitdir = absFrom(dir, strings.TrimSpace(gitdir))
	raw, err = os.ReadFile(filepath.Join(gitdir, "commondir"))
	if errors.Is(err, os.ErrNotExist) {
		return dir, nil
	}
	if err != nil {
		return "", err
	}
	common, err := filepath.EvalSymlinks(absFrom(gitdir, strings.TrimSpace(string(raw))))
	if err != nil {
		return "", err
	}
	if filepath.Base(common) != ".git" {
		return "", nil
	}
	return filepath.Dir(common), nil
}

func absFrom(base, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(base, p)
}

// AccountDirs are the Claude Code account directories under home: every
// directory named .claude or .claude*.
func AccountDirs(home string) ([]string, error) {
	entries, err := os.ReadDir(home)
	if err != nil {
		return nil, err
	}
	dirs := []string{}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".claude") {
			continue
		}
		p := filepath.Join(home, e.Name())
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			dirs = append(dirs, p)
		}
	}
	sort.Strings(dirs)
	return dirs, nil
}

var versionRE = regexp.MustCompile(`^\d{1,6}\.\d{1,6}\.\d{1,6}$`)

// InstalledVersion reads the version from the native installer's layout (a
// symlink to …/versions/<version>); "" when it is not that. It never runs the
// binary: starting Claude Code can write to the account directory.
func InstalledVersion(bin string) string {
	p, err := filepath.EvalSymlinks(bin)
	if err != nil {
		return ""
	}
	if v := filepath.Base(p); versionRE.MatchString(v) {
		return v
	}
	return ""
}

// AtLeast compares two versions that match versionRE.
func AtLeast(v, floor string) bool {
	a, b := strings.Split(v, "."), strings.Split(floor, ".")
	for i := range b {
		x, _ := strconv.Atoi(a[i])
		y, _ := strconv.Atoi(b[i])
		if x != y {
			return x > y
		}
	}
	return true
}
