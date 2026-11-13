package delivery

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

// A plain-directory worker (no checkout; the session from #1968) is never
// reported by Orca as a terminal's worktreePath, so it cannot be found by
// path. Its terminal records its own handle instead (cli#1989): the host's
// worker-supervise writes <dir>/.hadron-team/terminal.json at every launch,
// and the deliverer only READS it and verifies it. That file sits in a
// directory the worker can write and selects the terminal that receives
// typed input, so it is never trusted alone: three checks (a)-(c) stand
// between it and a send, and a failure is a typed hold, never a fallback to
// a screen, title or identity match.

// terminalRecordName is the handle file inside the directory's cache.
const terminalRecordName = "terminal.json"

// plainCacheName is the plain directory's binding-cache home; the same name
// `hadron team session start` uses (internal/cmd/team/state.go).
const plainCacheName = ".hadron-team"

// maxTerminalRecord bounds the read: the file is worker-writable.
const maxTerminalRecord = 64 << 10

// Registered maps each worker registered on this host (Recipient.Name) to
// its registered directories, absolute. It is how the claim checks learn
// the OTHER workers' claims; a nil Registered knows only the recipient.
type Registered map[string][]string

// isPlainDir says a registered directory has no .git at all: the plain
// binding home applies, and only then (.git wins when present, never both:
// cor:agt:020:03, #1969 F9). The directory must exist: a registered path
// that is missing is not a plain directory, and any other stat failure is
// not "plain" either.
func isPlainDir(dir string) bool {
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return false
	}
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return errors.Is(err, fs.ErrNotExist)
}

// plainBoundWorker is the worker id a plain directory's binding names.
func plainBoundWorker(dir string) string {
	return readBinding(filepath.Join(dir, plainCacheName, bindingFileName))
}

// readTerminalHandle is the handle <dir>/.hadron-team/terminal.json names,
// and whether the record is usable. Anything but a regular file holding a
// JSON object with a non-empty handle is "no record".
func readTerminalHandle(dir string) (string, bool) {
	path := filepath.Join(dir, plainCacheName, terminalRecordName)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, maxTerminalRecord+1))
	if err != nil || len(b) > maxTerminalRecord {
		return "", false
	}
	var rec struct {
		Handle string `json:"handle"`
	}
	if json.Unmarshal(b, &rec) != nil {
		return "", false
	}
	h := strings.TrimSpace(rec.Handle)
	return h, h != ""
}

// claims is what one recipient lookup knows of everyone's registrations,
// built from RESOLVED paths so two spellings of one directory cannot slip
// past (Xan's gate note on cli#1989).
type claims struct {
	handles   map[string]map[string]bool // handle -> workers whose record names it
	dirOwners map[string]map[string]bool // canonical dir -> workers it is registered to
}

func (d *Deliverer) claims(r Recipient) claims {
	reg := Registered{}
	if d.Registered != nil {
		reg = d.Registered()
	}
	if _, ok := reg[r.Name]; !ok {
		reg = Registered{r.Name: r.Worktrees}
	}
	c := claims{handles: map[string]map[string]bool{}, dirOwners: map[string]map[string]bool{}}
	add := func(m map[string]map[string]bool, k, name string) {
		if m[k] == nil {
			m[k] = map[string]bool{}
		}
		m[k][name] = true
	}
	for name, dirs := range reg {
		for _, dir := range dirs {
			add(c.dirOwners, canonical(dir), name)
			if isPlainDir(dir) {
				if h, ok := readTerminalHandle(dir); ok {
					add(c.handles, h, name)
				}
			}
		}
	}
	return c
}

// othersThan reports whether any worker besides name is in set.
func othersThan(set map[string]bool, name string) bool {
	for n := range set {
		if n != name {
			return true
		}
	}
	return false
}

// plainTerminal resolves one plain directory to its terminal, or the typed
// hold that refuses it. The checks run before any screen is read.
func (d *Deliverer) plainTerminal(terms []orcaterm.Terminal, dir string, r Recipient, c claims) (orcaterm.Terminal, string, string, bool) {
	var none orcaterm.Terminal
	handle, ok := readTerminalHandle(dir)
	if !ok {
		return none, HoldNoTerminalRecord, "", false
	}
	var t *orcaterm.Terminal
	for i := range terms {
		if terms[i].Handle == handle {
			t = &terms[i]
			break
		}
	}
	// (a) connected and writable, with an agent identity.
	if t == nil || !t.Connected || !t.Writable || t.Identity() == "" {
		return none, HoldTerminalStale, handle, false
	}
	// (b) no other registered worker's record names the same handle.
	if othersThan(c.handles[handle], r.Name) {
		return none, HoldTerminalClaimed, handle, false
	}
	// (c) the terminal's own worktree is not a directory registered to a
	// different worker.
	if t.WorktreePath != "" && othersThan(c.dirOwners[canonical(t.WorktreePath)], r.Name) {
		return none, HoldTerminalBoundElsewhere, handle, false
	}
	return *t, "", handle, true
}
