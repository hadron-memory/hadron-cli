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
// bounded; a symlink is never followed. A directory that does not exist is no
// names; any other failure is an error (never read as "nothing here").
func readDirNames(dir string, wantDir bool) ([]string, error) {
	f, err := os.Open(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, errCodexDir
	}
	defer func() { _ = f.Close() }()
	ents, err := f.ReadDir(maxDirEntries + 1)
	if err != nil && err != io.EOF {
		return nil, errCodexDir
	}
	if len(ents) > maxDirEntries {
		return nil, nil
	}
	var out []string
	for _, e := range ents {
		fi, err := os.Lstat(filepath.Join(dir, e.Name()))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, errCodexDir
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if wantDir && fi.IsDir() || !wantDir && fi.Mode().IsRegular() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

var errCodexDir = bad("the Codex session directory cannot be read")

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
// YYYY/MM/DD directory that has one. The Codex home and everything under it
// are reached without following a link; "" means none, an error means a
// directory could not be read.
func newestRollout(home string) (string, error) {
	root := filepath.Join(home, "sessions")
	if fi, err := os.Lstat(root); err != nil || !fi.IsDir() {
		if err != nil && !os.IsNotExist(err) {
			return "", errCodexDir
		}
		return "", nil
	}
	probed := 0
	isNum := func(n string) bool { return digitsRe.MatchString(n) }
	list := func(dir string, wantDir bool, ok func(string) bool) ([]string, error) {
		names, err := readDirNames(dir, wantDir)
		return descending(names, ok), err
	}
	years, err := list(root, true, isNum)
	if err != nil {
		return "", err
	}
	for _, y := range years {
		months, err := list(filepath.Join(root, y), true, isNum)
		if err != nil {
			return "", err
		}
		for _, m := range months {
			days, err := list(filepath.Join(root, y, m), true, isNum)
			if err != nil {
				return "", err
			}
			for _, d := range days {
				if probed++; probed > maxDayDirsProbed {
					return "", nil
				}
				dir := filepath.Join(root, y, m, d)
				files, err := list(dir, false, rolloutNameR.MatchString)
				if err != nil {
					return "", err
				}
				if len(files) > 0 {
					return filepath.Join(dir, files[0]), nil
				}
			}
		}
	}
	return "", nil
}

// ReadCodex reads one account's newest rollout. No rollout, or one with no
// usable token_count event, is no data (Present false), not an error.
func ReadCodex(a Account) (CodexSnapshot, error) {
	if fi, err := os.Lstat(a.ConfigDir); err != nil || !fi.IsDir() {
		if err != nil && !os.IsNotExist(err) {
			return CodexSnapshot{}, bad("account %q: the Codex home cannot be read", a.Label)
		}
		return CodexSnapshot{}, nil
	}
	path, derr := newestRollout(a.ConfigDir)
	if derr != nil {
		return CodexSnapshot{}, bad("account %q: the Codex session directory cannot be read", a.Label)
	}
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
	// The file opened must be the regular file that was checked: a swap for a
	// link or another file between the Lstat and the Open is refused.
	if ofi, serr := f.Stat(); serr != nil || !ofi.Mode().IsRegular() || !os.SameFile(fi, ofi) {
		return CodexSnapshot{}, nil
	}
	var snap CodexSnapshot
	// The size check above is advisory; this limit is what bounds the read of
	// a file that is still being appended to.
	lr := &io.LimitedReader{R: f, N: maxRolloutBytes + 1}
	r := bufio.NewReaderSize(wrapRolloutReader(lr), 64<<10)
	var rerr error
	for {
		var line []byte
		var tooLong bool
		line, tooLong, rerr = readLine(r)
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
	if lr.N <= 0 {
		return CodexSnapshot{}, bad("account %q: the newest Codex session file is larger than %d bytes", a.Label, maxRolloutBytes)
	}
	if rerr != io.EOF {
		return CodexSnapshot{}, bad("account %q: the newest Codex session file cannot be read", a.Label)
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
