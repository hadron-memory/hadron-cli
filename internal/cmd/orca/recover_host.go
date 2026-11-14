package orca

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// scanProc reads every process's working directory and argv from /proc. A
// process it cannot read (another user's, or gone mid-scan) is skipped: the
// agents recover cares about run as this user. Where /proc does not exist the
// scan fails, and recover refuses rather than guess.
func scanProc() ([]Proc, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var out []Proc
	found := false
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		found = true
		dir := filepath.Join("/proc", e.Name())
		cwd, err := os.Readlink(filepath.Join(dir, "cwd"))
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, "cmdline")) // #nosec G304 — /proc
		if err != nil || len(raw) == 0 {
			continue
		}
		var argv []string
		for _, a := range bytes.Split(bytes.TrimRight(raw, "\x00"), []byte{0}) {
			argv = append(argv, string(a))
		}
		out = append(out, Proc{PID: pid, Cwd: cwd, Argv: argv})
	}
	if !found {
		return nil, fmt.Errorf("/proc lists no processes")
	}
	return out, nil
}

// selfAncestors is this process's pid and its parents', from /proc.
func selfAncestors() []int {
	var out []int
	for pid := os.Getpid(); pid > 1 && len(out) < 64; {
		out = append(out, pid)
		raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)) // #nosec G304 — /proc
		if err != nil {
			break
		}
		s := string(raw)
		i := strings.LastIndex(s, ")")
		if i < 0 {
			break
		}
		f := strings.Fields(s[i+1:])
		if len(f) < 2 {
			break
		}
		pid, _ = strconv.Atoi(f[1])
	}
	return out
}

var uuidName = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var nonProjectChar = regexp.MustCompile(`[^A-Za-z0-9-]`)
var configDirLine = regexp.MustCompile(`(?m)^\s*(?:export\s+)?CLAUDE_CONFIG_DIR=["']?([^"'\s]+)`)

// claudeTranscripts lists the worktree's transcripts: the *.jsonl files in
// <config dir>/projects/<worktree path with every non-alphanumeric as "-">.
func claudeTranscripts(worktree string, launch []string) ([]Transcript, error) {
	dir := filepath.Join(claudeConfigDir(launch), "projects", nonProjectChar.ReplaceAllString(worktree, "-"))
	return readTranscriptDir(dir)
}

func readTranscriptDir(dir string) ([]Transcript, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Transcript
	for _, e := range entries {
		id := strings.TrimSuffix(e.Name(), ".jsonl")
		if e.IsDir() || id == e.Name() || !uuidName.MatchString(id) {
			continue
		}
		info, err := e.Info()
		if err != nil || info.Size() == 0 {
			continue
		}
		out = append(out, Transcript{ID: id, ModTime: info.ModTime()})
	}
	return out, nil
}

// claudeConfigDir is the account's Claude config dir, taken from the launch
// line: a word of it that resolves to a launcher script exporting
// CLAUDE_CONFIG_DIR names it. Otherwise the environment's, else ~/.claude.
func claudeConfigDir(launch []string) string {
	home, _ := os.UserHomeDir()
	for _, word := range launch {
		path := word
		if !strings.Contains(word, "/") {
			path = lookPath(word)
		}
		if path == "" {
			continue
		}
		raw, err := os.ReadFile(path) // #nosec G304 — a word of the host's own launch line
		if err != nil || len(raw) > 1<<16 {
			continue
		}
		if m := configDirLine.FindSubmatch(raw); m != nil {
			return os.Expand(string(m[1]), func(k string) string {
				if k == "HOME" {
					return home
				}
				return os.Getenv(k)
			})
		}
	}
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	return filepath.Join(home, ".claude")
}

func lookPath(name string) string {
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		p := filepath.Join(d, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// stagedPrompt: the recovery prompt the shed procedure stages for the
// worktree, which worker-supervise hands the relaunched agent.
func stagedPrompt(worktree string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	f, err := os.Open(filepath.Join(home, ".local", "state", "hadron-worker-shed", filepath.Base(worktree)+".prompt")) // #nosec G304
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReader(f)
	_, err = r.Peek(1)
	return err == nil
}
