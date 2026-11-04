package accountusage

// The Codex reader (cli#1659). It reads the newest session rollout under an
// account's Codex home and takes the latest token_count event's rate_limits.
// Rollout files hold conversation content, so the decode is into a typed
// struct that names only the fields used; nothing else is stored, printed or
// logged, a malformed or oversized line is skipped unread, and no error echoes
// a line or a path. No network, no subprocess.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	maxRolloutLine   = 1 << 20   // a longer line is skipped without being parsed
	maxRolloutBytes  = 256 << 20 // a larger newest file is refused (reason class only)
	maxDirEntries    = 512
	maxDayDirsProbed = 64
	maxWindowMinutes = 525600 // one year
)

// Test seams: both are no-ops in production.
var (
	afterRolloutStat  = func() {}                                // runs between the Lstat and the Open
	wrapRolloutReader = func(r io.Reader) io.Reader { return r } // wraps the opened file's reader
)

var (
	digitsRe     = regexp.MustCompile(`^[0-9]{1,4}$`)
	rolloutNameR = regexp.MustCompile(`^rollout-.+\.jsonl$`)
)

// CodexWindow is one Codex rate-limit window with its own length.
type CodexWindow struct {
	UsedPct  float64
	Minutes  int
	ResetsAt time.Time
}

// CodexSnapshot is the latest reading. Present is false when no usable
// token_count event exists: that is "no reading yet", never zero.
type CodexSnapshot struct {
	Present    bool
	ObservedAt time.Time
	Primary    *CodexWindow
	Secondary  *CodexWindow
}

type rawCodexWindow struct {
	UsedPercent   *float64 `json:"used_percent"`
	WindowMinutes *float64 `json:"window_minutes"`
	ResetsAt      *float64 `json:"resets_at"`
}

type rawRolloutLine struct {
	Timestamp string `json:"timestamp"`
	Payload   *struct {
		Type       string `json:"type"`
		RateLimits *struct {
			Primary   *rawCodexWindow `json:"primary"`
			Secondary *rawCodexWindow `json:"secondary"`
		} `json:"rate_limits"`
	} `json:"payload"`
}

func codexWindow(w *rawCodexWindow) *CodexWindow {
	if w == nil || w.UsedPercent == nil || w.WindowMinutes == nil || w.ResetsAt == nil {
		return nil
	}
	p, m := *w.UsedPercent, *w.WindowMinutes
	if math.IsNaN(p) || p < 0 || p > 100 || math.IsNaN(m) || m < 1 || m > maxWindowMinutes || m != math.Trunc(m) {
		return nil
	}
	t, ok := unixSeconds(*w.ResetsAt)
	if !ok {
		return nil
	}
	return &CodexWindow{UsedPct: p, Minutes: int(m), ResetsAt: t}
}

// readDirNames lists the names of directory entries that are not symlinks,
// bounded; a symlink is never followed.
func readDirNames(dir string, wantDir bool) []string {
	f, err := os.Open(dir)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	ents, err := f.ReadDir(maxDirEntries + 1)
	if err != nil && err != io.EOF {
		return nil
	}
	if len(ents) > maxDirEntries {
		return nil
	}
	var out []string
	for _, e := range ents {
		fi, err := os.Lstat(filepath.Join(dir, e.Name()))
		if err != nil || fi.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if wantDir && fi.IsDir() || !wantDir && fi.Mode().IsRegular() {
			out = append(out, e.Name())
		}
	}
	return out
}

func descending(names []string, ok func(string) bool) []string {
	var keep []string
	for _, n := range names {
		if ok(n) {
			keep = append(keep, n)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(keep)))
	return keep
}

// newestRollout is the lexically newest rollout-*.jsonl in the newest
// YYYY/MM/DD directory that has one. The Codex home must itself be a real
// directory (not a link); "" means none.
func newestRollout(home string) string {
	root := filepath.Join(home, "sessions")
	if fi, err := os.Lstat(root); err != nil || !fi.IsDir() {
		return ""
	}
	probed := 0
	isNum := func(n string) bool { return digitsRe.MatchString(n) }
	for _, y := range descending(readDirNames(root, true), isNum) {
		for _, m := range descending(readDirNames(filepath.Join(root, y), true), isNum) {
			for _, d := range descending(readDirNames(filepath.Join(root, y, m), true), isNum) {
				if probed++; probed > maxDayDirsProbed {
					return ""
				}
				dir := filepath.Join(root, y, m, d)
				files := descending(readDirNames(dir, false), rolloutNameR.MatchString)
				if len(files) > 0 {
					return filepath.Join(dir, files[0])
				}
			}
		}
	}
	return ""
}

// ReadCodex reads one account's newest rollout. No rollout, or one with no
// usable token_count event, is no data (Present false), not an error.
func ReadCodex(a Account) (CodexSnapshot, error) {
	if fi, err := os.Lstat(a.ConfigDir); err != nil || !fi.IsDir() {
		return CodexSnapshot{}, nil
	}
	path := newestRollout(a.ConfigDir)
	if path == "" {
		return CodexSnapshot{}, nil
	}
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return CodexSnapshot{}, nil
	}
	if fi.Size() > maxRolloutBytes {
		return CodexSnapshot{}, bad("account %q: the newest Codex session file is larger than %d bytes", a.Label, maxRolloutBytes)
	}
	afterRolloutStat()
	f, err := os.Open(path)
	if err != nil {
		return CodexSnapshot{}, bad("account %q: the newest Codex session file cannot be read", a.Label)
	}
	defer func() { _ = f.Close() }()
	var snap CodexSnapshot
	r := bufio.NewReaderSize(wrapRolloutReader(f), 64<<10)
	for {
		line, tooLong, rerr := readLine(r)
		if len(line) > 0 && !tooLong && bytes.Contains(line, []byte("token_count")) {
			var rl rawRolloutLine
			if json.Unmarshal(line, &rl) == nil && rl.Payload != nil && rl.Payload.Type == "token_count" && rl.Payload.RateLimits != nil {
				p, s := codexWindow(rl.Payload.RateLimits.Primary), codexWindow(rl.Payload.RateLimits.Secondary)
				if p != nil || s != nil {
					snap = CodexSnapshot{Present: true, Primary: p, Secondary: s}
					if t, perr := time.Parse(time.RFC3339Nano, strings.TrimSpace(rl.Timestamp)); perr == nil {
						snap.ObservedAt = t.UTC().Truncate(time.Second)
					} else {
						snap.ObservedAt = fi.ModTime().UTC().Truncate(time.Second)
					}
				}
			}
		}
		if rerr != nil {
			break
		}
	}
	return snap, nil
}

// readLine returns the next line; a line longer than maxRolloutLine is drained
// and reported tooLong with no content. err is io.EOF (or a read error) after
// the last line.
func readLine(r *bufio.Reader) (line []byte, tooLong bool, err error) {
	var buf []byte
	for {
		chunk, isPrefix, e := r.ReadLine()
		if e != nil {
			return buf, tooLong, e
		}
		if !tooLong {
			if len(buf)+len(chunk) > maxRolloutLine {
				tooLong, buf = true, nil
			} else {
				buf = append(buf, chunk...)
			}
		}
		if !isPrefix {
			if tooLong {
				return nil, true, nil
			}
			return buf, false, nil
		}
	}
}
