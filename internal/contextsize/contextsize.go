// Package contextsize measures how large an agent's conversation context is
// from the usage numbers its launcher writes locally (hadron-cli#2063, the
// client half of hadron-server#4238 S4a).
//
// It is deliberately narrow, like internal/modelreport:
//   - Only numbers and a timestamp are decoded. Every record is unmarshalled
//     into a struct that has no field for message content, so transcript text
//     is never held, returned, logged or sent.
//   - Any doubt returns nil: a missing, unreadable, oversized, unattributable
//     or implausible record yields NO measurement, never a zero.
//   - A file is attributed to a worker only when the file itself records the
//     worker's worktree as its cwd; a directory-name match alone is not proof.
package contextsize

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	maxHeader     = 64 << 10
	maxTail       = 4 << 20
	maxRecord     = 1 << 20
	maxCandidates = 300
	// maxAhead mirrors the server's bound on contextMeasuredAt (hadron-server#4238).
	maxAhead = 2 * time.Minute
	// maxTokens is the server's upper bound on contextTokens (Int32).
	maxTokens = 2147483647
)

// Measurement is one context-size reading: both fields are always set together.
type Measurement struct {
	Tokens     int
	MeasuredAt time.Time
}

// Roots are the directories the supported launchers write sessions under.
type Roots struct {
	ClaudeProjects string // e.g. ~/.claude/projects
	CodexSessions  string // e.g. ~/.codex/sessions
}

// DefaultRoots honours CLAUDE_CONFIG_DIR and CODEX_HOME like the tools do.
func DefaultRoots() Roots {
	home, _ := os.UserHomeDir()
	claude, codex := os.Getenv("CLAUDE_CONFIG_DIR"), os.Getenv("CODEX_HOME")
	if claude == "" && home != "" {
		claude = filepath.Join(home, ".claude")
	}
	if codex == "" && home != "" {
		codex = filepath.Join(home, ".codex")
	}
	r := Roots{}
	if claude != "" {
		r.ClaudeProjects = filepath.Join(claude, "projects")
	}
	if codex != "" {
		r.CodexSessions = filepath.Join(codex, "sessions")
	}
	return r
}

// Measurer owns the discovery work shared by one health sweep.
// The zero value is ready to use; do not retain it across sweeps.
type Measurer struct {
	codexCandidates map[string][]string
}

// Filesystem seams let tests replace a rollout at the open boundary and count
// archive traversal without timing-based races or performance assertions.
var openContextFile = openRegular
var walkCodexSessions = filepath.WalkDir

// Measure returns the newest context-size reading for a worker running the
// given model family in any of the worktrees, or nil. Only the families
// "claude" and "codex" have a measurer; every other family returns nil.
// `now` bounds a reading that claims to be from the future.
func Measure(family string, worktrees []string, roots Roots, now time.Time) *Measurement {
	return (&Measurer{}).Measure(family, worktrees, roots, now)
}

// Measure reuses candidate discovery for each Codex root within this sweep.
// Calls on one Measurer must be sequential; measurements themselves are not cached.
func (sweep *Measurer) Measure(family string, worktrees []string, roots Roots, now time.Time) *Measurement {
	var best *Measurement
	for _, wt := range worktrees {
		var m *Measurement
		switch strings.ToLower(strings.TrimSpace(family)) {
		case "claude":
			m = measureClaude(roots.ClaudeProjects, wt, now)
		case "codex":
			m = sweep.measureCodex(roots.CodexSessions, wt, now)
		default:
			return nil
		}
		if m != nil && (best == nil || m.MeasuredAt.After(best.MeasuredAt)) {
			best = m
		}
	}
	return best
}

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

// claudeProjectDir is how Claude Code names a cwd's transcript directory.
func claudeProjectDir(root, cwd string) string {
	return filepath.Join(root, nonAlnum.ReplaceAllString(cwd, "-"))
}

func measureClaude(root, worktree string, now time.Time) *Measurement {
	if root == "" || worktree == "" {
		return nil
	}
	dir := claudeProjectDir(root, worktree)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	// Only the newest transcript is the current session; an older file's
	// reading would be stale, so no fallback to it.
	newest := newestFirst(files)
	if len(newest) == 0 {
		return nil
	}
	m, _ := ClaudeFromFile(newest[0], worktree, now)
	return m
}

func (m *Measurer) measureCodex(root, worktree string, now time.Time) *Measurement {
	if root == "" || worktree == "" {
		return nil
	}
	root = filepath.Clean(root)
	if m.codexCandidates == nil {
		m.codexCandidates = map[string][]string{}
	}
	files, found := m.codexCandidates[root]
	if !found {
		files = discoverCodex(root)
		m.codexCandidates[root] = files
	}
	for _, path := range files {
		reading, attributed := codexForWorktree(path, worktree, now)
		if attributed {
			// Never fall back from this worktree's newest rollout.
			return reading
		}
	}
	return nil
}

func discoverCodex(root string) []string {
	var files []string
	_ = walkCodexSessions(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() && strings.HasPrefix(d.Name(), "rollout-") && strings.HasSuffix(d.Name(), ".jsonl") {
			files = append(files, p)
		}
		return nil
	})
	ordered := newestFirst(files)
	if len(ordered) > maxCandidates {
		ordered = ordered[:maxCandidates]
	}
	// Retain only the bounded candidates, not the entire archive's backing array.
	return append([]string(nil), ordered...)
}

// Attribution and usage must come from the same descriptor. Replacing the
// pathname after opening cannot substitute another worktree's measurement.
func codexForWorktree(path, worktree string, now time.Time) (*Measurement, bool) {
	f, size, err := openContextFile(path)
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }()
	if !codexCwdIs(f, size, worktree) {
		return nil, false
	}
	reading, _ := codexUsage(f, size, now)
	return reading, true
}

func newestFirst(paths []string) []string {
	type pm struct {
		p string
		t time.Time
	}
	var l []pm
	for _, p := range paths {
		if fi, err := os.Lstat(p); err == nil && fi.Mode().IsRegular() {
			l = append(l, pm{p, fi.ModTime()})
		}
	}
	sort.Slice(l, func(i, j int) bool { return l[i].t.After(l[j].t) })
	out := make([]string, len(l))
	for i := range l {
		out[i] = l[i].p
	}
	return out
}

func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ra, ea := filepath.EvalSymlinks(a)
	rb, eb := filepath.EvalSymlinks(b)
	if ea != nil || eb != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}

// openRegular opens a regular file without following a final symlink and
// proves the opened fd is the file that was checked.
func openRegular(path string) (*os.File, int64, error) {
	pre, err := os.Lstat(path)
	if err != nil || !pre.Mode().IsRegular() {
		return nil, 0, errors.New("not a regular file")
	}
	f, err := os.Open(path) // #nosec G304 — path comes from a directory listing of a known root
	if err != nil {
		return nil, 0, err
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || !os.SameFile(pre, fi) {
		_ = f.Close()
		return nil, 0, errors.New("identity changed")
	}
	return f, fi.Size(), nil
}

// tailLines returns the complete records of the bounded tail, newest last.
// A partial final record (writer mid-append) and a window-cut first record
// are dropped; a final record over budget is refused.
func tailLines(f *os.File, size int64) ([][]byte, bool) {
	start := size - maxTail
	if start < 0 {
		start = 0
	}
	buf := make([]byte, size-start)
	if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
		return nil, false
	}
	i := bytes.LastIndexByte(buf, '\n')
	if i < 0 {
		return nil, false
	}
	if len(buf)-i-1 > maxRecord {
		return nil, false
	}
	ls := bytes.Split(buf[:i], []byte("\n"))
	if start > 0 {
		ls = ls[1:]
	}
	return ls, true
}

func plausible(tokens int, at time.Time, now time.Time) bool {
	return tokens >= 1 && tokens <= maxTokens && !at.IsZero() && !at.After(now.Add(maxAhead-time.Second))
}

// ClaudeFromFile reads the latest main-thread assistant usage record of a
// Claude Code transcript whose own cwd is worktree. Context is input +
// cache-read + cache-creation input tokens of that turn.
func ClaudeFromFile(path, worktree string, now time.Time) (*Measurement, bool) {
	f, size, err := openContextFile(path)
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }()
	ls, ok := tailLines(f, size)
	if !ok {
		return nil, false
	}
	for i := len(ls) - 1; i >= 0; i-- {
		if len(ls[i]) > maxRecord {
			return nil, false
		}
		var rec struct {
			Type        string `json:"type"`
			IsSidechain bool   `json:"isSidechain"`
			Cwd         string `json:"cwd"`
			Timestamp   string `json:"timestamp"`
			Message     struct {
				Model string `json:"model"`
				Usage *struct {
					In            int `json:"input_tokens"`
					CacheRead     int `json:"cache_read_input_tokens"`
					CacheCreation int `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(ls[i], &rec) != nil || rec.Type != "assistant" || rec.IsSidechain || rec.Message.Usage == nil || rec.Message.Model == "<synthetic>" {
			continue
		}
		u := rec.Message.Usage
		tokens := u.In + u.CacheRead + u.CacheCreation
		at, err := time.Parse(time.RFC3339Nano, rec.Timestamp)
		if err != nil || u.In < 0 || u.CacheRead < 0 || u.CacheCreation < 0 || tokens < 1 {
			continue // an unusable record: look at the previous turn, never send 0
		}
		if !sameDir(rec.Cwd, worktree) || !plausible(tokens, at, now) {
			return nil, false
		}
		return &Measurement{Tokens: tokens, MeasuredAt: at.UTC()}, true
	}
	return nil, false
}

func codexCwdIs(f *os.File, size int64, worktree string) bool {
	n := int64(maxHeader)
	if size < n {
		n = size
	}
	buf := make([]byte, n)
	if _, err := f.ReadAt(buf, 0); err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	ls := bytes.Split(buf, []byte("\n"))
	if n == int64(maxHeader) && size > n {
		ls = ls[:len(ls)-1]
	}
	for _, l := range ls {
		var rec struct {
			Type    string `json:"type"`
			Payload struct {
				Cwd string `json:"cwd"`
			} `json:"payload"`
		}
		if json.Unmarshal(l, &rec) == nil && rec.Type == "session_meta" {
			return sameDir(rec.Payload.Cwd, worktree)
		}
	}
	return false
}

// CodexFromFile reads the latest token_count event of a Codex rollout. The
// context is the last turn's input tokens (Codex counts cached input inside
// input_tokens), not the running total, which only grows.
func CodexFromFile(path string, now time.Time) (*Measurement, bool) {
	f, size, err := openContextFile(path)
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }()
	return codexUsage(f, size, now)
}

func codexUsage(f *os.File, size int64, now time.Time) (*Measurement, bool) {
	ls, ok := tailLines(f, size)
	if !ok {
		return nil, false
	}
	for i := len(ls) - 1; i >= 0; i-- {
		if len(ls[i]) > maxRecord {
			return nil, false
		}
		var rec struct {
			Timestamp string `json:"timestamp"`
			Payload   struct {
				Type string `json:"type"`
				Info *struct {
					Last struct {
						In int `json:"input_tokens"`
					} `json:"last_token_usage"`
				} `json:"info"`
			} `json:"payload"`
		}
		if json.Unmarshal(ls[i], &rec) != nil || rec.Payload.Type != "token_count" || rec.Payload.Info == nil {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, rec.Timestamp)
		tokens := rec.Payload.Info.Last.In
		if err != nil || tokens < 1 {
			continue
		}
		if !plausible(tokens, at, now) {
			return nil, false
		}
		return &Measurement{Tokens: tokens, MeasuredAt: at.UTC()}, true
	}
	return nil, false
}
