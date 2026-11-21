package hostpoller

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// cli#2623: a running poller picks up a change to its configuration file.
//
// The poller used to read the file once, at start, so a worker added to the
// file while the poller ran was never registered and never got a wake until
// someone restarted it. Each poll now compares the file's content to the
// content the poller holds and, when it changed, loads and checks the new file
// as start does. Only what workers depend on is applied live: the workers
// map, the repos worktree.create clones from and the account profiles a
// launcher profile names. Anything else (the key, the poll interval, delivery,
// the capability list, the state file) is read at start by code that has
// already wired itself from it, so a change to it is logged as needing a
// restart and is not applied. A file that does not load or does not pass the
// start-time checks changes nothing: the poller keeps what it has and says so
// once for that content.
//
// Hot reload turns "whoever can write this file" into "whoever can make the
// poller run a command on the next poll", so it is held to more than a restart
// is: the file, and every program or directory a changed entry names, must
// belong to the poller's own user (or root) and not be writable by group or
// others, or the reload is refused (checkReloadTrust). A new content must also
// be seen on two consecutive polls before it is applied, so an editor that
// truncates and then writes never has its half-written file applied.
//
// A Config is a snapshot and never changes once built. Applying a reload
// builds a new one and swaps the poller's pointer (Poller.config), so a pass
// that took the configuration once, as register and the recipient lookup do,
// sees one consistent set of workers however a reload lands around it.

// reloadable names the Config fields a running poller swaps. Every other field
// is "read at start": a new field is therefore restart-only until someone
// classifies it here on purpose.
var reloadable = []string{"Workers", "Repos", "Accounts"}

func isReloadable(field string) bool {
	for _, f := range reloadable {
		if f == field {
			return true
		}
	}
	return false
}

// reloadConfig is called at the top of every Step. It never fails a poll.
func (p *Poller) reloadConfig() {
	c := p.config()
	path, held := c.sourcePath, c.sourceHash
	if path == "" {
		return
	}
	// One open file: the bytes parsed and the owner and mode judged below are
	// the same file's, so the file cannot be swapped between the read and the
	// check (a symlink is followed once, at the open).
	raw, info, err := readConfigFile(path)
	if err != nil {
		p.reloadRefused(sha256.Sum256([]byte(err.Error())), "the configuration file cannot be read: "+err.Error())
		return
	}
	sum := sha256.Sum256(raw)
	if sum == held {
		p.reloadSeen, p.reloadPending = [sha256.Size]byte{}, [sha256.Size]byte{}
		return
	}
	if sum != p.reloadPending {
		// First sight of this content: wait one poll, so a file an editor is
		// still writing is never applied half-written.
		p.reloadPending = sum
		return
	}
	next, err := loadConfigBytes(path, raw)
	if err != nil {
		p.reloadRefused(sum, err.Error())
		return
	}
	if err := next.CheckWorktreeAliases(p.state); err != nil {
		p.reloadRefused(sum, err.Error())
		return
	}
	if next.UsesOrcaLaunch() && !p.orcaWired {
		p.reloadRefused(sum, "a worker now launches through Orca, which this poller did not wire at start; restart it to apply any of this change")
		return
	}
	added, removed, modified := diffWorkers(c, next)
	others := reloadableChanges(c, next, "Workers")
	if err := checkReloadTrust(p.trust, c, next, path, info, append(append([]string{}, added...), modified...)); err != nil {
		p.reloadRefused(sum, err.Error())
		return
	}
	changed := fixedChanges(c, next)
	merged := *c
	merged.sourceHash = sum
	apply := len(added)+len(removed)+len(modified)+len(others) > 0
	if apply {
		merged.Workers, merged.Repos, merged.Accounts = next.Workers, next.Repos, next.Accounts
		p.noteRemoved(c, removed, added)
	}
	p.live.Store(&merged)
	p.reloadSeen, p.reloadPending = [sha256.Size]byte{}, [sha256.Size]byte{}
	if apply {
		p.log("config-reloaded", "", "", "", reloadDetail(added, removed, modified, others))
	}
	if len(changed) > 0 {
		p.log("config-restart-needed", "", "", "", "changed in the file but read only at start, so not applied: "+strings.Join(changed, ", ")+"; restart the poller to apply it")
	}
}

// readConfigFile reads path through one open file and returns its content and
// that file's own metadata.
func readConfigFile(path string) ([]byte, os.FileInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = f.Close() }() // read only: nothing to flush
	info, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if len(raw) > maxConfigBytes {
		return nil, nil, fmt.Errorf("the file is larger than %d bytes", maxConfigBytes)
	}
	return raw, info, nil
}

// maxConfigBytes bounds what a reload reads: a host config is a few kilobytes.
const maxConfigBytes = 1 << 20

// noteRemoved remembers, by worker id, the entry each removed worker had, so a
// worker.end for it can still be carried out with that entry, and forgets a
// worker that came back.
func (p *Poller) noteRemoved(before *Config, removed, added []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.removed == nil {
		p.removed = map[string]removedWorker{}
	}
	for id, rm := range p.removed {
		for _, ref := range added {
			if rm.ref == ref {
				delete(p.removed, id)
			}
		}
	}
	for _, ref := range removed {
		w, ok := before.Workers[ref]
		if !ok {
			continue
		}
		for id, r := range p.workerRefs {
			if r == ref {
				p.removed[id] = removedWorker{ref: ref, cfg: w}
			}
		}
	}
}

// removedWorkerFor is the last entry of a worker a reload removed, found by
// the operation's worker id.
func (p *Poller) removedWorkerFor(op Operation) (removedWorker, bool) {
	id := op.WorkerID
	if id == "" {
		id = argString(op, "workerId")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	rm, ok := p.removed[id]
	return rm, ok
}

// reloadRefused keeps the running configuration and says why, once for each
// distinct content and reason. The content is judged again every poll, so a
// refusal that can be fixed without touching the content (a file mode) lifts
// by itself; only the saying is deduplicated.
func (p *Poller) reloadRefused(sum [sha256.Size]byte, why string) {
	key := sha256.Sum256(append(sum[:], why...))
	if key == p.reloadSeen {
		return
	}
	p.reloadSeen = key
	p.log("config-reload-refused", "", "", "", "the configuration file changed but was not applied; the poller keeps the one it has: "+printable(why, 400))
}

// printable is s on one line, control characters dropped, cut to at most max
// runes: the text may come from a file an operator did not write.
func printable(s string, max int) string {
	var b strings.Builder
	for _, r := range strings.Join(strings.Fields(s), " ") {
		if unicode.IsControl(r) || r == utf8.RuneError {
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	if r := []rune(out); len(r) > max {
		out = string(r[:max]) + "…"
	}
	return out
}

// reloadableChanges names the reloadable fields other than skip that differ.
func reloadableChanges(c, next *Config, skip string) []string {
	var out []string
	a, b := reflect.ValueOf(c).Elem(), reflect.ValueOf(next).Elem()
	for _, name := range reloadable {
		if name == skip {
			continue
		}
		if !reflect.DeepEqual(a.FieldByName(name).Interface(), b.FieldByName(name).Interface()) {
			out = append(out, yamlName(name))
		}
	}
	sort.Strings(out)
	return out
}

func yamlName(field string) string {
	if f, ok := reflect.TypeOf(Config{}).FieldByName(field); ok {
		if n := strings.Split(f.Tag.Get("yaml"), ",")[0]; n != "" {
			return n
		}
	}
	return field
}

// diffWorkers lists worker references added to, removed from and changed in
// next, sorted.
func diffWorkers(c, next *Config) (added, removed, modified []string) {
	for ref, w := range next.Workers {
		old, ok := c.Workers[ref]
		switch {
		case !ok:
			added = append(added, ref)
		case !reflect.DeepEqual(old, w):
			modified = append(modified, ref)
		}
	}
	for ref := range c.Workers {
		if _, ok := next.Workers[ref]; !ok {
			removed = append(removed, ref)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(modified)
	return added, removed, modified
}

// fixedChanges names, by their file key, the settings that differ between the
// running configuration and next and are not reloadable.
func fixedChanges(c, next *Config) []string {
	var out []string
	t := reflect.TypeOf(c).Elem()
	a, b := reflect.ValueOf(c).Elem(), reflect.ValueOf(next).Elem()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() || isReloadable(f.Name) {
			continue
		}
		if !reflect.DeepEqual(a.Field(i).Interface(), b.Field(i).Interface()) {
			out = append(out, yamlName(f.Name))
		}
	}
	sort.Strings(out)
	return out
}

func reloadDetail(added, removed, modified, others []string) string {
	var parts []string
	if len(added) > 0 {
		parts = append(parts, "workers added: "+strings.Join(added, ", "))
	}
	if len(removed) > 0 {
		parts = append(parts, "workers removed: "+strings.Join(removed, ", "))
	}
	if len(modified) > 0 {
		parts = append(parts, "workers changed: "+strings.Join(modified, ", "))
	}
	if len(others) > 0 {
		parts = append(parts, "also changed: "+strings.Join(others, ", "))
	}
	return fmt.Sprintf("the configuration file changed and was applied (%s)", strings.Join(parts, "; "))
}

// namedFile is one path a changed entry names, and whether the poller runs it
// (a program) or an agent runs in it / a clone lives in it (a directory).
type namedFile struct {
	path    string
	program bool
}

// namedFiles is what the entries that changed would make the poller run or
// use: each worker's launch, end and recover programs, the launcher account
// program of a worker or account that changed, a changed repo's clone, and the
// directories a worker or account names for its agent to run in (CODEX_HOME,
// CLAUDE_CONFIG_DIR and TMPDIR hold settings and hooks an agent runs).
func namedFiles(c, next *Config, workers []string) []namedFile {
	var out []namedFile
	argv0 := func(argv []string) {
		if len(argv) > 0 && strings.TrimSpace(argv[0]) != "" {
			out = append(out, namedFile{argv[0], true})
		}
	}
	seen := map[string]bool{}
	account := func(name string) {
		if a, ok := next.Accounts[name]; ok && a.Bin != "" && !seen[name] {
			seen[name] = true
			out = append(out, namedFile{a.Bin, true})
		}
	}
	dir := func(path string) {
		if strings.TrimSpace(path) != "" {
			out = append(out, namedFile{path, false})
		}
	}
	for _, ref := range workers {
		w := next.Workers[ref]
		argv0(w.Launch)
		argv0(w.End)
		argv0(w.Recover)
		dir(w.CodexHome)
		dir(w.ClaudeConfigDir)
		dir(w.DiskTmp)
		if w.Profile != nil {
			account(w.Profile.Account)
		}
	}
	for name, a := range next.Accounts {
		if !reflect.DeepEqual(c.Accounts[name], a) {
			account(name)
			dir(a.CodexHome)
		}
	}
	for token, dir := range next.Repos {
		if c.Repos[token] != dir {
			out = append(out, namedFile{dir, false})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}
