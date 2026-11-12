//go:build !windows

package hostpoller

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

// detach starts the agent in a new session, so it outlives the poller and
// the poller's signals do not reach it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func realPath(p string) (string, error) { return filepath.EvalSymlinks(p) }

// keyFileTooOpen: any group or other permission bit.
func keyFileTooOpen(info os.FileInfo) bool { return info.Mode().Perm()&0o077 != 0 }

// processAlive: signal 0 probes without delivering; EPERM still means a
// process holds the pid.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// occupied scans /proc/<pid>/cwd for a process working inside path and names
// it by pid and argv (#1474: an operator must be able to tell WHICH process
// holds the tree — the comm name alone cannot). Only processes this user may
// inspect are seen, which covers agents the holder's user runs. Without /proc
// (macOS) it reports none.
func occupied(path string) (string, error) {
	root, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir("/proc")
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	self := os.Getpid()
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue
		}
		cwd, err := os.Readlink(filepath.Join("/proc", e.Name(), "cwd"))
		if err != nil {
			continue // gone, or not ours to inspect
		}
		if cwd == root || strings.HasPrefix(cwd, root+string(os.PathSeparator)) {
			return fmt.Sprintf("pid %d, %s", pid, processArgv(e.Name())), nil
		}
	}
	return "", nil
}

// processArgv names a process by its command line (/proc/<pid>/cmdline,
// NUL-separated), falling back to comm for a process with no command line
// (a kernel thread) or an unreadable one. The line is rendered by
// renderArgv — quoted, redacted and bounded — because it leaves the host in
// a refusal reason and the logs (#1474).
func processArgv(dir string) string {
	raw, err := os.ReadFile(filepath.Join("/proc", dir, "cmdline"))
	if fields, ok := argvFromCmdline(raw); err == nil && ok {
		return renderArgv(fields)
	}
	comm, _ := os.ReadFile(filepath.Join("/proc", dir, "comm"))
	return strings.TrimSpace(string(comm))
}

// argvFromCmdline splits a /proc/<pid>/cmdline read: NUL-separated, ending
// in ONE terminator NUL. A further trailing NUL is an empty LAST argument,
// which is kept — an empty argv slot is not nothing (#1474 review; Jane D3:
// TrimRight ate every empty trailing slot). An empty read — kernel threads,
// zombies — is not an argv at all: false, so comm answers instead (#1474
// delta review P3-1: Split("") returns [""], which used to render as a bare
// quoted empty argument and starve the comm fallback). A setproctitle-
// padded cmdline carries runs of trailing NULs beyond that, which read as
// empty arguments; a RUN of trailing empties is padding and collapses to
// one HERE — at the /proc boundary where padding happens — so the display
// bound is not spent on it and renderArgv renders the slots it is given
// (delta round 2 F6; round 3 Jane D3: the collapse used to live in
// renderArgv and ate genuine slots). Residual, accepted (Jane D3,
// optional): in /proc a genuine argv with two or more trailing empty
// slots is byte-identical to padding, so it collapses with it and one
// empty slot renders.
func argvFromCmdline(raw []byte) ([]string, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	fields := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
	for len(fields) > 1 && fields[len(fields)-1] == "" && fields[len(fields)-2] == "" {
		fields = fields[:len(fields)-1]
	}
	return fields, true
}

// argvDisplayLimit bounds how much of an occupant's command line leaves the
// host, in bytes. The pid, the executable and a short prefix of the rest are
// what an operator needs to tell WHICH process holds the tree (#1474); the
// rest of an arbitrary command line is not this host's to send or log.
const argvDisplayLimit = 200

// credentialName matches the NAME of an assignment (NAME=value,
// --flag=value) whose value looks like a credential. Over-matching is safe —
// the name alone still identifies the process; the value never leaves.
var credentialName = regexp.MustCompile(`(?i)(token|secret|password|passwd|passphrase|credential|api[-_]?key|private[-_]?key|session|authorization|cookie)`)

// credentialHeader matches an argument that IS a header whose name says it
// carries a credential ("Authorization: Bearer …", "Cookie: …",
// "X-API-Key: …", "X-Auth-Token: …", "Proxy-Authorization: …"): any header
// name containing auth, token, key, secret or cookie (#1474 delta review
// D2). Over-matching is safe — the name alone still identifies the header;
// the value never leaves.
var credentialHeader = regexp.MustCompile(`(?i)^[\w-]*(auth|token|key|secret|cookie)[\w-]*\s*:`)

// credentialFlagValue says how a binary's short flag carries a credential:
// as the NEXT argument (curl -u user:pass), attached to the flag itself
// (mysql -pSECRET), or both (sshpass -p secret / sshpass -psecret).
type credentialFlagValue struct {
	next     bool
	attached bool
}

// credentialFlagBinaries is the per-binary table of short flags that carry
// a credential, because the same letters mean different things in different
// tools: curl's -u and -U (--proxy-user) take user:pass next or attached;
// sshpass's -p takes the password next or attached; mysql's -p takes the
// password ATTACHED only — `-p db` names a database and prompts instead.
// ssh's -p is a port and ssh takes no password on the command line, so it
// is not in any table; python -u (unbuffered) and find -print stay readable
// for the same reason (#1474 delta review P3, round 2 F5d; round 3 Jonas
// N3: one shared -u/-p set was both too wide and too narrow). The rules arm
// from the FIRST argv entry whose basename is in the table, not only
// argv[0], because wrapper launchers run in front (sudo curl -u …,
// env curl -u …, timeout 30 sshpass -p …) and argv[0]-gating leaked the
// credential for every wrapped form (N3). Residual, deliberate: a command
// line hidden inside ONE argument (sh -c "curl -u …") is not tokenised
// here — it renders as one quoted argument, and only its URL-shaped
// userinfo is stripped (stripURLUserinfo).
var credentialFlagBinaries = map[string]map[string]credentialFlagValue{
	"curl":    {"-u": {next: true, attached: true}, "-U": {next: true, attached: true}},
	"sshpass": {"-p": {next: true, attached: true}},
	"mysql":   {"-p": {attached: true}},
}

// credentialFlagTable returns the flag table of the FIRST argv entry naming
// a credential-flag binary, and that entry's index, so the arguments before
// it — the wrapper's own — are never treated as credential flags.
func credentialFlagTable(argv []string) (map[string]credentialFlagValue, int, bool) {
	for i, f := range argv {
		if table, ok := credentialFlagBinaries[filepath.Base(f)]; ok {
			return table, i, true
		}
	}
	return nil, -1, false
}

// matchCredentialFlag finds the table's flag that f carries: either the
// bare flag (-u) or the flag with its value attached (-uuser:pass). The
// longest matching flag wins, so a table holding both a flag and its
// longer spelling cannot mis-split.
func matchCredentialFlag(table map[string]credentialFlagValue, f string) (credentialFlagValue, string) {
	var best credentialFlagValue
	var bestFlag string
	for flag, spec := range table {
		if strings.HasPrefix(f, flag) && len(flag) > len(bestFlag) {
			best, bestFlag = spec, flag
		}
	}
	return best, bestFlag
}

// stripURLUserinfo removes user:password@ (or user@) from every URL-shaped
// occurrence in an argument: https://u:p@host/path renders as
// https://host/path, and a shell string carrying two URLs loses both
// userinfos (#1474 delta review P3, round 2 F5c). The cut for each URL is
// the LAST @ before the authority's end — the first /, ? or # after the
// scheme, or any whitespace or quote character, none of which a URL's
// authority can carry raw — so an unencoded @ inside a password leaves
// none of its tail behind (round 2 F5a) and a URL-shaped prefix running
// into prose (`https://h1 mail user@example.com`) ends its authority at
// the space instead of cutting the prose at a later @ (round 3, Jonas
// P3-1). Residual, deliberate: a password containing an unencoded /, ? or
// # is indistinguishable from the path boundary and is not stripped.
// git@host:path and bare addresses carry no scheme separator and pass
// through.
func stripURLUserinfo(arg string) string {
	const authorityEnd = "/?# \t\r\n\v\f\"'`"
	var out strings.Builder
	rest := arg
	for {
		i := strings.Index(rest, "://")
		if i < 0 {
			out.WriteString(rest)
			return out.String()
		}
		out.WriteString(rest[:i+3])
		after := rest[i+3:]
		end := len(after)
		if j := strings.IndexAny(after, authorityEnd); j >= 0 {
			end = j
		}
		if at := strings.LastIndexByte(after[:end], '@'); at >= 0 {
			after = after[at+1:]
			// after now starts at the host; its authority ends at the same
			// boundary — the first character of the same set in the
			// remainder.
			if j := strings.IndexAny(after, authorityEnd); j >= 0 {
				end = j
			} else {
				end = len(after)
			}
		}
		out.WriteString(after[:end])
		rest = after[end:]
	}
}

// renderArgv renders a command line for the occupied-tree refusal: an
// argument that would not survive a re-split on spaces is %q-quoted, a
// credential-shaped value keeps only its name, and the whole line is
// bounded (#1474 review P2-4: name WHICH process holds the tree, never its
// secrets, in the reason and in the logs).
func renderArgv(fields []string) string {
	credTable, credFrom, credArmed := credentialFlagTable(fields)
	out := make([]string, 0, len(fields))
	redactNext := false
	for i, f := range fields {
		f = stripURLUserinfo(f)
		switch {
		case redactNext:
			// The previous argument was a bare credential flag (--token,
			// -u, -p); this one is its value.
			out = append(out, "<redacted>")
			redactNext = false
		case credentialHeader.MatchString(f):
			name, _, _ := strings.Cut(f, ":")
			out = append(out, name+": <redacted>")
		default:
			name, _, hasValue := strings.Cut(f, "=")
			if hasValue && credentialName.MatchString(name) {
				out = append(out, name+"=<redacted>")
				continue
			}
			if !hasValue && strings.HasPrefix(f, "-") {
				// Only arguments AFTER the credential-flag binary carry its
				// flags; the wrapper's own arguments never do (Jonas N3).
				if credArmed && i > credFrom {
					if spec, flag := matchCredentialFlag(credTable, f); flag != "" {
						switch {
						case spec.next && len(f) == len(flag):
							// The bare flag: its value is the next argument.
							out = append(out, quoteArg(f))
							redactNext = true
							continue
						case spec.attached && len(f) > len(flag):
							// An attached short-flag value (mysql -pSECRET, curl
							// -uuser:pass): the flag stays, the value never
							// leaves. Checked BEFORE the credential-name branch —
							// the value attached to -p can itself contain
							// "secret".
							out = append(out, flag+"<redacted>")
							continue
						}
						// A bare attached-only flag (mysql -p) carries no
						// value here and none next: readable as-is.
						out = append(out, quoteArg(f))
						continue
					}
				}
				if credentialName.MatchString(f) {
					out = append(out, quoteArg(f))
					redactNext = true
					continue
				}
			}
			out = append(out, quoteArg(f))
		}
	}
	line := strings.Join(out, " ")
	if len(line) > argvDisplayLimit {
		// Never slice a rune in half: back the cut off to a rune boundary.
		// Every argument is valid UTF-8 by now (quoteArg escapes invalid
		// bytes), so this back-off is bounded by one rune's length.
		cut := argvDisplayLimit
		for cut > 0 && !utf8.RuneStart(line[cut]) {
			cut--
		}
		line = line[:cut] + "…"
	}
	return line
}

// quoteArg keeps an argument unambiguous in the joined line: only arguments
// that would not survive being re-split on spaces — or that carry invalid
// UTF-8, which would otherwise both render raw and make the byte-bound
// truncation back off past them (#1474 delta review P3) — are quoted, so a
// plain `sleep 30` stays readable while `sh -c "sleep 30 & wait"` keeps its
// shape and an invalid byte arrives as an escape, never raw.
func quoteArg(arg string) string {
	if arg == "" || !utf8.ValidString(arg) || strings.IndexFunc(arg, func(r rune) bool {
		return unicode.IsSpace(r) || r == '"' || r == '\\' || !unicode.IsPrint(r)
	}) >= 0 {
		return strconv.Quote(arg)
	}
	return arg
}

// signalGroup and probeGroup are the group primitives terminateGroup uses,
// as package vars so tests can make them fail (#1474 review: the
// signal-fails and does-not-exit paths are otherwise unreachable on a
// healthy host).
var (
	signalGroup = func(pgid int, sig syscall.Signal) error {
		return syscall.Kill(-pgid, sig)
	}
	probeGroup = func(pgid int) error {
		return syscall.Kill(-pgid, 0)
	}
)

// groupPollInterval is how often terminateGroup probes a group it is
// waiting on. It is far below any sane grace, so the probe cost bounds the
// latency of noticing the group emptied.
var groupPollInterval = 20 * time.Millisecond

// postKillWait bounds the wait after SIGKILL. It is deliberately NOT
// TerminateGrace again (the review's double-grace): SIGKILL cannot be
// caught or ignored, so only an unkillable (D-state) member can outlive it,
// and that is reported rather than waited on twice.
var postKillWait = 2 * time.Second

// groupGone reports whether the process group no longer exists: an ESRCH
// answer to a signal-0 probe of the group.
func groupGone(pgid int) bool {
	return errors.Is(probeGroup(pgid), syscall.ESRCH)
}

// stop ends the launch by its process group, and is safe at any point of
// the agent's life because the launch's own leader is the ownership proof:
// while it is alive, or while it is the unreaped zombie the waiter holds
// (waitid WNOWAIT, reapgroup_linux.go), its pid — the process-group id
// — stays allocated to THIS launch, so a probe or a signal by that number
// can only ever reach this launch's group, never a process that took a
// reused number, and an ESRCH answer means the group is gone, not that the
// number was taken (#1474 delta round 3, Ada #102351, closing Jane D1 /
// Jonas N1 / Cameron's forced-reuse proof that a pidfd holds the pid
// OBJECT but not its allocator slot: free_pid drops the idr entry at the
// reap, so a reaped launch's number can be retaken and killed by number).
// The zombie is reaped HERE, only once the group has drained (or the group
// was already proven empty at the waiter's reap), so the number is never
// public while the launch may still be signalled. Platforms without the
// hold reap at the leader's exit and stop by number with the reuse
// residual documented in host_run's help. Windows gates on Done instead
// (detach_windows.go).
func (a *execAgent) stop() (bool, string) {
	if a.confirmed.Load() {
		return true, "" // a confirmed stop stays confirmed; a repeat signals nothing
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if leaderHoldCapable && a.exited.Load() && a.reaped.Load() {
		// On a hold-capable platform only the waiter reaps before a stop,
		// and it does so only after a scan found the group EMPTY
		// (emptyAtReap): nothing of this launch ran at the reap, the
		// number became public then, and a group answering it now is not
		// ours — confirm without touching it. A reap without that proof
		// (the waitid-failure fallback) can claim nothing: report
		// unconfirmed and keep the records rather than signal a number
		// the launch may not own. A platform without the hold takes none
		// of this: its waiter reaped at the exit by design, and the stop
		// below probes and signals the number with the reuse residual the
		// help documents for it.
		if a.emptyAtReap.Load() {
			a.confirmed.Store(true)
			return true, ""
		}
		return false, fmt.Sprintf("the launch (pid %d) could not be tracked through its exit (waitid failed); its stop is left unconfirmed and its records kept", a.pid)
	}
	stopped, detail := a.terminateGroup()
	if stopped {
		a.confirmed.Store(true)
		if a.exited.Load() {
			// The group has drained and the leader is the held zombie (a
			// no-hold platform reaped it already): reap now, releasing
			// the number only once nothing of the launch can be
			// signalled.
			a.reap()
		}
	}
	return stopped, detail
}

// terminateGroup stops the launch's process group by number and confirms
// the WHOLE group is gone — the leader being gone is not the group being
// gone: a wrapper's child can outlive its leader (#1474). A launched agent
// runs as its own session leader (detach sets Setsid), so its group id is
// its pid, and every signal below goes out while the leader — live, or
// the zombie the waiter holds — keeps that number this launch's.
// SIGTERM, a bounded grace, SIGKILL, then a short fixed wait; stopped is
// false whenever the group could not be confirmed empty. Called with a.mu
// held.
func (a *execAgent) terminateGroup() (stopped bool, detail string) {
	pid := a.pid
	if a.drained() {
		// The group is already gone: nothing was running, nothing was
		// signalled, and the stop needs no words.
		return true, ""
	}
	if err := signalGroup(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return false, fmt.Sprintf("could not stop the launched process group (pid %d): %v", pid, err)
	}
	if a.drainedWithin(a.grace) {
		return true, fmt.Sprintf("terminated the launched process group (pid %d)", pid)
	}
	if err := signalGroup(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return false, fmt.Sprintf("could not stop the launched process group (pid %d): it ignored SIGTERM for %s and SIGKILL failed: %v", pid, a.grace, err)
	}
	if a.drainedWithin(postKillWait) {
		return true, fmt.Sprintf("killed the launched process group (pid %d) after it ignored SIGTERM for %s", pid, a.grace)
	}
	return false, fmt.Sprintf("the launched process group (pid %d) did not exit within %s of SIGKILL", pid, postKillWait)
}

// drained reports whether nothing of this launch's group runs, other than
// the held zombie leader itself. While the leader is alive the number is
// the launch's and an ESRCH probe means the whole group — leader
// included — is gone. Once the leader has exited, a hold-capable
// platform's zombie answers that probe forever, so the oracle switches to
// the /proc scan, which excludes the zombie; on the platforms without the
// hold there is no zombie (the waiter reaped at the exit) and the probe
// keeps its meaning.
func (a *execAgent) drained() bool {
	if leaderHoldCapable && a.exited.Load() {
		return !groupHasOtherMembers(a.pid, a.pid)
	}
	return groupGone(a.pid)
}

// drainedWithin polls until drained or wait passes; true when drained was
// seen. Called with a.mu held.
func (a *execAgent) drainedWithin(wait time.Duration) bool {
	if a.drained() {
		return true
	}
	ticker := time.NewTicker(groupPollInterval)
	defer ticker.Stop()
	timeout := time.NewTimer(wait)
	defer timeout.Stop()
	for {
		select {
		case <-ticker.C:
			if a.drained() {
				return true
			}
		case <-timeout.C:
			return a.drained()
		}
	}
}
