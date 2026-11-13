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
// which is kept — an empty argv slot is not nothing (#1474). An empty read
// — kernel threads, zombies — is not an argv at all: false, so comm answers
// instead (Split("") returns [""], which would render as a bare quoted
// empty argument and starve the comm fallback). A setproctitle-padded
// cmdline carries runs of trailing NULs beyond that, which read as empty
// arguments; a RUN of trailing empties is padding and collapses to one
// HERE — at the /proc boundary where padding happens — so the display bound
// is not spent on it and renderArgv renders the slots it is given (the
// collapse used to live in renderArgv and ate genuine slots). Residual,
// accepted: in /proc a genuine argv with two or more trailing empty slots
// is byte-identical to padding, so it collapses with it and one empty slot
// renders.
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
// name containing auth, token, key, secret or cookie (#1474).
// Over-matching is safe — the name alone still identifies the header; the
// value never leaves.
var credentialHeader = regexp.MustCompile(`(?i)^[\w-]*(auth|token|key|secret|cookie)[\w-]*\s*:`)

// credentialFlagValue says how a binary's short flag carries a credential:
// as the NEXT argument (curl -u user:pass), attached to the flag itself
// (mysql -pSECRET), or both (sshpass -p secret / sshpass -psecret).
type credentialFlagValue struct {
	next     bool
	attached bool
}

// credentialFlagBinaries is the per-binary table of flags that carry a
// credential, because the same letters mean different things in different
// tools: curl's -u/--user and -U/--proxy-user take user:pass as the next
// argument or attached (-uuser:pass, --user=u:p); curl's --pass,
// --proxy-pass, --oauth2-bearer and --proxy-oauth2-bearer carry a secret
// next or attached, and its -E/--cert and --proxy-cert carry
// keyfile:password the same two ways; header values (-H/--header,
// --proxy-header) need no table entry — the argument itself is a header
// line, matched by credentialHeader whatever flag carried it; sshpass's -p
// takes the password next or attached; mysql's -p takes the password
// ATTACHED only — `-p db` names a database and prompts instead. ssh's -p
// is a port and ssh takes no password on the command line, so it is not in
// any table; python -u (unbuffered) and find -print stay readable for the
// same reason (#1474). The tables ACCUMULATE at every argv entry whose
// basename is in the map, not only argv[0], because wrapper launchers run
// in front (sudo curl -u …, env curl -u …, timeout 30 sshpass -p …) and
// argv[0]-gating leaked the credential for every wrapped form — and a
// later table must never DISARM an earlier one: `curl -o mysql -u u:p`
// would switch to mysql's table and leak curl's -u value, and the
// replacement is not knowable from the argv alone (an output FILE named
// mysql is not the mysql binary), so the union of every armed table
// applies, and where two tables spell the same flag differently the
// more-redacting spec wins (next|attached beats either alone: over-
// redaction is the safe direction under this contract) (#1474). Residuals, deliberate: a command line hidden inside ONE argument
// (sh -c "curl -u …") is not tokenised here — it renders as one quoted
// argument, and only its URL-shaped userinfo is stripped (stripURLUserinfo)
// — bundled short flags (curl -su user:pass) are not unclustered into
// their letters, and curl's unambiguous long-option abbreviations
// (--proxy-use for --proxy-user) are not expanded: only exact spellings
// and their '='-attached values are matched.
var credentialFlagBinaries = map[string]map[string]credentialFlagValue{
	"curl": {
		"-u": {next: true, attached: true}, "--user": {next: true, attached: true},
		"-U": {next: true, attached: true}, "--proxy-user": {next: true, attached: true},
		"--pass": {next: true, attached: true}, "--proxy-pass": {next: true, attached: true},
		"--oauth2-bearer":       {next: true, attached: true},
		"--proxy-oauth2-bearer": {next: true, attached: true},
		"-E":                    {next: true, attached: true}, "--cert": {next: true, attached: true},
		"--proxy-cert": {next: true, attached: true},
	},
	"sshpass": {"-p": {next: true, attached: true}},
	"mysql":   {"-p": {attached: true}},
}

// matchCredentialFlag finds the table's flag that f carries. A SHORT flag
// carries its value attached with no separator (curl -uuser:pass), so a
// bare prefix matches it; a LONG flag carries one only after '=' (--user=u:p),
// so it matches exactly or '='-delimited — a bare prefix (--user-agent) is
// a different flag and must stay readable. The longest matching flag wins,
// so a table holding a flag and its longer spelling cannot mis-split.
func matchCredentialFlag(table map[string]credentialFlagValue, f string) (credentialFlagValue, string) {
	var best credentialFlagValue
	var bestFlag string
	for flag, spec := range table {
		var matched bool
		switch {
		case f == flag:
			matched = true
		case strings.HasPrefix(flag, "--"):
			matched = strings.HasPrefix(f, flag+"=")
		default:
			matched = len(f) > len(flag) && strings.HasPrefix(f, flag)
		}
		if matched && len(flag) > len(bestFlag) {
			best, bestFlag = spec, flag
		}
	}
	return best, bestFlag
}

// stripURLUserinfo removes user:password@ (or user@) from every URL-shaped
// occurrence in an argument: https://u:p@host/path renders as
// https://host/path, and a shell string carrying two URLs loses both
// userinfos (#1474). The cut for each URL is the LAST @ before the
// authority's end — the first /, ? or # after the scheme, or any whitespace
// or quote character, none of which a URL's authority can carry raw — so an
// unencoded @ inside a password leaves none of its tail behind, and a
// URL-shaped prefix running into prose (`https://h1 mail user@example.com`)
// ends its authority at the space instead of cutting the prose at a later
// @. Residual, deliberate: a password containing an unencoded /, ? or # is
// indistinguishable from the path boundary and is not stripped.
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
// bounded (#1474: name WHICH process holds the tree, never its secrets, in
// the reason and in the logs).
func renderArgv(fields []string) string {
	out := make([]string, 0, len(fields))
	credTable := map[string]credentialFlagValue{}
	credFrom := -1
	redactNext := false
	for i, f := range fields {
		f = stripURLUserinfo(f)
		// Arm at EVERY credential-flag binary on the line, ACCUMULATING:
		// the union of the armed tables applies to everything after the
		// first one, and a later binary never disarms an earlier one's
		// flags — whether a later table-binary spelling is the binary or
		// an argument of the last one (`curl -o mysql -u u:p`) is not
		// knowable from argv alone, and the union redacts both readings.
		if table, ok := credentialFlagBinaries[filepath.Base(f)]; ok {
			if credFrom < 0 {
				credFrom = i
			}
			for flag, spec := range table {
				cur := credTable[flag]
				// More-redacting wins: next|attached beats either alone.
				cur.next = cur.next || spec.next
				cur.attached = cur.attached || spec.attached
				credTable[flag] = cur
			}
		}
		switch {
		case redactNext:
			// The previous argument was a bare credential flag (--token,
			// -u, --user); this one is its value.
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
			if strings.HasPrefix(f, "-") {
				// Only arguments AFTER the first credential-flag binary
				// carry its flags; a wrapper's own arguments never do.
				// The table match runs for '='-attached long spellings too
				// (--user=u:p) — the '='-cut above only governed the
				// credential-NAME assignment form.
				if credFrom >= 0 && i > credFrom {
					if spec, flag := matchCredentialFlag(credTable, f); flag != "" {
						switch {
						case spec.next && len(f) == len(flag):
							// The bare flag: its value is the next argument.
							out = append(out, quoteArg(f))
							redactNext = true
							continue
						case spec.attached && len(f) > len(flag):
							// An attached value (mysql -pSECRET, curl
							// -uuser:pass, curl --user=u:p): the flag stays,
							// the value never leaves. Checked BEFORE the
							// credential-name branch — the value attached to
							// a credential flag can itself contain "secret".
							// A long flag's separator '=' stays with the flag.
							rest := "<redacted>"
							if strings.HasPrefix(f[len(flag):], "=") {
								rest = "=<redacted>"
							}
							out = append(out, flag+rest)
							continue
						}
						// A bare attached-only flag (mysql -p) carries no
						// value here and none next: readable as-is.
						out = append(out, quoteArg(f))
						continue
					}
				}
				if !hasValue && credentialName.MatchString(f) {
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
// truncation back off past them — are quoted, so a plain `sleep 30` stays
// readable while `sh -c "sleep 30 & wait"` keeps its shape and an invalid
// byte arrives as an escape, never raw.
func quoteArg(arg string) string {
	if arg == "" || !utf8.ValidString(arg) || strings.IndexFunc(arg, func(r rune) bool {
		return unicode.IsSpace(r) || r == '"' || r == '\\' || !unicode.IsPrint(r)
	}) >= 0 {
		return strconv.Quote(arg)
	}
	return arg
}

// signalGroup and probeGroup are the group primitives terminateGroup uses,
// as package vars so tests can make them fail (#1474: the signal-fails and
// does-not-exit paths are otherwise unreachable on a healthy host).
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
// TerminateGrace again: SIGKILL cannot be caught or ignored, so only an
// unkillable (D-state) member can outlive it, and that is reported rather
// than waited on twice.
var postKillWait = 2 * time.Second

// groupGone reports whether the process group no longer exists: an ESRCH
// answer to a signal-0 probe of the group.
func groupGone(pgid int) bool {
	return errors.Is(probeGroup(pgid), syscall.ESRCH)
}

// stop ends the launch by its process group, and is safe at any point of
// the agent's life because the launch's own leader is the ownership proof:
// while it is alive, or while it is the unreaped zombie the hold keeps
// (waitid WNOWAIT or the /proc poll, reapgroup_linux.go), its pid — the
// process-group id — stays allocated to THIS launch, so a probe or a
// signal by that number can only ever reach this launch's group, never a
// process that took a reused number, and an ESRCH answer means the group
// is gone, not that the number was taken (#1474: a pidfd holds the pid
// OBJECT but not its allocator slot — free_pid drops the idr entry at the
// reap, so a reaped launch's number can be retaken and killed by number;
// the zombie hold is what closes that). The zombie is reaped HERE, only
// once the group has drained (or the group was already proven empty at
// the waiter's reap — or by its drain watcher), so the number is never
// public while the launch may still be signalled. Launches without any
// hold — the non-Linux Unix platforms, and a Linux launch whose waitid
// was refused — reap at the leader's exit; their stop signals only while
// the leader is live and aborts to unconfirmed at the first sight of the
// exit, keeping every record: the reuse residual documented in host_run's
// help. Windows gates on Done instead (detach_windows.go).
func (a *execAgent) stop() (bool, string) {
	if a.confirmed.Load() {
		return true, "" // a confirmed stop stays confirmed; a repeat signals nothing
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.holdsZombie() && a.exited.Load() && a.reaped.Load() {
		// On a hold-keeping launch a reap can precede a stop only after a
		// scan found the group EMPTY (emptyAtReap): nothing of this
		// launch ran at the reap, the number became public then, and a
		// group answering it now is not ours — confirm without touching
		// it. A reaped launch without that proof (a reap whose wait
		// failed) can claim nothing: report unconfirmed and keep the
		// records rather than signal a number the launch may no longer
		// own. A no-hold launch skips this branch entirely: its reap
		// proves nothing about the number, and its stop below confirms
		// only through the probe oracle an ESRCH answer closes.
		if a.emptyAtReap.Load() {
			a.confirmed.Store(true)
			return true, ""
		}
		return false, fmt.Sprintf("the launch (pid %d) could not be tracked through its exit; its stop is left unconfirmed and its records kept", a.pid)
	}
	stopped, detail := a.terminateGroup()
	if stopped {
		a.confirmed.Store(true)
		if a.exited.Load() {
			// The group has drained and the leader is the held zombie (a
			// no-hold platform never gets here post-exit — the abort
			// above returned first): reap now, releasing the number only
			// once nothing of the launch can be signalled.
			a.reap()
		}
	}
	return stopped, detail
}

// stopAuthorityGone reports whether this stop may no longer signal the
// group by number: a launch without the hold — a no-hold platform, or a
// hold-capable platform whose waitid was refused — frees the number at
// the leader's exit, unserialized with any stop, so once the exit is
// observed a signal by number could reach whatever took the reused
// number. While the leader is live the number is bound to it and
// signalling stays sound. A launch keeping the zombie hold never loses
// authority: the zombie keeps the number allocated until a reap under the
// stop mutex, so false there is a fact, not a sample.
func (a *execAgent) stopAuthorityGone() bool {
	return !a.holdsZombie() && a.exited.Load()
}

// terminateGroup stops the launch's process group by number and confirms
// the WHOLE group is gone — the leader being gone is not the group being
// gone: a wrapper's child can outlive its leader (#1474). A launched agent
// runs as its own session leader (detach sets Setsid), so its group id is
// its pid, and every signal below goes out while the leader — live, or
// the zombie the hold keeps — owns that number. On a launch without the
// hold the loop re-checks stopAuthorityGone before every signal and every
// probe answer it acts on: the leader's exit there releases the number
// unserialized with this stop, and the first observation of it ends the
// procedure unconfirmed instead of signalling a number the launch may no
// longer own (#1474). SIGTERM, a bounded grace, SIGKILL, then a short
// fixed wait; stopped is false whenever the group could not be confirmed
// empty. Called with a.mu held.
func (a *execAgent) terminateGroup() (stopped bool, detail string) {
	pid := a.pid
	if a.drained() {
		// The group is already gone: nothing was running, nothing was
		// signalled, and the stop needs no words. This check runs BEFORE
		// the authority abort on purpose: an ESRCH answer is sound even
		// after the number went public — a group answering the number
		// would not be ESRCH — so a no-hold launch whose group is already
		// gone still confirms instead of degrading to unconfirmed.
		return true, ""
	}
	if a.stopAuthorityGone() {
		return false, a.unconfirmedDetail()
	}
	if err := signalGroup(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return false, fmt.Sprintf("could not stop the launched process group (pid %d): %v", pid, err)
	}
	if a.drainedOrAuthorityLost(a.grace) {
		if a.stopAuthorityGone() {
			return false, a.unconfirmedDetail()
		}
		return true, fmt.Sprintf("terminated the launched process group (pid %d)", pid)
	}
	if a.stopAuthorityGone() {
		return false, a.unconfirmedDetail()
	}
	if err := signalGroup(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return false, fmt.Sprintf("could not stop the launched process group (pid %d): it ignored SIGTERM for %s and SIGKILL failed: %v", pid, a.grace, err)
	}
	if a.drainedOrAuthorityLost(postKillWait) {
		if a.stopAuthorityGone() {
			return false, a.unconfirmedDetail()
		}
		return true, fmt.Sprintf("killed the launched process group (pid %d) after it ignored SIGTERM for %s", pid, a.grace)
	}
	return false, fmt.Sprintf("the launched process group (pid %d) did not exit within %s of SIGKILL", pid, postKillWait)
}

// unconfirmedDetail is the no-hold launch's abort wording: records kept,
// nothing signalled past the exit, the operator told why.
func (a *execAgent) unconfirmedDetail() string {
	return fmt.Sprintf("the launch (pid %d) exited while being stopped without the ownership hold (its platform or kernel policy provides none); its stop is left unconfirmed and its records kept", a.pid)
}

// drained reports whether nothing of this launch's group runs, other than
// the held zombie leader itself. While the leader is alive the number is
// the launch's and an ESRCH probe means the whole group — leader
// included — is gone. Once the leader has exited, a hold-keeping launch's
// zombie answers that probe forever, so the oracle switches to the /proc
// scan, which excludes the zombie; the zombie-state check catches the
// exit even in the waiter's notice lag (a TERM the stop itself just sent
// may have killed the leader a tick before the waiter stored exited) —
// and it requires the zombie's thread group to have FULLY exited: a
// leader in state Z whose thread count is still above one has running
// threads, so the group is not drained and the probe oracle below says so
// (those threads answer it) — Z alone is not proof a leader's whole
// thread group left (#1474). On a launch without the hold there is no
// zombie (the waiter reaped at the exit) and the probe keeps its meaning
// until stopAuthorityGone ends the procedure.
func (a *execAgent) drained() bool {
	if a.holdsZombie() && (a.exited.Load() || leaderZombieAlone(a.pid)) {
		return !groupHasOtherMembers(a.pid, a.pid)
	}
	return groupGone(a.pid)
}

// drainedOrAuthorityLost polls until drained, the wait passes, or — on a
// platform without the hold — the leader's exit is observed, which ends
// the stop's authority to signal (the caller then answers unconfirmed).
// True when drained was seen; the caller re-checks stopAuthorityGone to
// tell "drained" from "aborted". Called with a.mu held.
func (a *execAgent) drainedOrAuthorityLost(wait time.Duration) bool {
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
			if a.drained() || a.stopAuthorityGone() {
				return true
			}
		case <-timeout.C:
			return a.drained() || a.stopAuthorityGone()
		}
	}
}
