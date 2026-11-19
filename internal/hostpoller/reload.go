package hostpoller

import (
	"crypto/sha256"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
)

// cli#2623: a running poller picks up a change to its configuration file.
//
// The poller used to read the file once, at start, so a worker added to the
// file while the poller ran was never registered and never got a wake until
// someone restarted it. Each poll now compares the file's content to the
// content the poller holds and, when it changed, loads and checks the new
// file exactly as start does. Only what workers depend on is applied live:
// the workers map, the repos worktree.create clones from and the account
// profiles a launcher profile names. Anything else (the key, the poll
// interval, delivery, the capability list, the state file) is read at start
// by code that has already wired itself from it, so a change to it is logged
// as needing a restart and is not applied. A file that does not load or does
// not pass the start-time checks changes nothing: the poller keeps what it
// has and says so once for that content.

// reloadable names the Config fields a running poller swaps.
var reloadable = map[string]bool{"Workers": true, "Repos": true, "Accounts": true}

// reloadConfig is called at the top of every Step. It never fails a poll.
func (p *Poller) reloadConfig() {
	c := p.cfg
	path, held := c.source()
	if path == "" {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		p.reloadRefused(sha256.Sum256([]byte(err.Error())), "the configuration file cannot be read: "+err.Error())
		return
	}
	sum := sha256.Sum256(raw)
	if sum == held {
		p.reloadClear()
		return
	}
	if sum == p.reloadSeen {
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
	if next.UsesOrcaLaunch() && !c.UsesOrcaLaunch() {
		p.reloadRefused(sum, "a worker now launches through Orca, which this poller did not wire at start; restart it to apply any of this change")
		return
	}
	changed := fixedChanges(c, next)
	added, removed, modified := diffWorkers(c, next)
	apply := len(added)+len(removed)+len(modified) > 0 || !reflect.DeepEqual(c.reloadView(), next.reloadView())
	if apply {
		c.swapReloadable(next)
	} else {
		c.setSourceHash(sum)
	}
	p.reloadSeen = [sha256.Size]byte{}
	if apply {
		p.log("config-reloaded", "", "", "", reloadDetail(added, removed, modified, c.reloadChangedBeyondWorkers(next)))
	}
	if len(changed) > 0 {
		p.log("config-restart-needed", "", "", "", "changed in the file but read only at start, so not applied: "+strings.Join(changed, ", ")+"; restart the poller to apply it")
	}
}

// reloadRefused keeps the running configuration and says why, once for each
// distinct content.
func (p *Poller) reloadRefused(sum [sha256.Size]byte, why string) {
	if sum == p.reloadSeen {
		return
	}
	p.reloadSeen = sum
	p.log("config-reload-refused", "", "", "", "the configuration file changed but was not applied; the poller keeps the one it has: "+oneLine(why))
}

// reloadClear forgets a refused content once the file is back to what the
// poller holds, so the same bad content later is reported again.
func (p *Poller) reloadClear() { p.reloadSeen = [sha256.Size]byte{} }

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 400 {
		s = s[:400] + "…"
	}
	return s
}

func (c *Config) source() (string, [sha256.Size]byte) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sourcePath, c.sourceHash
}

func (c *Config) setSourceHash(h [sha256.Size]byte) {
	c.mu.Lock()
	c.sourceHash = h
	c.mu.Unlock()
}

// reloadView is the part of a Config a reload swaps, for comparison.
func (c *Config) reloadView() [3]any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return [3]any{c.Workers, c.Repos, c.Accounts}
}

// swapReloadable installs next's workers, repos and account profiles, and the
// content hash they came from, under the lock readers take.
func (c *Config) swapReloadable(next *Config) {
	c.mu.Lock()
	c.Workers, c.Repos, c.Accounts, c.sourceHash = next.Workers, next.Repos, next.Accounts, next.sourceHash
	c.mu.Unlock()
}

// reloadChangedBeyondWorkers names the reloadable maps other than workers
// that differ, for the event line.
func (c *Config) reloadChangedBeyondWorkers(next *Config) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []string
	if !reflect.DeepEqual(c.Repos, next.Repos) {
		out = append(out, "repos")
	}
	if !reflect.DeepEqual(c.Accounts, next.Accounts) {
		out = append(out, "account_profiles")
	}
	return out
}

// diffWorkers lists worker references added to, removed from and changed in
// next, sorted.
func diffWorkers(c, next *Config) (added, removed, modified []string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
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
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []string
	t := reflect.TypeOf(c).Elem()
	a, b := reflect.ValueOf(c).Elem(), reflect.ValueOf(next).Elem()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() || reloadable[f.Name] {
			continue
		}
		if !reflect.DeepEqual(a.Field(i).Interface(), b.Field(i).Interface()) {
			name := strings.Split(f.Tag.Get("yaml"), ",")[0]
			if name == "" {
				name = f.Name
			}
			out = append(out, name)
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
	parts = append(parts, others...)
	return fmt.Sprintf("the configuration file changed and was applied (%s)", strings.Join(parts, "; "))
}
