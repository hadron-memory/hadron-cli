package delivery

import (
	"encoding/json"
	"errors"
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

// plainBoundWorker is the worker id a plain directory's binding names. The
// file is worker-writable, so it is read as a bounded regular file only.
func plainBoundWorker(dir string) string {
	b, ok := readSmallRegular(filepath.Join(dir, plainCacheName, bindingFileName))
	if !ok {
		return ""
	}
	var binding struct {
		WorkerID string `json:"workerId"`
	}
	if json.Unmarshal(b, &binding) != nil {
		return ""
	}
	return binding.WorkerID
}

// terminalRecord is what <dir>/.hadron-team/terminal.json holds that the
// deliverer uses: the terminal's handle and its wrapper's pid.
type terminalRecord struct {
	Handle     string `json:"handle"`
	WrapperPID int    `json:"wrapperPid"`
}

// readTerminalRecord reads <dir>/.hadron-team/terminal.json, and says whether
// the record is usable. Anything but a bounded regular file holding a JSON
// object with a non-empty handle is "no record".
func readTerminalRecord(dir string) (terminalRecord, bool) {
	b, ok := readSmallRegular(filepath.Join(dir, plainCacheName, terminalRecordName))
	if !ok {
		return terminalRecord{}, false
	}
	var rec terminalRecord
	if json.Unmarshal(b, &rec) != nil {
		return terminalRecord{}, false
	}
	rec.Handle = strings.TrimSpace(rec.Handle)
	return rec, rec.Handle != ""
}

// readTerminalHandle is the handle of readTerminalRecord.
func readTerminalHandle(dir string) (string, bool) {
	rec, ok := readTerminalRecord(dir)
	return rec.Handle, ok
}

// claimLive says a record's wrapper may still be running. Uncertainty counts
// as live (a claim that wrongly lapses could send two workers to one
// terminal): only proof the process does not exist (ESRCH) ends a claim. An
// EPERM, any other error, and a missing or non-positive pid (kill(0, ...)
// would signal a process group) all count as live. A reused pid also reads as
// live: the stale file keeps holding, which is safe and clears at the next
// launch.
func (d *Deliverer) claimLive(pid int) bool {
	if pid <= 0 {
		return true
	}
	gone := processGone
	if d.ProcessGone != nil {
		gone = d.ProcessGone
	}
	return !gone(pid)
}

// claims is what one recipient lookup knows of everyone's registrations,
// built from RESOLVED paths so two spellings of one directory cannot slip
// past (Xan's gate note on cli#1989).
type claims struct {
	handles   map[string]map[string]bool // handle -> workers whose record names it
	dirOwners map[string]map[string]bool // canonical dir -> workers it is registered to
}

// claims builds the handle and directory maps for one lookup, and says
// whether they could be built at all. Without a Registered source the other
// workers' claims are unknowable, so checks (b) and (c) cannot be judged and
// the caller holds: never a silent narrowing to the recipient alone.
func (d *Deliverer) claims(r Recipient) (claims, bool) {
	if d.Registered == nil {
		return claims{}, false
	}
	// A copy: the source's map is never written, and the recipient is always
	// among the registered even if the source omits it.
	reg := Registered{}
	for name, dirs := range d.Registered() {
		reg[name] = dirs
	}
	if _, ok := reg[r.Name]; !ok {
		reg[r.Name] = r.Worktrees
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
			if !isPlainDir(dir) {
				continue
			}
			rec, ok := readTerminalRecord(dir)
			// Another worker's record is a claim only while its wrapper
			// may be alive; the recipient's own is judged by (a)-(c).
			if ok && (name == r.Name || d.claimLive(rec.WrapperPID)) {
				add(c.handles, rec.Handle, name)
			}
		}
	}
	return c, true
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
	// The binding of the directory the terminal actually sits in: (c) only
	// knows the directories registered on THIS host. A checkout bound to a
	// different worker holds, as the worktree path always did.
	if t.WorktreePath != "" {
		if b := d.Bound(t.WorktreePath); b != "" && b != r.WorkerID {
			return none, HoldTerminalBoundElsewhere, handle, false
		}
	}
	return *t, "", handle, true
}
